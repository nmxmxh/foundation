package projectiongw

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"google.golang.org/protobuf/proto"
)

// The scope under test is the shape the finding describes: one organization
// holding many mutually-untrusting end users, where a record is addressed to a
// customer and a merchant rather than to the tenant.
const (
	audienceDomain     = "marketplace"
	audienceCollection = "orders"
	audienceScopeName  = audienceDomain + "/" + audienceCollection
)

func orderAudiencePolicies() map[string]AudiencePolicy {
	return map[string]AudiencePolicy{
		audienceScopeName: {
			Mode:   AudiencePerRecord,
			Fields: []string{"customer_id", "merchant_id"},
		},
	}
}

func newAudienceGateway(t *testing.T, config AudienceConfig) *Gateway {
	t.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          audienceDomain,
		Domain:        audienceDomain,
		Collection:    audienceCollection,
		IndexedFields: []string{"customer_id", "merchant_id"},
		MaxRecords:    64,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithAudience(config))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	t.Cleanup(gw.Close)
	return gw
}

func audienceScope() *foundationpb.ProjectionScope {
	return &foundationpb.ProjectionScope{TenantId: "org_default", Domain: audienceDomain, Collection: audienceCollection}
}

func stringValue(value string) *foundationpb.ScalarValue {
	return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: value}}
}

// orderMutation builds an order owned by a customer and a merchant. An empty
// party is omitted, which is how a tombstone that carries no audience field
// looks to the gateway.
func orderMutation(recordID string, version uint64, customer, merchant string) *foundationpb.RecordMutation {
	mutation := &foundationpb.RecordMutation{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		Version:        version,
		Domain:         audienceDomain,
		Collection:     audienceCollection,
		OrganizationId: "org_default",
		RecordId:       recordID,
	}
	if customer != "" {
		mutation.Fields = append(mutation.Fields, &foundationpb.FieldValue{Name: "customer_id", Value: stringValue(customer)})
	}
	if merchant != "" {
		mutation.Fields = append(mutation.Fields, &foundationpb.FieldValue{Name: "merchant_id", Value: stringValue(merchant)})
	}
	return mutation
}

func applyOrders(t *testing.T, gw *Gateway, mutations ...*foundationpb.RecordMutation) {
	t.Helper()
	envelope, err := hermes.NewProjectionEnvelope(mutations, "corr-audience")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), audienceDomain, []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
}

func decodeFrame(t *testing.T, frame Frame) []*foundationpb.RecordMutation {
	t.Helper()
	decoded, err := events.FromBinary(frame.Envelope)
	if err != nil {
		t.Fatalf("frame envelope decode err=%v", err)
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
		t.Fatalf("batch unmarshal err=%v", err)
	}
	return batch.GetMutations()
}

// drainMutations collects every mutation delivered to a subscription within a
// short settle window. The window has to be a wait rather than a single read:
// the assertion is about what did NOT arrive.
func drainMutations(t *testing.T, sub *Subscription) []*foundationpb.RecordMutation {
	t.Helper()
	var mutations []*foundationpb.RecordMutation
	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case frame, ok := <-sub.Frames:
			if !ok {
				return mutations
			}
			mutations = append(mutations, decodeFrame(t, frame)...)
		case <-deadline:
			return mutations
		}
	}
}

// drainRecordIDs is drainMutations reduced to the record ids it delivered.
func drainRecordIDs(t *testing.T, sub *Subscription) []string {
	t.Helper()
	return recordIDs(drainMutations(t, sub))
}

func recordIDs(mutations []*foundationpb.RecordMutation) []string {
	ids := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		ids = append(ids, mutation.GetRecordId())
	}
	return ids
}

func countOf(values []string, want string) int {
	total := 0
	for _, value := range values {
		if value == want {
			total++
		}
	}
	return total
}

