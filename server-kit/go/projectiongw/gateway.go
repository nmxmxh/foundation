package projectiongw

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"google.golang.org/protobuf/proto"
)

// DefaultSnapshotLimit bounds a snapshot when the caller does not specify a
// limit, keeping the read response bounded by default (BoundedWork).
const DefaultSnapshotLimit = 1024

var (
	// ErrScopeInvalid is returned when a ProjectionScope is missing required
	// fields (tenant, domain, collection).
	ErrScopeInvalid = errors.New("projectiongw: scope tenant_id, domain, and collection are required")
	// ErrNilStore is returned when a Gateway is constructed without a store.
	ErrNilStore = errors.New("projectiongw: hermes store is required")
)

// Resolver maps a domain-agnostic ProjectionScope onto a concrete hermes
// projection partition name plus the tenant-scoped Query used to read it. It is
// the single place a project customizes how scopes bind to partitions; the
// default (DomainResolver) treats the scope domain as the projection name and
// the scope tenant as the organization filter.
type Resolver func(scope *foundationpb.ProjectionScope) (projection string, query hermes.Query, err error)

// DomainResolver is the default Resolver: one projection partition per domain,
// filtered by tenant. Collection scoping is applied by the gateway as a
// per-record filter on the resolved partition.
func DomainResolver(limit int) Resolver {
	return func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		if err := validateScope(scope); err != nil {
			return "", hermes.Query{}, err
		}
		query := hermes.QueryWithFilters(scope.GetTenantId(), limit)
		return scope.GetDomain(), query, nil
	}
}

// Gateway is the generic read-path bridge over a hermes.Store. It serves scoped
// snapshots and, via an embedded Hub, fans live deltas out to subscribers. The
// projector must apply through the Gateway (ApplyEnvelopes) so applied mutations
// reach subscribers.
type Gateway struct {
	store    *hermes.Store
	hub      *Hub
	resolve  Resolver
	maxLimit int
	cancel   func()
	// warmScope lazily warms a cold scope on first read (see WithScopeWarmer):
	// a snapshot that would return ErrProjectionNotFound instead resolves the
	// scope through the projected store's warm path and retries once.
	warmScope func(ctx context.Context, scope *foundationpb.ProjectionScope) error
	// audience declares which scopes are addressed per record rather than
	// tenant-wide (see WithAudience). The zero value is every scope
	// AudienceTenant, which is the pre-audience behavior.
	audience AudienceConfig
	// audienceDrops counts accepted mutations that reached no subscriber
	// because their audience could not be derived — overwhelmingly tombstones
	// that carried no audience fields. Fanning them out to the whole tenant
	// would leak membership, so they are dropped and counted here instead of
	// disappearing silently; a client reconciles them on its next snapshot.
	audienceDrops atomic.Uint64
	// snapshotCache holds assembled snapshot responses keyed by epoch (see
	// WithSnapshotCache). Nil disables it, which is the default.
	snapshotCache *snapshotCache
}

// Option configures a Gateway.
type Option func(*Gateway)

// WithResolver overrides the default scope→partition Resolver.
func WithResolver(resolver Resolver) Option {
	return func(g *Gateway) {
		if resolver != nil {
			g.resolve = resolver
		}
	}
}

// WithScopeWarmer makes cold scopes self-resolving on first read: when a
// snapshot hits ErrProjectionNotFound, the gateway invokes warm (idempotent,
// singleflighted by the projected store) and retries once. With this wired, a
// scope never needs eager warming for correctness — seeded data resolves on
// the first request, and a scope with no data anywhere serves an empty
// snapshot instead of a wiring error.
func WithScopeWarmer(warm func(ctx context.Context, scope *foundationpb.ProjectionScope) error) Option {
	return func(g *Gateway) {
		g.warmScope = warm
	}
}

// WithAudience declares per-record audiences for scopes whose records are not
// addressed to the whole tenant. Scopes left undeclared keep tenant-wide
// delivery, so wiring this option changes nothing until a policy names a scope;
// set AudienceConfig.Strict once every scope is declared, so a scope that was
// forgotten is refused rather than broadcast. An unenforceable policy is a
// construction error (see AudienceConfig.validate), not a runtime surprise.
func WithAudience(config AudienceConfig) Option {
	return func(g *Gateway) {
		g.audience = config
	}
}

