package projectiongw

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// The read path's trust boundary used to be the tenant and nothing else: a
// subscriber entitled to a scope received every record in that scope for its
// organization, on both the snapshot and the delta stream. That is right when
// the organization IS the customer (B2B), and wrong for the other shape this
// foundation generates — a consumer product where every end user is a member of
// one organization. There "tenant" is not an audience, it is everybody, and the
// read path becomes a broadcast of every user's rows to every signed-in device.
//
// The fix cannot be a per-subscriber filter: the hub's whole performance model
// is encode once, share the frame. So the audience is a PARTITION KEY. A scope
// declared AudiencePerRecord fans out under tenant:domain:collection:@audience,
// one encoded frame per distinct audience in a batch (bounded by the batch, not
// by subscriber count), and a subscriber is registered only under the audience
// keys its own verified identity resolves to.
//
// Both halves of the read path derive the audience from the same declaration,
// which is the property that keeps them from drifting: the delta path partitions
// on it, and the snapshot path both filters the query on it and re-checks every
// returned record against it before answering.

// AudienceMode declares who, inside a tenant, may read a scope's records.
type AudienceMode uint8

const (
	// AudienceTenant delivers every record of a scope to every subscriber of
	// that tenant. It is the default, and it is the behavior every deployment
	// had before audiences existed, so an upgrade changes nothing until a
	// policy is declared.
	AudienceTenant AudienceMode = iota
	// AudiencePerRecord delivers a record only to the audience ids the record
	// itself names. A subscriber that resolves to no audience receives nothing
	// and is refused outright — never silently downgraded to tenant scope.
	AudiencePerRecord
)

var (
	// ErrAudienceForbidden is returned when a request reaches an
	// AudiencePerRecord scope without a resolvable audience: the mount declares
	// no SubjectAudienceFunc, the resolver returned nothing, or the caller is
	// not a member of any audience. It is a 403, never a fall back to tenant
	// scope — that fall back is exactly the bug this mechanism exists to close.
	ErrAudienceForbidden = errors.New("projectiongw: no resolvable audience for this scope")
	// ErrAudienceUndeclared is returned in strict mode for a scope that names
	// no audience policy. Strict mode makes an omission a refusal rather than
	// an accidental broadcast.
	ErrAudienceUndeclared = errors.New("projectiongw: scope has no declared audience policy")
	// ErrAudienceTooBroad is returned when a caller's audience set crosses so
	// many indexed fields that the snapshot would fan out past
	// MaxAudienceSnapshotReads. Refusing keeps the read O(limit) per request
	// (BoundedWork) instead of letting identity breadth drive scan cost.
	ErrAudienceTooBroad = errors.New("projectiongw: audience set exceeds the bounded snapshot fan-out")
	// ErrAudiencePolicyInvalid is returned by NewGateway when a declared policy
	// cannot be enforced — an AudiencePerRecord scope with no indexed field to
	// filter a snapshot on would be enforceable on deltas but not on reads.
	ErrAudiencePolicyInvalid = errors.New("projectiongw: invalid audience policy")
)

// MaxAudienceSnapshotReads bounds the (fields x audience ids) fan-out of one
// audience-scoped snapshot. Each combination is a separate bounded read of the
// partition, so the product — not the individual read — is what has to stay
// small for the request to remain O(limit).
const MaxAudienceSnapshotReads = 32

// RecordAudienceFunc returns the audience ids a record belongs to. It runs on
// the apply path for every accepted mutation of an AudiencePerRecord scope, so
// it must be cheap and allocation-light. Returning nothing means the record
// reaches nobody: fail-closed is the deliberate behavior, including for
// tombstones (see AudiencePolicy.Record).
type RecordAudienceFunc func(mutation *foundationpb.RecordMutation) []string

// SubjectAudienceFunc derives the caller's audience ids from a request. Like
// TenantFunc it is a trust boundary: the ids must come from verified claims or
// server-side lookup, never from a path, header, query or subscribe frame. A
// client that could name its own audience could name someone else's.
type SubjectAudienceFunc func(r *http.Request) ([]string, error)

