package hermes

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// TestMirrorSweeperDeleteRecordSourceCarriesAudienceFields pins the addressed
// delete lane end to end through the sweeper: a DeletedRecordsSince source
// streams the deleted row's audience-bearing fields, the sweep projects the
// delete carrying them, and the observer — which is what the projection gateway
// subscribes to — sees a delete it can address. An identity-only DeleteSince
// source is unchanged and yields a delete with no fields, which is exactly the
// difference the two registration methods exist to express.
func TestMirrorSweeperDeleteRecordSourceCarriesAudienceFields(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	seed := func(recordID string) database.DomainRecord {
		return database.DomainRecord{
			Domain: "marketplace", Collection: "orders", OrganizationID: "org_1", RecordID: recordID,
			Data: testRecordData(map[string]any{"customer_id": "cust_alice"}),
		}
	}
	for _, id := range []string{"order_addressed", "order_anonymous"} {
		if _, upsertErr := store.UpsertRecord(ctx, seed(id)); upsertErr != nil {
			t.Fatalf("UpsertRecord(%s) error = %v", id, upsertErr)
		}
	}

	var deletes []AppliedMutation
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		for _, mutation := range mutations {
			if mutation.Operation == OperationDelete {
				deletes = append(deletes, mutation)
			}
		}
	})
	defer cancel()

	deletedAt := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	addressed, anonymous := tombstoneSources(deletedAt)

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 4})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddDeleteRecordSource("orders_tombstones", addressed); err != nil {
		t.Fatalf("AddDeleteRecordSource() error = %v", err)
	}
	if err := sweeper.AddDeleteSource("orders_legacy_tombstones", anonymous); err != nil {
		t.Fatalf("AddDeleteSource() error = %v", err)
	}

	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 2 {
		t.Fatalf("SweepOnce() = %d err=%v, want 2", n, err)
	}
	// The cursor advances for both source kinds, so a swept deletion is not
	// re-applied on the next pass.
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 0 {
		t.Fatalf("idle SweepOnce() = %d err=%v, want 0", n, err)
	}

	assertDeleteAudience(t, deletes)

	// Both rows are gone from the durable mirror regardless of addressing.
	for _, id := range []string{"order_addressed", "order_anonymous"} {
		if _, found, getErr := base.GetRecord(ctx, "marketplace", "orders", "org_1", id); getErr != nil || found {
			t.Fatalf("mirror still holds %s: found=%v err=%v", id, found, getErr)
		}
	}
}

// assertDeleteAudience requires that the record-bearing source produced a
// tombstone carrying the audience field, and the identity-only source one
// carrying nothing — the whole difference between the two registrations.
func assertDeleteAudience(t *testing.T, deletes []AppliedMutation) {
	t.Helper()
	byRecord := map[string]database.RecordData{}
	for _, mutation := range deletes {
		byRecord[mutation.Record.RecordID] = mutation.Record.Data
	}
	if len(byRecord) != 2 {
		t.Fatalf("observed deletes for %v, want both records", byRecord)
	}
	addressed, ok := byRecord["order_addressed"]
	if !ok {
		t.Fatal("no delete observed for order_addressed")
	}
	value, found := addressed.Get("customer_id")
	if !found || !value.Equal(database.StringValue("cust_alice")) {
		t.Fatalf("addressed delete carried %v, want customer_id=cust_alice", addressed)
	}
	if anonymous := byRecord["order_anonymous"]; len(anonymous) != 0 {
		t.Fatalf("identity-only delete carried %v, want no fields", anonymous)
	}
}

// tombstoneSources returns one delete source of each kind over the same
// deletion instant: the record-bearing one carries the audience field, the
// identity-only one cannot.
func tombstoneSources(deletedAt time.Time) (DeletedRecordsSince, DeletedSince) {
	addressed := func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		if !deletedAt.After(cursor) {
			return nil
		}
		// The tombstone carries only the audience field, not a copy of the row.
		return visit(database.DomainRecord{
			Domain: "marketplace", Collection: "orders", OrganizationID: "org_1", RecordID: "order_addressed",
			Data: testRecordData(map[string]any{"customer_id": "cust_alice"}),
		}, deletedAt)
	}
	anonymous := func(_ context.Context, cursor time.Time, visit func(string, string, string, string, time.Time) error) error {
		if !deletedAt.After(cursor) {
			return nil
		}
		return visit("marketplace", "orders", "org_1", "order_anonymous", deletedAt)
	}
	return addressed, anonymous
}