// Evidence 1: two subscribers on one scope with disjoint audiences observe
// none of each other's records — on the delta stream and on the snapshot.
func TestAudienceIsolatesSubscribersOnDelta(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})

	alice, err := gw.SubscribeAudience(audienceScope(), []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience(alice) err=%v", err)
	}
	defer alice.Cancel()
	bob, err := gw.SubscribeAudience(audienceScope(), []string{"cust_bob"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience(bob) err=%v", err)
	}
	defer bob.Cancel()

	applyOrders(t,
		gw,
		orderMutation("order_alice", 1, "cust_alice", "merch_1"),
		orderMutation("order_bob", 2, "cust_bob", "merch_1"),
	)

	aliceIDs := drainRecordIDs(t, alice)
	bobIDs := drainRecordIDs(t, bob)
	if len(aliceIDs) != 1 || aliceIDs[0] != "order_alice" {
		t.Fatalf("alice received %v, want only order_alice", aliceIDs)
	}
	if len(bobIDs) != 1 || bobIDs[0] != "order_bob" {
		t.Fatalf("bob received %v, want only order_bob", bobIDs)
	}
}

func TestAudienceIsolatesSubscribersOnSnapshot(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	applyOrders(t,
		gw,
		orderMutation("order_alice", 1, "cust_alice", "merch_1"),
		orderMutation("order_bob", 2, "cust_bob", "merch_1"),
	)

	snapshot, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("SnapshotAudience() err=%v", err)
	}
	ids := recordIDs(snapshot.GetBatch().GetMutations())
	if len(ids) != 1 || ids[0] != "order_alice" {
		t.Fatalf("alice snapshot = %v, want only order_alice", ids)
	}

	// The merchant is an audience of both orders through the second declared
	// field, which is the union the snapshot has to assemble from two reads.
	merchant, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"merch_1"})
	if err != nil {
		t.Fatalf("SnapshotAudience(merchant) err=%v", err)
	}
	if got := len(merchant.GetBatch().GetMutations()); got != 2 {
		t.Fatalf("merchant snapshot count = %d, want 2", got)
	}
}

// Evidence 2: a record naming two audiences reaches each of them, and reaches
// a subscriber that holds only one of them exactly once.
func TestAudienceDeliversMultiAudienceRecordOncePerSubscriber(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})

	customer, err := gw.SubscribeAudience(audienceScope(), []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience(customer) err=%v", err)
	}
	defer customer.Cancel()
	merchant, err := gw.SubscribeAudience(audienceScope(), []string{"merch_1"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience(merchant) err=%v", err)
	}
	defer merchant.Cancel()

	applyOrders(t, gw, orderMutation("order_shared", 1, "cust_alice", "merch_1"))

	if got := drainRecordIDs(t, customer); countOf(got, "order_shared") != 1 {
		t.Fatalf("customer received %v, want order_shared exactly once", got)
	}
	if got := drainRecordIDs(t, merchant); countOf(got, "order_shared") != 1 {
		t.Fatalf("merchant received %v, want order_shared exactly once", got)
	}
}

// A snapshot that matches one record through both declared fields returns it
// once: the union deduplicates by record id rather than by read.
func TestAudienceSnapshotDeduplicatesAcrossFields(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	// A self-dealing party occupies both audience fields of one record.
	applyOrders(t, gw, orderMutation("order_self", 1, "party_1", "party_1"))

	snapshot, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"party_1"})
	if err != nil {
		t.Fatalf("SnapshotAudience() err=%v", err)
	}
	if ids := recordIDs(snapshot.GetBatch().GetMutations()); len(ids) != 1 {
		t.Fatalf("snapshot = %v, want one record", ids)
	}
}

// Evidence 3: an unresolvable audience is refused on both halves of the read
// path — never degraded to the tenant stream.
func TestAudienceRefusesUnresolvableCallerOnBothHalves(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	applyOrders(t, gw, orderMutation("order_alice", 1, "cust_alice", "merch_1"))

	if _, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, nil); !errors.Is(err, ErrAudienceForbidden) {
		t.Fatalf("SnapshotAudience(no audience) err=%v, want ErrAudienceForbidden", err)
	}
	if _, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"   "}); !errors.Is(err, ErrAudienceForbidden) {
		t.Fatalf("SnapshotAudience(blank audience) err=%v, want ErrAudienceForbidden", err)
	}
	// The identity-free legacy entry points must fail rather than fall back.
	if _, err := gw.Snapshot(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}); !errors.Is(err, ErrAudienceForbidden) {
		t.Fatalf("Snapshot() err=%v, want ErrAudienceForbidden", err)
	}
	if _, err := gw.Subscribe(audienceScope()); !errors.Is(err, ErrAudienceForbidden) {
		t.Fatalf("Subscribe() err=%v, want ErrAudienceForbidden", err)
	}
}