// SecuritySubjectAudienceFunc is the default identity-derived audience: the
// authenticated subject from verified JWT claims, as one audience id. Projects
// whose records are owned by something other than the subject id (a profile id,
// a merchant id, a thread membership) supply their own — this one is the
// correct default only when records name the subject directly.
func SecuritySubjectAudienceFunc(r *http.Request) ([]string, error) {
	subject := strings.TrimSpace(security.GetUserIDFromContext(r.Context()))
	if subject == "" {
		return nil, ErrUnauthenticated
	}
	return []string{subject}, nil
}

// AudiencePolicy declares how one scope's records are addressed.
type AudiencePolicy struct {
	// Mode selects tenant-wide or per-record delivery.
	Mode AudienceMode
	// Fields names the record fields that carry an audience id (e.g.
	// "customer_profile_id", "merchant_profile_id"). It is required for
	// AudiencePerRecord and does double duty: it derives the delta partition
	// AND supplies the snapshot's equality filters, so the two halves cannot
	// disagree about who owns a record. Declare these fields in the hermes
	// ProjectionSpec's IndexedFields as well, or the snapshot stays correct but
	// pays a scan instead of an index lookup.
	Fields []string
	// Record optionally narrows the derivation beyond Fields — a thread's
	// participant list, say. It is authoritative for delivery: a record the
	// fields would have matched but Record excludes is not delivered. Fields is
	// still required, because a snapshot has to have something indexable to
	// filter on before Record re-checks the result.
	//
	// A tombstone carries only what the delete event carried. If deletes in
	// this scope do not carry the audience fields, Record (and the field
	// derivation) returns nothing for them and the delete reaches nobody —
	// fail-closed, counted by Gateway.AudienceDrops, and reconciled by the
	// client's next snapshot. Emit deletes carrying the audience fields to
	// converge deletions live.
	Record RecordAudienceFunc
}

// audiences returns the audience ids for one mutation under this policy. It
// runs once per accepted mutation on the apply path, so the single-audience
// case — the overwhelmingly common one — does not pay for a dedupe pass:
// stringField already trims and the builder already skips empties, so a result
// of one element is trivially clean.
func (p AudiencePolicy) audiences(mutation *foundationpb.RecordMutation) []string {
	return p.appendAudiences(nil, mutation)
}

// appendAudiences is audiences with a caller-supplied buffer, so the apply path
// can derive a record's audiences into a stack array instead of heap-allocating
// per mutation. dst is only read back by the caller; nothing retains it.
func (p AudiencePolicy) appendAudiences(dst []string, mutation *foundationpb.RecordMutation) []string {
	if p.Record != nil {
		for _, id := range p.Record(mutation) {
			if id = strings.TrimSpace(id); id != "" && !containsString(dst, id) {
				dst = append(dst, id)
			}
		}
		return dst
	}
	for _, field := range p.Fields {
		value := stringField(mutation, field)
		if value == "" || containsString(dst, value) {
			continue
		}
		dst = append(dst, value)
	}
	return dst
}

// AudienceConfig is the gateway's audience declaration.
type AudienceConfig struct {
	// Policies maps "{domain}/{collection}" to that scope's policy. A scope
	// absent from the map is AudienceTenant unless Strict is set.
	Policies map[string]AudiencePolicy
	// Strict refuses any scope with no declared policy (ErrAudienceUndeclared)
	// instead of defaulting it to tenant-wide delivery. Silent-broadcast-by-
	// default is the property that let this reach production unnoticed, so a
	// project that has finished declaring its scopes should set it and let an
	// omission read as a wiring error.
	Strict bool
}

// lookup returns the policy for a scope and whether one was declared.
func (c AudienceConfig) lookup(domain, collection string) (AudiencePolicy, bool) {
	policy, ok := c.Policies[domain+"/"+collection]
	return policy, ok
}