// WithSnapshotCache enables the epoch-keyed snapshot response cache with the
// given byte budget. Zero or negative disables it.
//
// The cache serves byte-identical repeat reads of an unchanged page without
// re-reading or re-encoding it, which is the shape a scope with many subscribers
// produces: every client that connects at the same epoch is asking for the same
// bytes. Validity is proven by the partition epoch, not by a clock — hermes
// advances the epoch on every accepted apply, so a matching epoch means the
// materialized page has not moved.
//
// It is opt-in because it trades resident memory for encode time, and only the
// deploying project knows its page sizes and subscriber counts. A budget is a
// hard ceiling: entries are evicted least-recently-used, and a page larger than
// the whole budget is never admitted.
func WithSnapshotCache(maxBytes int) Option {
	return func(g *Gateway) {
		g.snapshotCache = newSnapshotCache(maxBytes)
	}
}

// WithHub injects a shared Hub (e.g. one Hub across several gateways).
func WithHub(hub *Hub) Option {
	return func(g *Gateway) {
		if hub != nil {
			g.hub = hub
		}
	}
}

// NewGatewayForProjectedStore constructs a Gateway over a ProjectedRuntimeStore,
// wiring a Resolver that matches exactly how that store names its partitions
// (prefix:domain:collection:organization). This is the constructor the generated
// server uses: it guarantees scope→partition agreement with the write path, so
// snapshots find the same records the projected store materialized.
func NewGatewayForProjectedStore(projected *hermes.ProjectedRuntimeStore, queueSize int, opts ...Option) (*Gateway, error) {
	if projected == nil {
		return nil, ErrNilStore
	}
	resolver := func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		if err := validateScope(scope); err != nil {
			return "", hermes.Query{}, err
		}
		name := projected.ProjectionName(scope.GetDomain(), scope.GetCollection(), scope.GetTenantId())
		return name, hermes.QueryWithFilters(scope.GetTenantId(), DefaultSnapshotLimit), nil
	}
	// Cold scopes lazy-warm on first read: WarmScope rebuilds from the record
	// mirror (and self-backfills via ScopeBackfill when the mirror is empty),
	// so no scope ever needs eager warming for correctness.
	warmer := func(ctx context.Context, scope *foundationpb.ProjectionScope) error {
		return projected.WarmScope(ctx, scope.GetDomain(), scope.GetCollection(), scope.GetTenantId())
	}
	return NewGateway(projected.Store(), queueSize, append([]Option{WithResolver(resolver), WithScopeWarmer(warmer)}, opts...)...)
}

// NewGateway constructs a Gateway over the given store. queueSize bounds the
// per-subscriber delta buffer (DefaultSubscriberQueue when <= 0).
func NewGateway(store *hermes.Store, queueSize int, opts ...Option) (*Gateway, error) {
	if store == nil {
		return nil, ErrNilStore
	}
	g := &Gateway{
		store:    store,
		hub:      NewHub(queueSize),
		resolve:  DomainResolver(DefaultSnapshotLimit),
		maxLimit: DefaultSnapshotLimit,
	}
	for _, opt := range opts {
		opt(g)
	}
	if err := g.audience.validate(); err != nil {
		return nil, err
	}
	// Subscribe to every accepted apply so live deltas flow regardless of write
	// path: the in-process projected runtime store, the Redis envelope projector,
	// or a direct ApplyBatch all reach subscribers through this one seam.
	g.cancel = store.Observe(g.onApplied)
	return g, nil
}