// A caller who is a member of nothing in this scope reads an empty snapshot,
// not another audience's rows.
func TestAudienceStrangerReadsNothing(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	applyOrders(t, gw, orderMutation("order_alice", 1, "cust_alice", "merch_1"))

	snapshot, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"cust_stranger"})
	if err != nil {
		t.Fatalf("SnapshotAudience() err=%v", err)
	}
	if got := snapshot.GetBatch().GetMutations(); len(got) != 0 {
		t.Fatalf("stranger snapshot = %v, want empty", recordIDs(got))
	}
}

// Strict mode turns a forgotten scope into a refusal instead of a broadcast.
func TestAudienceStrictRefusesUndeclaredScope(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies(), Strict: true})
	other := &foundationpb.ProjectionScope{TenantId: "org_default", Domain: audienceDomain, Collection: "carts"}
	if _, err := gw.SubscribeAudience(other, []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE); !errors.Is(err, ErrAudienceUndeclared) {
		t.Fatalf("SubscribeAudience(undeclared) err=%v, want ErrAudienceUndeclared", err)
	}
	if _, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: other}, []string{"cust_alice"}); !errors.Is(err, ErrAudienceUndeclared) {
		t.Fatalf("SnapshotAudience(undeclared) err=%v, want ErrAudienceUndeclared", err)
	}
}

// Without Strict, an undeclared scope keeps the behavior every deployment had
// before audiences existed. This is the upgrade-safety property.
func TestAudienceUndeclaredScopeStaysTenantWide(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	sub, err := gw.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_default", Domain: audienceDomain, Collection: "carts"})
	if err != nil {
		t.Fatalf("Subscribe(undeclared) err=%v", err)
	}
	sub.Cancel()
}

// A policy that could partition deltas but not filter a snapshot is refused at
// construction: half-enforced is the failure mode this mechanism exists to
// prevent, so it must not be reachable at runtime.
func TestAudiencePolicyWithoutFieldsIsAConstructionError(t *testing.T) {
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:       audienceDomain,
		Domain:     audienceDomain,
		Collection: audienceCollection,
		MaxRecords: 8,
		MaxBytes:   1 << 16,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	_, err = NewGateway(store, 0, WithAudience(AudienceConfig{Policies: map[string]AudiencePolicy{
		audienceScopeName: {Mode: AudiencePerRecord},
	}}))
	if !errors.Is(err, ErrAudiencePolicyInvalid) {
		t.Fatalf("NewGateway() err=%v, want ErrAudiencePolicyInvalid", err)
	}
}

// A tombstone that carries no audience field reaches nobody and is counted.
// Delivering it tenant-wide would leak membership; delivering it nowhere and
// saying so is the accepted trade.
func TestAudienceUnaddressableMutationIsDroppedAndCounted(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	sub, err := gw.SubscribeAudience(audienceScope(), []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience() err=%v", err)
	}
	defer sub.Cancel()

	applyOrders(t, gw, orderMutation("order_orphan", 1, "", ""))

	if got := drainRecordIDs(t, sub); len(got) != 0 {
		t.Fatalf("subscriber received %v, want nothing", got)
	}
	if got := gw.AudienceDrops(); got != 1 {
		t.Fatalf("AudienceDrops() = %d, want 1", got)
	}
}

// The audience term must not be forgeable through a crafted scope component,
// so ':' is rejected everywhere the scope key is built from.
func TestScopeWithColonIsRejected(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	forged := &foundationpb.ProjectionScope{
		TenantId:   "org_default",
		Domain:     audienceDomain,
		Collection: audienceCollection + ":@cust_alice",
	}
	if _, err := gw.SubscribeAudience(forged, []string{"anything"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE); !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("SubscribeAudience(forged) err=%v, want ErrScopeInvalid", err)
	}
}

// An audience set broad enough to multiply the snapshot's reads past the cap is
// refused rather than allowed to turn identity breadth into scan cost.
func TestAudienceSnapshotFanOutIsBounded(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	wide := make([]string, 0, MaxAudienceSnapshotReads)
	for index := range MaxAudienceSnapshotReads {
		wide = append(wide, "cust_"+strings.Repeat("x", index+1))
	}
	_, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, wide)
	if !errors.Is(err, ErrAudienceTooBroad) {
		t.Fatalf("SnapshotAudience(wide) err=%v, want ErrAudienceTooBroad", err)
	}
}