// resolve maps a scope onto the policy that governs it, refusing an undeclared
// scope in strict mode.
func (c AudienceConfig) resolve(domain, collection string) (AudiencePolicy, error) {
	policy, ok := c.lookup(domain, collection)
	if !ok {
		if c.Strict {
			return AudiencePolicy{}, fmt.Errorf("%w: %s/%s", ErrAudienceUndeclared, domain, collection)
		}
		return AudiencePolicy{Mode: AudienceTenant}, nil
	}
	return policy, nil
}

// validate rejects a policy set that could not be enforced on both halves of
// the read path. Construction is the right place to fail: a policy that only
// partitions deltas would leave the snapshot serving the whole tenant.
func (c AudienceConfig) validate() error {
	for key, policy := range c.Policies {
		if policy.Mode != AudiencePerRecord {
			continue
		}
		if len(dedupeAudiences(policy.Fields)) == 0 {
			return fmt.Errorf("%w: %s declares AudiencePerRecord with no Fields to filter a snapshot on", ErrAudiencePolicyInvalid, key)
		}
	}
	return nil
}

// dedupeAudiences trims, drops empties, and removes duplicates while keeping a
// stable order. Duplicate ids would register a subscriber twice in one bucket
// and encode a mutation into one group twice.
func dedupeAudiences(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if containsString(out, id) {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

// intersects reports whether the record's audiences include any of the
// caller's. It is the last gate on the snapshot path, applied to records the
// query filter already selected, so a wrong filter or an unexpected field type
// cannot turn into a disclosure.
func intersects(recordAudiences, subjectAudiences []string) bool {
	for _, id := range recordAudiences {
		if containsString(subjectAudiences, id) {
			return true
		}
	}
	return false
}

// stringField reads a string-valued field from a mutation. Only string audience
// ids are supported: they are what the hermes scalar index and the wire scope
// key both speak.
func stringField(mutation *foundationpb.RecordMutation, field string) string {
	for _, value := range mutation.GetFields() {
		if value.GetName() != field {
			continue
		}
		return strings.TrimSpace(value.GetValue().GetStringValue())
	}
	return ""
}

// audienceKeyPrefix separates the audience term from the scope term in a hub
// key. Scope components may not contain ':' (validateScope), so no scope can
// forge a key in another audience's bucket by naming a crafted collection.
const audienceKeyPrefix = ":@"

// AudienceScopeKey is the hub topic for one audience of a scope:
// tenant:domain:collection:@audience. AudienceTenant scopes keep the plain
// ScopeKey, so their fan-out is byte-for-byte what it was.
func AudienceScopeKey(scope *foundationpb.ProjectionScope, audience string) string {
	return ScopeKey(scope) + audienceKeyPrefix + audience
}

// audienceKeys returns the hub keys a subscriber with these audiences occupies.
func audienceKeys(scope *foundationpb.ProjectionScope, audiences []string) []string {
	keys := make([]string, 0, len(audiences))
	for _, audience := range audiences {
		keys = append(keys, AudienceScopeKey(scope, audience))
	}
	return keys
}

// snapshotAudienceFilters builds the (field, audience) equality filters an
// audience-scoped snapshot reads with — one bounded read per combination,
// unioned by the caller. Sorted so the reads are deterministic across requests.
func snapshotAudienceFilters(policy AudiencePolicy, audiences []string) ([]audienceFilter, error) {
	fields := dedupeAudiences(policy.Fields)
	if len(fields)*len(audiences) > MaxAudienceSnapshotReads {
		return nil, ErrAudienceTooBroad
	}
	filters := make([]audienceFilter, 0, len(fields)*len(audiences))
	for _, field := range fields {
		for _, audience := range audiences {
			filters = append(filters, audienceFilter{field: field, audience: audience})
		}
	}
	sort.Slice(filters, func(i, j int) bool {
		if filters[i].field != filters[j].field {
			return filters[i].field < filters[j].field
		}
		return filters[i].audience < filters[j].audience
	})
	return filters, nil
}

type audienceFilter struct {
	field    string
	audience string
}