// Close detaches the gateway's store observer. It is safe to call more than once.
func (g *Gateway) Close() {
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

// Hub exposes the underlying fan-out hub.
func (g *Gateway) Hub() *Hub { return g.hub }

// onApplied fans an accepted apply batch out to subscribers, grouped by exact
// scope and stamped with the post-apply epoch. It runs after the store's
// partition lock is released, so the encode + non-blocking fan-out never
// serializes applies.
func (g *Gateway) onApplied(projection string, mutations []hermes.AppliedMutation) {
	epoch, _ := g.store.Epoch(projection)
	groups, undeliverable := groupAccepted(mutations, g.audience)
	if undeliverable > 0 {
		g.audienceDrops.Add(undeliverable)
	}
	for key, group := range groups {
		withVectors, withoutVectors := g.hub.VectorSubscriberCounts(key)
		if withVectors == 0 && withoutVectors == 0 {
			continue
		}
		watermark := watermarkFromMutations(group.mutations)
		frame, err := encodeFrameVariants(group.mutations, epoch, watermark, "projectiongw", withVectors > 0, withoutVectors > 0)
		if err != nil {
			continue
		}
		g.hub.Broadcast(key, frame)
	}
}

// Snapshot reads the materialized read model for a scope at the current epoch as
// a ProjectionSnapshot. Records are carried as upsert mutations so the snapshot
// and the delta stream share one wire shape; watermark/epoch let the client
// resume the delta stream exactly where the snapshot ended.
//
// It carries no caller identity, so an AudiencePerRecord scope is refused with
// ErrAudienceForbidden. Identity-bearing callers use SnapshotAudience.
func (g *Gateway) Snapshot(ctx context.Context, req *foundationpb.ProjectionSnapshotRequest) (*foundationpb.ProjectionSnapshot, error) {
	return g.SnapshotAudience(ctx, req, nil)
}

// SnapshotAudience reads the scope as the given audiences may see it. On a
// tenant-wide scope it is Snapshot. On an AudiencePerRecord scope it is the
// read-side half of the same partition the delta stream uses:
//
//   - the declared audience fields become equality filters, so the newest-N
//     window is the CALLER'S newest N rather than the organization's — which is
//     also what stops a busy tenant silently pushing a user's own rows out of
//     the window;
//   - one bounded read per (field, audience) pair, unioned by version, so the
//     request stays O(limit) per read and the pair count is capped
//     (MaxAudienceSnapshotReads);
//   - every returned record is then re-checked against the caller's audiences
//     with the same derivation the fan-out uses. The filter is the fast path;
//     this check is the authority, so a missing index, an unexpected field type
//     or a resolver that widens the query cannot become a disclosure.
func (g *Gateway) SnapshotAudience(ctx context.Context, req *foundationpb.ProjectionSnapshotRequest, audiences []string) (*foundationpb.ProjectionSnapshot, error) {
	scope := req.GetScope()
	projection, query, err := g.resolve(scope)
	if err != nil {
		return nil, err
	}
	policy, err := g.audience.resolve(scope.GetDomain(), scope.GetCollection())
	if err != nil {
		return nil, err
	}
	audiences = dedupeAudiences(audiences)
	if policy.Mode == AudiencePerRecord && len(audiences) == 0 {
		return nil, ErrAudienceForbidden
	}
	query.Limit = g.boundedLimit(query.Limit, int(req.GetLimit()))
	readWarming := g.snapshotReader(ctx, projection, scope, req)

	var snapshot hermes.Snapshot
	if policy.Mode == AudiencePerRecord {
		snapshot, err = g.audienceSnapshot(policy, query, audiences, readWarming)
	} else {
		snapshot, err = readWarming(query)
	}
	if err != nil {
		return nil, err
	}
	mutations := filterScope(snapshot.Mutations, scope)
	if policy.Mode == AudiencePerRecord {
		mutations = filterAudience(mutations, policy, audiences)
	}
	return &foundationpb.ProjectionSnapshot{
		Scope:      scope,
		Batch:      &foundationpb.RecordMutationBatch{Mutations: mutations},
		Watermark:  hermes.FormatWatermark(snapshot.Watermark),
		Epoch:      snapshot.Epoch,
		NextCursor: hermes.FormatWatermark(snapshot.NextCursor),
		HasMore:    snapshot.HasMore,
	}, nil
}

// boundedLimit resolves the record limit for one snapshot.
//
// Always bound the snapshot. A positive limit engages hermes's ordered-index
// early-stop (visiting ~limit newest records instead of the whole scope), so an
// unbounded scan can never be triggered by a client or a misconfigured
// resolver. This keeps the read O(limit), not O(scope) — the BoundedWork
// invariant. Benchmarks: a limited 10K-scope snapshot is ~0.58ms vs ~210ms
// unbounded; the incremental (watermark) path over the same scope is ~0.23ms.
func (g *Gateway) boundedLimit(resolved, requested int) int {
	if requested > 0 && (resolved == 0 || requested < resolved) {
		resolved = requested
	}
	if resolved <= 0 || resolved > g.maxLimit {
		return g.maxLimit
	}
	return resolved
}

// snapshotReader builds the bounded read this request performs, closing over
// its resume position and vector mode so the audience union can issue the same
// read once per (field, audience) pair.
//
// It honors the resume watermark (forward catch-up) and the keyset cursor
// (backward backfill): a client that already holds prior state sends its
// watermark for the changed tail; a client backfilling a scope larger than the
// limit pages older records via the cursor. Each read stays O(limit).
func (g *Gateway) snapshotReader(ctx context.Context, projection string, scope *foundationpb.ProjectionScope, req *foundationpb.ProjectionSnapshotRequest) func(hermes.Query) (hermes.Snapshot, error) {
	sinceVersion := hermes.ParseWatermark(req.GetSinceWatermark())
	beforeVersion := hermes.ParseWatermark(req.GetCursor())
	includeVectors := req.GetVectorMode() != foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE
	read := func(scoped hermes.Query) (hermes.Snapshot, error) {
		if includeVectors {
			return g.store.SnapshotPage(ctx, projection, scoped, hermes.Fence{}, sinceVersion, beforeVersion)
		}
		return g.store.SnapshotPageWithoutVectors(ctx, projection, scoped, hermes.Fence{}, sinceVersion, beforeVersion)
	}
	// Lazy warm: resolve the cold scope through the projected store (which
	// registers the partition, rebuilds from the mirror, and self-backfills an
	// empty mirror), then retry once. Warm failure preserves the original
	// not-found so the HTTP mapping stays stable.
	return func(scoped hermes.Query) (hermes.Snapshot, error) {
		snapshot, err := read(scoped)
		if errors.Is(err, hermes.ErrProjectionNotFound) && g.warmScope != nil {
			if warmErr := g.warmScope(ctx, scope); warmErr == nil {
				snapshot, err = read(scoped)
			}
		}
		return snapshot, err
	}
}

// audienceSnapshot reads a per-record scope as the union of one bounded read
// per (audience field, audience id) pair.
//
// hermes conjoins query filters, so "customer_profile_id = me OR
// merchant_profile_id = me" cannot be expressed as one query. It does not need
// to be: each pair is an indexed, limit-bounded read, and the union of a capped
// number of O(limit) reads is still O(limit). That is what makes this landable
// without an array-contains filter kind in hermes — which remains the right
// optimization later for a record with many audiences, not a prerequisite now.
func (g *Gateway) audienceSnapshot(policy AudiencePolicy, query hermes.Query, audiences []string, read func(hermes.Query) (hermes.Snapshot, error)) (hermes.Snapshot, error) {
	filters, err := snapshotAudienceFilters(policy, audiences)
	if err != nil {
		return hermes.Snapshot{}, err
	}
	pages := make([]hermes.Snapshot, 0, len(filters))
	for _, filter := range filters {
		scoped := query.RecordQuery()
		scoped.Filters = append(scoped.Filters, database.RecordFilter{
			Field: filter.field,
			Value: database.StringValue(filter.audience),
		})
		page, err := read(hermes.QueryFromRecordQuery(query.OrganizationID, scoped))
		if err != nil {
			return hermes.Snapshot{}, err
		}
		pages = append(pages, page)
	}
	return mergeSnapshotPages(pages, query.Limit), nil
}

// mergeSnapshotPages unions per-audience pages into one version-descending page
// bounded by limit. Each page is already version-descending, so the merge only
// has to deduplicate records that matched on more than one audience field and
// re-apply the limit.
//
// Epoch and watermark are the MINIMUM across pages, and the cursor the oldest
// version actually emitted: a resume token must not claim more progress than
// the least-advanced read it was derived from, or the delta stream would skip
// what that read had not yet seen.
func mergeSnapshotPages(pages []hermes.Snapshot, limit int) hermes.Snapshot {
	merged := hermes.Snapshot{}
	for index, page := range pages {
		if index == 0 || page.Epoch < merged.Epoch {
			merged.Epoch = page.Epoch
		}
		if index == 0 || page.Watermark < merged.Watermark {
			merged.Watermark = page.Watermark
		}
		merged.HasMore = merged.HasMore || page.HasMore
	}
	mutations := newestPerRecord(pages)
	sort.SliceStable(mutations, func(i, j int) bool {
		return mutations[i].GetVersion() > mutations[j].GetVersion()
	})
	if limit > 0 && len(mutations) > limit {
		mutations = mutations[:limit]
		merged.HasMore = true
	}
	merged.Mutations = mutations
	if merged.HasMore && len(mutations) > 0 {
		merged.NextCursor = mutations[len(mutations)-1].GetVersion()
	}
	return merged
}

// newestPerRecord collapses the pages to one mutation per record id, keeping
// the highest version. A record matched by more than one audience field appears
// in more than one page, and the pages are read independently, so the newest
// version is the only one that reflects the record's current state.
func newestPerRecord(pages []hermes.Snapshot) []*foundationpb.RecordMutation {
	byRecord := make(map[string]*foundationpb.RecordMutation)
	order := make([]string, 0)
	for _, page := range pages {
		for _, mutation := range page.Mutations {
			id := mutation.GetRecordId()
			existing, seen := byRecord[id]
			if !seen {
				byRecord[id] = mutation
				order = append(order, id)
				continue
			}
			if mutation.GetVersion() > existing.GetVersion() {
				byRecord[id] = mutation
			}
		}
	}
	mutations := make([]*foundationpb.RecordMutation, 0, len(order))
	for _, id := range order {
		mutations = append(mutations, byRecord[id])
	}
	return mutations
}

// filterAudience drops any record the caller is not an audience of. It is the
// snapshot's counterpart to the fan-out partition: same policy, same
// derivation, applied to the answer rather than to the query, so the two halves
// of the read path cannot disagree about who may see a record.
func filterAudience(mutations []*foundationpb.RecordMutation, policy AudiencePolicy, audiences []string) []*foundationpb.RecordMutation {
	out := make([]*foundationpb.RecordMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if !intersects(policy.audiences(mutation), audiences) {
			continue
		}
		out = append(out, mutation)
	}
	return out
}

// Subscribe opens a live delta feed for a scope. The caller should typically
// take a snapshot first and present that snapshot's watermark to reconcile any
// gap; the Hub itself does not replay history.
func (g *Gateway) Subscribe(scope *foundationpb.ProjectionScope) (*Subscription, error) {
	return g.SubscribeWithVectorMode(scope, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_UNSPECIFIED)
}

// SubscribeWithVectorMode opens a scoped live feed. Unspecified and INCLUDE
// preserve v1 frames; EXCLUDE removes dense vectors before socket delivery.
//
// It carries no caller identity, so on an AudiencePerRecord scope it fails with
// ErrAudienceForbidden rather than subscribing tenant-wide. Identity-bearing
// callers use SubscribeAudience.
func (g *Gateway) SubscribeWithVectorMode(scope *foundationpb.ProjectionScope, mode foundationpb.ProjectionVectorMode) (*Subscription, error) {
	return g.SubscribeAudience(scope, nil, mode)
}

// SubscribeAudience opens a scoped live feed for a caller belonging to the
// given audiences, which must come from verified identity (see
// SubjectAudienceFunc) — never from the client.
//
// On a tenant-wide scope the audiences are irrelevant and the subscription is
// exactly what it was before audiences existed. On an AudiencePerRecord scope
// the subscriber is registered under one hub key per audience and an empty
// audience set is refused: there is no path from "who is this?" being
// unanswerable to receiving the tenant's stream.
func (g *Gateway) SubscribeAudience(scope *foundationpb.ProjectionScope, audiences []string, mode foundationpb.ProjectionVectorMode) (*Subscription, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	policy, err := g.audience.resolve(scope.GetDomain(), scope.GetCollection())
	if err != nil {
		return nil, err
	}
	vectors := mode != foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE
	if policy.Mode != AudiencePerRecord {
		return g.hub.SubscribeWithVectors(scope, vectors), nil
	}
	audiences = dedupeAudiences(audiences)
	if len(audiences) == 0 {
		return nil, ErrAudienceForbidden
	}
	return g.hub.SubscribeKeys(audienceKeys(scope, audiences), vectors), nil
}

// ApplyEnvelopes applies projection envelopes to the store. Broadcasting happens
// through the store apply observer (onApplied), so this is a thin pass-through
// retained for callers (e.g. an envelope projector) that want a single call that
// both applies and — via the observer — fans accepted deltas out. Only mutations
// hermes accepts are broadcast, each stamped with the version hermes assigned, so
// the stream is the source of truth.
func (g *Gateway) ApplyEnvelopes(ctx context.Context, projection string, envelopes []events.Envelope) (hermes.ApplyResult, error) {
	return g.store.ApplyEnvelopes(ctx, projection, envelopes)
}

type scopeGroup struct {
	scope     *foundationpb.ProjectionScope
	mutations []*foundationpb.RecordMutation
}

// groupAccepted converts each accepted mutation into a RecordMutation stamped
// with its assigned version and groups them into the fan-out topics that carry
// them: the plain scope key for a tenant-wide scope, one key per audience for
// an AudiencePerRecord scope. A record naming two audiences lands in two
// groups, so encode cost scales with the DISTINCT AUDIENCES IN THIS BATCH and
// never with the number of subscribers.
//
// The second return is the count of mutations that reached no group at all: an
// undeclared scope under Strict, or — the common case — a per-record scope
// whose tombstone carried no audience fields. Both are dropped rather than
// broadcast, which is the fail-closed half of the contract.
func groupAccepted(accepted []hermes.AppliedMutation, audience AudienceConfig) (map[string]*scopeGroup, uint64) {
	groups := make(map[string]*scopeGroup)
	var undeliverable uint64
	// Most records name one or two audiences; deriving them into a stack array
	// keeps the apply path from allocating once per accepted mutation.
	var audienceBuffer [4]string
	add := func(key string, scope *foundationpb.ProjectionScope, mutation *foundationpb.RecordMutation) {
		group := groups[key]
		if group == nil {
			group = &scopeGroup{scope: scope}
			groups[key] = group
		}
		group.mutations = append(group.mutations, mutation)
	}
	for _, applied := range accepted {
		mutation := hermes.MutationFromRecord(applied.Record, applied.Operation, applied.Version)
		scope := &foundationpb.ProjectionScope{
			TenantId:   mutation.GetOrganizationId(),
			Domain:     mutation.GetDomain(),
			Collection: mutation.GetCollection(),
		}
		policy, err := audience.resolve(scope.GetDomain(), scope.GetCollection())
		if err != nil {
			undeliverable++
			continue
		}
		if policy.Mode != AudiencePerRecord {
			add(ScopeKey(scope), scope, mutation)
			continue
		}
		ids := policy.appendAudiences(audienceBuffer[:0], mutation)
		if len(ids) == 0 {
			undeliverable++
			continue
		}
		for _, key := range audienceKeys(scope, ids) {
			add(key, scope, mutation)
		}
	}
	return groups, undeliverable
}

// AudienceDrops reports how many accepted mutations were withheld from the
// live stream because no audience could be derived for them. A non-zero and
// growing count on a per-record scope means its deletes do not carry the
// audience fields: those deletions converge only on the client's next
// snapshot. It is exported so a deployment can alarm on the gap instead of
// discovering it as "the row never disappeared".
func (g *Gateway) AudienceDrops() uint64 { return g.audienceDrops.Load() }

func watermarkFromMutations(mutations []*foundationpb.RecordMutation) string {
	var max uint64
	for _, mutation := range mutations {
		if v := mutation.GetVersion(); v > max {
			max = v
		}
	}
	return hermes.FormatWatermark(max)
}

// encodeFrame builds a binary events.Envelope carrying a RecordMutationBatch and
// wraps it as a fan-out Frame. The envelope reuses the canonical hermes
// projection envelope shape (protobuf payload, terminal event type).
func encodeFrame(mutations []*foundationpb.RecordMutation, epoch uint64, watermark, correlationID string) (Frame, error) {
	return encodeFrameVariants(mutations, epoch, watermark, correlationID, true, true)
}

// encodeFrameVariants builds only the variants an active subscriber needs.
// Dense vectors are the expensive payload, so record-only subscribers never
// cause the vector-bearing protobuf frame to be allocated or serialized.
func encodeFrameVariants(mutations []*foundationpb.RecordMutation, epoch uint64, watermark, correlationID string, includeVectors, excludeVectors bool) (Frame, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = "projectiongw"
	}
	frame := Frame{Watermark: watermark, Epoch: epoch}
	if includeVectors {
		envelope, err := hermes.NewProjectionEnvelope(mutations, correlationID)
		if err != nil {
			return Frame{}, err
		}
		frame.Envelope, err = envelope.ToBinary()
		if err != nil {
			return Frame{}, err
		}
	}
	if excludeVectors {
		envelope, err := hermes.NewProjectionEnvelope(withoutVectors(mutations), correlationID)
		if err != nil {
			return Frame{}, err
		}
		frame.EnvelopeWithoutVectors, err = envelope.ToBinary()
		if err != nil {
			return Frame{}, err
		}
		if !includeVectors {
			frame.Envelope = frame.EnvelopeWithoutVectors
		}
	}
	return frame, nil
}