// End to end over HTTP: the mount resolves identity, and a per-record scope on
// a mount with no resolver is a 403 rather than a tenant-wide read.
func TestAudienceHandlerScopesSnapshotToCaller(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	applyOrders(t,
		gw,
		orderMutation("order_alice", 1, "cust_alice", "merch_1"),
		orderMutation("order_bob", 2, "cust_bob", "merch_1"),
	)

	config := HandlerConfig{
		Tenant: func(*http.Request) (string, error) { return "org_default", nil },
		Audience: func(r *http.Request) ([]string, error) {
			// Stands in for verified claims; a real mount reads request context.
			return []string{r.Header.Get("X-Test-Subject")}, nil
		},
	}
	handler := gw.Handler(config)

	request := httptest.NewRequest(http.MethodGet, "/v1/projections/"+audienceDomain+"/"+audienceCollection, nil)
	request.Header.Set("X-Test-Subject", "cust_alice")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("snapshot unmarshal err=%v", err)
	}
	if ids := recordIDs(snapshot.GetBatch().GetMutations()); len(ids) != 1 || ids[0] != "order_alice" {
		t.Fatalf("alice HTTP snapshot = %v, want only order_alice", ids)
	}

	// Same gateway, a mount that declares no audience resolver: refused.
	anonymous := gw.Handler(HandlerConfig{Tenant: func(*http.Request) (string, error) { return "org_default", nil }})
	recorder = httptest.NewRecorder()
	anonymous.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/projections/"+audienceDomain+"/"+audienceCollection, nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("audience-less mount status = %d, want 403", recorder.Code)
	}
}

// Record narrows beyond Fields: the indexed field selects candidates, the
// resolver decides. Here an order the customer field matches is nonetheless
// withheld, which is only observable if the post-read check — not the query
// filter — is the authority on both halves of the path.
func TestAudienceRecordFuncIsAuthoritativeOverFieldFilter(t *testing.T) {
	policies := map[string]AudiencePolicy{
		audienceScopeName: {
			Mode:   AudiencePerRecord,
			Fields: []string{"customer_id"},
			Record: func(mutation *foundationpb.RecordMutation) []string {
				// A record withdrawn from the customer's view keeps its
				// customer_id — and its audience is empty regardless.
				if stringField(mutation, "withdrawn") == "yes" {
					return nil
				}
				return []string{stringField(mutation, "customer_id")}
			},
		},
	}
	gw := newAudienceGateway(t, AudienceConfig{Policies: policies})

	sub, err := gw.SubscribeAudience(audienceScope(), []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience() err=%v", err)
	}
	defer sub.Cancel()

	withdrawn := orderMutation("order_withdrawn", 1, "cust_alice", "merch_1")
	withdrawn.Fields = append(withdrawn.Fields, &foundationpb.FieldValue{Name: "withdrawn", Value: stringValue("yes")})
	applyOrders(t, gw, withdrawn, orderMutation("order_visible", 2, "cust_alice", "merch_1"))

	if got := drainRecordIDs(t, sub); len(got) != 1 || got[0] != "order_visible" {
		t.Fatalf("delta delivered %v, want only order_visible", got)
	}
	snapshot, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope()}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("SnapshotAudience() err=%v", err)
	}
	if ids := recordIDs(snapshot.GetBatch().GetMutations()); len(ids) != 1 || ids[0] != "order_visible" {
		t.Fatalf("snapshot = %v, want only order_visible", ids)
	}
}

func TestSecuritySubjectAudienceFunc(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/projections/marketplace/orders", nil)
	if _, err := SecuritySubjectAudienceFunc(request); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("SecuritySubjectAudienceFunc(anonymous) err=%v, want ErrUnauthenticated", err)
	}

	authenticated := request.WithContext(security.ContextWithUserID(request.Context(), " user_7 "))
	ids, err := SecuritySubjectAudienceFunc(authenticated)
	if err != nil {
		t.Fatalf("SecuritySubjectAudienceFunc() err=%v", err)
	}
	if len(ids) != 1 || ids[0] != "user_7" {
		t.Fatalf("audiences = %v, want [user_7]", ids)
	}
}

