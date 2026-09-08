//go:build servicebacked

package servicebacked

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// TestServiceBackedDeleteRecordsBatchParity proves the single-statement unnest
// delete refines sequential DeleteRecord per record on live Postgres: the same
// rows are removed, an absent row is not an error, a duplicate identity inside
// one batch is removed once, and rows outside the batch are untouched.
//
// The last property is the one worth a live database. The batch matches on a
// four-column identity through a USING join, and a join predicate that dropped
// a column would delete another organization's rows while every in-memory test
// still passed.
func TestServiceBackedDeleteRecordsBatchParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	db := requirePostgresDB(t, state)

	orgID := uniqueName(env.prefix, "delete-batch-org")
	otherOrgID := uniqueName(env.prefix, "delete-batch-other-org")
	cleanupOrganization(t, ctx, state, orgID)
	cleanupOrganization(t, ctx, state, otherOrgID)

	mk := func(org, id string) database.DomainRecord {
		return database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: org, RecordID: id,
			Data: serviceRecordData(map[string]any{"name": "Delete Parity Dish"}),
		}
	}
	seed := func(records ...database.DomainRecord) {
		t.Helper()
		for _, rec := range records {
			if _, err := state.UpsertRecord(ctx, rec); err != nil {
				t.Fatalf("seed UpsertRecord(%s/%s) error = %v", rec.OrganizationID, rec.RecordID, err)
			}
		}
	}
	present := func(org, id string) bool {
		t.Helper()
		_, found, err := state.GetRecord(ctx, "menu", "dishes", org, id)
		if err != nil {
			t.Fatalf("GetRecord(%s/%s) error = %v", org, id, err)
		}
		return found
	}

	// Lane A: sequential singles. Lane B: one unnest batch over the same shapes.
	seed(mk(orgID, "seq_1"), mk(orgID, "seq_2"), mk(orgID, "bat_1"), mk(orgID, "bat_2"))
	// A neighbour that must survive: same domain, collection and record id,
	// different organization.
	seed(mk(otherOrgID, "bat_1"))

	for _, id := range []string{"seq_1", "seq_2"} {
		if err := state.DeleteRecord(ctx, "menu", "dishes", orgID, id); err != nil {
			t.Fatalf("sequential DeleteRecord(%s) error = %v", id, err)
		}
	}
	if err := db.DeleteRecordsBatch(ctx, []database.DomainRecord{mk(orgID, "bat_1"), mk(orgID, "bat_2")}); err != nil {
		t.Fatalf("DeleteRecordsBatch() error = %v", err)
	}

	for _, id := range []string{"seq_1", "seq_2", "bat_1", "bat_2"} {
		if present(orgID, id) {
			t.Fatalf("%s survived its delete", id)
		}
	}
	if !present(otherOrgID, "bat_1") {
		t.Fatal("the batch deleted another organization's row: the identity join is not tenant-scoped")
	}

	// Deleting absent rows is not an error, so a replay after a partial pass is
	// safe. The sequential lane behaves the same way.
	if err := db.DeleteRecordsBatch(ctx, []database.DomainRecord{mk(orgID, "bat_1"), mk(orgID, "never_existed")}); err != nil {
		t.Fatalf("replay over absent rows error = %v", err)
	}

	// A duplicate identity in one batch is removed once and reports no error.
	seed(mk(orgID, "bat_dup"))
	if err := db.DeleteRecordsBatch(ctx, []database.DomainRecord{mk(orgID, "bat_dup"), mk(orgID, "bat_dup")}); err != nil {
		t.Fatalf("duplicate batch error = %v", err)
	}
	if present(orgID, "bat_dup") {
		t.Fatal("bat_dup survived a duplicate batch")
	}

	// An empty batch is a no-op that touches nothing.
	seed(mk(orgID, "bat_keep"))
	if err := db.DeleteRecordsBatch(ctx, nil); err != nil {
		t.Fatalf("empty batch error = %v", err)
	}
	if !present(orgID, "bat_keep") {
		t.Fatal("an empty batch deleted a row")
	}

	cleanupOrganization(t, ctx, state, orgID)
	cleanupOrganization(t, ctx, state, otherOrgID)
}

// TestServiceBackedProjectedDeleteRecordsUsesBatch proves the capability seam
// end to end on live Postgres: hermes detects DeleteRecordsBatch on the base
// store and removes a whole batch in one round trip, while the hot partition
// and the durable mirror stay in agreement.
func TestServiceBackedProjectedDeleteRecordsUsesBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	orgID := uniqueName(env.prefix, "projected-delete-org")
	cleanupOrganization(t, ctx, state, orgID)

	projected, err := hermes.WrapRuntimeStore(state, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 64,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}

	records := make([]database.DomainRecord, 0, 4)
	for _, id := range []string{"dish_1", "dish_2", "dish_3", "dish_4"} {
		records = append(records, database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: orgID, RecordID: id,
			Data: serviceRecordData(map[string]any{"name": "Projected Dish"}),
		})
	}
	if _, err := projected.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}

	applies := 0
	cancelObserve := projected.Store().Observe(func(string, []hermes.AppliedMutation) { applies++ })
	defer cancelObserve()

	if err := projected.DeleteRecords(ctx, records); err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}

	// One scope, so one hot apply and one fan-out frame for the whole batch.
	if applies != 1 {
		t.Fatalf("hot applies = %d, want 1 for a single-scope batch", applies)
	}
	for _, rec := range records {
		if _, found, err := state.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || found {
			t.Fatalf("mirror still holds %s: found=%v err=%v", rec.RecordID, found, err)
		}
	}
	name := projected.ProjectionName("menu", "dishes", orgID)
	count, err := projected.Store().Count(ctx, name, hermes.Query{OrganizationID: orgID}, hermes.Fence{})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("hot partition holds %d records after the batch delete, want 0", count)
	}

	cleanupOrganization(t, ctx, state, orgID)
}