// withoutVectors copies only mutations that carry dense vectors. Unchanged
// entries retain their immutable protobuf object and avoid needless allocation.
func withoutVectors(mutations []*foundationpb.RecordMutation) []*foundationpb.RecordMutation {
	var filtered []*foundationpb.RecordMutation
	for index, mutation := range mutations {
		if len(mutation.GetVector()) == 0 {
			continue
		}
		if filtered == nil {
			filtered = append([]*foundationpb.RecordMutation(nil), mutations...)
		}
		filtered[index] = proto.Clone(mutation).(*foundationpb.RecordMutation)
		filtered[index].Vector = nil
	}
	if filtered == nil {
		return mutations
	}
	return filtered
}

// filterScope keeps only mutations whose domain/collection/tenant match the
// requested scope. The resolved partition may hold multiple collections; the
// gateway is the place collection scoping is enforced.
func filterScope(mutations []*foundationpb.RecordMutation, scope *foundationpb.ProjectionScope) []*foundationpb.RecordMutation {
	domain := scope.GetDomain()
	collection := scope.GetCollection()
	tenant := scope.GetTenantId()
	out := make([]*foundationpb.RecordMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.GetDomain() != domain || mutation.GetCollection() != collection {
			continue
		}
		if org := mutation.GetOrganizationId(); org != "" && org != tenant {
			continue
		}
		out = append(out, mutation)
	}
	return out
}

// ScopeKey is the exact colon-prefix topic for a scope: tenant:domain:collection.
func ScopeKey(scope *foundationpb.ProjectionScope) string {
	if scope == nil {
		return "::"
	}
	return scope.GetTenantId() + ":" + scope.GetDomain() + ":" + scope.GetCollection()
}

func validateScope(scope *foundationpb.ProjectionScope) error {
	if scope == nil ||
		strings.TrimSpace(scope.GetTenantId()) == "" ||
		strings.TrimSpace(scope.GetDomain()) == "" ||
		strings.TrimSpace(scope.GetCollection()) == "" {
		return ErrScopeInvalid
	}
	// ':' is the scope key's own separator, and the domain/collection halves
	// are client-supplied path segments. Without this, a crafted collection
	// could spell out another subscriber's audience key (or another
	// collection's topic) and land in its bucket. The projected store already
	// names partitions "prefix:domain:collection:organization", so a colon in
	// any component was never addressable to begin with.
	if strings.ContainsRune(scope.GetTenantId(), ':') ||
		strings.ContainsRune(scope.GetDomain(), ':') ||
		strings.ContainsRune(scope.GetCollection(), ':') {
		return ErrScopeInvalid
	}
	return nil
}