func TestDedupeAudiences(t *testing.T) {
	if got := dedupeAudiences(nil); got != nil {
		t.Fatalf("dedupeAudiences(nil) = %v, want nil", got)
	}
	if got := dedupeAudiences([]string{"", "   "}); got != nil {
		t.Fatalf("dedupeAudiences(blank) = %v, want nil", got)
	}
	got := dedupeAudiences([]string{" a ", "b", "a", ""})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dedupeAudiences = %v, want [a b]", got)
	}
}

// A per-record policy whose fields are all blank is as unenforceable as one
// with no fields at all.
func TestAudiencePolicyWithBlankFieldsIsAConstructionError(t *testing.T) {
	config := AudienceConfig{Policies: map[string]AudiencePolicy{
		audienceScopeName: {Mode: AudiencePerRecord, Fields: []string{"  ", ""}},
	}}
	if err := config.validate(); !errors.Is(err, ErrAudiencePolicyInvalid) {
		t.Fatalf("validate() err=%v, want ErrAudiencePolicyInvalid", err)
	}
}

// Strict mode withholds an undeclared scope's mutations from the fan-out
// entirely, and counts them, rather than letting them broadcast.
func TestAudienceStrictDropsUndeclaredScopeFromFanOut(t *testing.T) {
	applied := []hermes.AppliedMutation{{
		Operation: hermes.OperationUpsert,
		Version:   1,
		Record:    databaseRecordForCollection("cart_1", "carts"),
	}}
	groups, dropped := groupAccepted(applied, AudienceConfig{Policies: orderAudiencePolicies(), Strict: true})
	if len(groups) != 0 || dropped != 1 {
		t.Fatalf("groups=%d dropped=%d, want 0/1", len(groups), dropped)
	}
	// Without Strict the same mutation keeps tenant-wide delivery.
	groups, dropped = groupAccepted(applied, AudienceConfig{Policies: orderAudiencePolicies()})
	if len(groups) != 1 || dropped != 0 {
		t.Fatalf("non-strict groups=%d dropped=%d, want 1/0", len(groups), dropped)
	}
}

// The union takes the newest version of a record that matched on more than one
// audience field, and re-applies the limit across the merged set.
func TestMergeSnapshotPages(t *testing.T) {
	newest := orderMutation("order_1", 9, "cust_alice", "merch_1")
	pages := []hermes.Snapshot{
		{Epoch: 7, Watermark: 40, Mutations: []*foundationpb.RecordMutation{orderMutation("order_1", 2, "cust_alice", "merch_1")}},
		{Epoch: 5, Watermark: 30, Mutations: []*foundationpb.RecordMutation{newest, orderMutation("order_2", 4, "cust_alice", "")}},
	}
	merged := mergeSnapshotPages(pages, 0)
	if merged.Epoch != 5 || merged.Watermark != 30 {
		t.Fatalf("merged epoch/watermark = %d/%d, want 5/30", merged.Epoch, merged.Watermark)
	}
	if len(merged.Mutations) != 2 {
		t.Fatalf("merged = %v, want 2 records", recordIDs(merged.Mutations))
	}
	if merged.Mutations[0].GetRecordId() != "order_1" || merged.Mutations[0].GetVersion() != 9 {
		t.Fatalf("merged head = %s@%d, want order_1@9", merged.Mutations[0].GetRecordId(), merged.Mutations[0].GetVersion())
	}
	if merged.HasMore {
		t.Fatalf("merged.HasMore = true, want false when nothing was truncated")
	}

	// Truncating the union sets the resume cursor to the oldest record kept.
	bounded := mergeSnapshotPages(pages, 1)
	if !bounded.HasMore || bounded.NextCursor != 9 {
		t.Fatalf("bounded HasMore=%v NextCursor=%d, want true/9", bounded.HasMore, bounded.NextCursor)
	}
}

// A policy with no fields cannot reach snapshotAudienceFilters through
// NewGateway, but the helper still refuses to build a read plan from one.
func TestSnapshotAudienceFiltersRejectsEmptyPolicy(t *testing.T) {
	filters, err := snapshotAudienceFilters(AudiencePolicy{Mode: AudiencePerRecord}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("snapshotAudienceFilters() err=%v", err)
	}
	if len(filters) != 0 {
		t.Fatalf("filters = %v, want none", filters)
	}
}

