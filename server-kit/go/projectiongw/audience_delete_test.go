package projectiongw

import (
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// newProjectedAudienceGateway wires the real write path — a projected runtime
// store over a base DB — to a gateway with a per-record audience policy. The
// delete lane is only meaningful end to end: hermes decides what the observer
// sees, and the gateway decides who it reaches.
func newProjectedAudienceGateway(t *testing.T) (*hermes.ProjectedRuntimeStore, *Gateway) {
	t.Helper()
	projected, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		IndexedFields:      []string{"customer_id", "merchant_id"},
		MaxRecordsPerScope: 64,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	gw, err := NewGatewayForProjectedStore(projected, 0, WithAudience(AudienceConfig{Policies: orderAudiencePolicies()}))
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() err=%v", err)
	}
	t.Cleanup(gw.Close)
	return projected, gw
}

func orderRecord(recordID, customer string) database.DomainRecord {
	return database.DomainRecord{
		Domain:         audienceDomain,
		Collection:     audienceCollection,
		OrganizationID: "org_default",
		RecordID:       recordID,
		Data:           database.RecordData{{Name: "customer_id", Value: database.StringValue(customer)}},
	}
}

// collectOps drains a subscription into (recordID, operation) pairs.
func collectOps(t *testing.T, sub *Subscription) map[string]foundationpb.ProjectionOperation {
	t.Helper()
	ops := map[string]foundationpb.ProjectionOperation{}
	for _, mutation := range drainMutations(t, sub) {
		ops[mutation.GetRecordId()] = mutation.GetOperation()
	}
	return ops
}

// DeleteRecordWithFields addresses the tombstone: it reaches exactly the
// audience the record itself reached, and nobody else. Without the fields the
// gateway has nothing to derive an audience from, so the deletion would be
// withheld and converge only on the reader's next snapshot.
func TestDeleteRecordWithFieldsReachesTheRecordsAudienceOnly(t *testing.T) {
	projected, gw := newProjectedAudienceGateway(t)
	ctx := t.Context()

	for _, seed := range []database.DomainRecord{
		orderRecord("order_alice", "cust_alice"),
		orderRecord("order_bob", "cust_bob"),
	} {
		if _, err := projected.UpsertRecord(ctx, seed); err != nil {
			t.Fatalf("UpsertRecord(%s) err=%v", seed.RecordID, err)
		}
	}

	// Subscribe after the upserts so the only frames in flight are the deletes.
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

	// Delete one record of each audience in the same scope: the partition has
	// to route both, not merely withhold one.
	for _, tombstone := range []database.DomainRecord{
		orderRecord("order_alice", "cust_alice"),
		orderRecord("order_bob", "cust_bob"),
	} {
		if err := projected.DeleteRecordWithFields(ctx, tombstone); err != nil {
			t.Fatalf("DeleteRecordWithFields(%s) err=%v", tombstone.RecordID, err)
		}
	}

	assertOnlyDelete(t, "alice", collectOps(t, alice), "order_alice")
	assertOnlyDelete(t, "bob", collectOps(t, bob), "order_bob")
	if drops := gw.AudienceDrops(); drops != 0 {
		t.Fatalf("AudienceDrops() = %d, want 0 for addressed deletes", drops)
	}
}

// assertOnlyDelete requires that a subscriber saw a DELETE for exactly the one
// record it is an audience of, and nothing else.
func assertOnlyDelete(t *testing.T, who string, ops map[string]foundationpb.ProjectionOperation, recordID string) {
	t.Helper()
	if len(ops) != 1 {
		t.Fatalf("%s received %v, want only %s", who, ops, recordID)
	}
	if got := ops[recordID]; got != foundationpb.ProjectionOperation_PROJECTION_OPERATION_DELETE {
		t.Fatalf("%s received %v for %s, want DELETE", got, recordID, who)
	}
}

// The identity-only DeleteRecord is unchanged, and on a per-record scope that
// means the deletion names nobody: withheld and counted rather than broadcast
// to the whole tenant. This is the behavior DeleteRecordWithFields exists to
// give projects a way out of, so both halves are pinned together.
func TestDeleteRecordWithoutFieldsIsWithheldAndCounted(t *testing.T) {
	projected, gw := newProjectedAudienceGateway(t)
	ctx := t.Context()

	if _, err := projected.UpsertRecord(ctx, orderRecord("order_alice", "cust_alice")); err != nil {
		t.Fatalf("UpsertRecord() err=%v", err)
	}
	alice, err := gw.SubscribeAudience(audienceScope(), []string{"cust_alice"}, foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE)
	if err != nil {
		t.Fatalf("SubscribeAudience() err=%v", err)
	}
	defer alice.Cancel()

	if err := projected.DeleteRecord(ctx, audienceDomain, audienceCollection, "org_default", "order_alice"); err != nil {
		t.Fatalf("DeleteRecord() err=%v", err)
	}

	if ops := collectOps(t, alice); len(ops) != 0 {
		t.Fatalf("subscriber received %v, want nothing from an unaddressed delete", ops)
	}
	if drops := gw.AudienceDrops(); drops != 1 {
		t.Fatalf("AudienceDrops() = %d, want 1", drops)
	}
}

// On a tenant-wide scope the identity-only delete keeps working exactly as it
// did: DeleteRecord's delegation to DeleteRecordWithFields with empty data must
// not have changed the pre-audience behavior.
func TestDeleteRecordUnchangedOnTenantScope(t *testing.T) {
	projected, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 16,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	gw, err := NewGatewayForProjectedStore(projected, 0)
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() err=%v", err)
	}
	defer gw.Close()
	ctx := t.Context()

	if _, upsertErr := projected.UpsertRecord(ctx, orderRecord("order_alice", "cust_alice")); upsertErr != nil {
		t.Fatalf("UpsertRecord() err=%v", upsertErr)
	}
	sub, err := gw.Subscribe(audienceScope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	if deleteErr := projected.DeleteRecord(ctx, audienceDomain, audienceCollection, "org_default", "order_alice"); deleteErr != nil {
		t.Fatalf("DeleteRecord() err=%v", deleteErr)
	}
	assertOnlyDelete(t, "tenant subscriber", collectOps(t, sub), "order_alice")
	if drops := gw.AudienceDrops(); drops != 0 {
		t.Fatalf("AudienceDrops() = %d, want 0 on a tenant scope", drops)
	}
}

// Batching must not blur the audience partition: one DeleteRecords call
// covering both audiences still reaches each subscriber with only its own
// record. The batch is one apply and one encode per scope, but the fan-out
// groups it by audience exactly as it does any other batch.
func TestDeleteRecordsBatchStillPartitionsByAudience(t *testing.T) {
	projected, gw := newProjectedAudienceGateway(t)
	ctx := t.Context()

	records := []database.DomainRecord{
		orderRecord("order_alice", "cust_alice"),
		orderRecord("order_bob", "cust_bob"),
	}
	if _, err := projected.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() err=%v", err)
	}

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

	if err := projected.DeleteRecords(ctx, records); err != nil {
		t.Fatalf("DeleteRecords() err=%v", err)
	}

	assertOnlyDelete(t, "alice", collectOps(t, alice), "order_alice")
	assertOnlyDelete(t, "bob", collectOps(t, bob), "order_bob")
	if drops := gw.AudienceDrops(); drops != 0 {
		t.Fatalf("AudienceDrops() = %d, want 0 for an addressed batch", drops)
	}
}