// A resolver that cannot establish identity fails the request on every entry
// point — snapshot, single-scope subscribe, and the multiplexed upgrade — with
// the resolver's own status rather than a wider read.
func TestAudienceResolverErrorRefusesEveryEntryPoint(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	handler := gw.Handler(HandlerConfig{
		Tenant:   func(*http.Request) (string, error) { return "org_default", nil },
		Audience: func(*http.Request) ([]string, error) { return nil, ErrUnauthenticated },
	})
	path := "/v1/projections/" + audienceDomain + "/" + audienceCollection

	for _, testCase := range []struct {
		name    string
		request func() *http.Request
	}{
		{"snapshot", func() *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }},
		{"subscribe", func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Upgrade", "websocket")
			return request
		}},
		{"multiplex", func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/v1/projections/", nil)
			request.Header.Set("Upgrade", "websocket")
			return request
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, testCase.request())
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
		})
	}
}

// The single-scope subscribe upgrade is refused before the socket opens when
// the caller resolves to no audience, so the client gets a status rather than
// a silently empty stream.
func TestAudienceSubscribeUpgradeRefusedWithoutAudience(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	handler := gw.Handler(HandlerConfig{
		Tenant:   func(*http.Request) (string, error) { return "org_default", nil },
		Audience: func(*http.Request) ([]string, error) { return nil, nil },
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/projections/"+audienceDomain+"/"+audienceCollection, nil)
	request.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

// The per-audience window is the CALLER'S newest N, not the organization's.
// This is the correctness half of the finding: with a tenant-wide window, a
// user's own rows fall out of it once the organization gets busy, silently.
func TestAudienceSnapshotWindowIsPerAudienceAndPaged(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	// Alice's two orders are the OLDEST rows in the scope; twenty of Bob's
	// newer ones would fill any tenant-wide window ahead of them.
	mutations := make([]*foundationpb.RecordMutation, 0, 22)
	mutations = append(mutations,
		orderMutation("order_alice_1", 1, "cust_alice", "merch_1"),
		orderMutation("order_alice_2", 2, "cust_alice", "merch_1"),
	)
	for index := range 20 {
		mutations = append(mutations, orderMutation(
			"order_bob_"+strconv.Itoa(index), uint64(index+3), "cust_bob", "merch_1"))
	}
	applyOrders(t, gw, mutations...)

	snapshot, err := gw.SnapshotAudience(t.Context(),
		&foundationpb.ProjectionSnapshotRequest{Scope: audienceScope(), Limit: 2}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("SnapshotAudience() err=%v", err)
	}
	ids := recordIDs(snapshot.GetBatch().GetMutations())
	if len(ids) != 2 || !containsString(ids, "order_alice_1") || !containsString(ids, "order_alice_2") {
		t.Fatalf("alice window = %v, want both of her orders despite 20 newer rows", ids)
	}

	// The limit bounds each read of the union, and the cursor pages the rest.
	first, err := gw.SnapshotAudience(t.Context(),
		&foundationpb.ProjectionSnapshotRequest{Scope: audienceScope(), Limit: 1}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("SnapshotAudience(limit=1) err=%v", err)
	}
	if got := recordIDs(first.GetBatch().GetMutations()); len(got) != 1 || got[0] != "order_alice_2" {
		t.Fatalf("first page = %v, want [order_alice_2]", got)
	}
	if !first.GetHasMore() || first.GetNextCursor() == "" {
		t.Fatalf("first page HasMore=%v cursor=%q, want a resumable page", first.GetHasMore(), first.GetNextCursor())
	}
	next, err := gw.SnapshotAudience(t.Context(), &foundationpb.ProjectionSnapshotRequest{
		Scope: audienceScope(), Limit: 1, Cursor: first.GetNextCursor(),
	}, []string{"cust_alice"})
	if err != nil {
		t.Fatalf("SnapshotAudience(page 2) err=%v", err)
	}
	if got := recordIDs(next.GetBatch().GetMutations()); len(got) != 1 || got[0] != "order_alice_1" {
		t.Fatalf("second page = %v, want [order_alice_1]", got)
	}
}
