package hermes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func deleteTestRecord(collection, recordID, customer string) database.DomainRecord {
	return database.DomainRecord{
		Domain: "marketplace", Collection: collection, OrganizationID: "org_1", RecordID: recordID,
		Data: testRecordData(map[string]any{"customer_id": customer}),
	}
}

func newDeleteTestStore(t *testing.T) (*database.MemoryDB, *ProjectedRuntimeStore) {
	t.Helper()
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 64, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	return base, store
}

// DeleteRecords is DeleteRecordWithFields for a batch: every row leaves the
// mirror and the hot partition, each delete still carries its audience fields,
// and the whole scope converges in ONE observer callback rather than one per
// row — which is the cost the batch lane exists to collapse.
func TestDeleteRecordsBatchesOneApplyPerScope(t *testing.T) {
	base, store := newDeleteTestStore(t)
	ctx := t.Context()

	records := []database.DomainRecord{
		deleteTestRecord("orders", "order_1", "cust_alice"),
		deleteTestRecord("orders", "order_2", "cust_bob"),
		deleteTestRecord("orders", "order_3", "cust_alice"),
	}
	if _, err := store.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}

	var callbacks int
	var deletes []AppliedMutation
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		callbacks++
		for _, mutation := range mutations {
			if mutation.Operation == OperationDelete {
				deletes = append(deletes, mutation)
			}
		}
	})
	defer cancel()

	if err := store.DeleteRecords(ctx, records); err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}

	if callbacks != 1 {
		t.Fatalf("observer callbacks = %d, want 1 for a single-scope batch", callbacks)
	}
	if len(deletes) != len(records) {
		t.Fatalf("observed %d deletes, want %d", len(deletes), len(records))
	}
	// Versions are a contiguous run from the shared counter, so LWW ordering
	// against a concurrent upsert is unchanged by batching.
	for index := 1; index < len(deletes); index++ {
		if deletes[index].Version != deletes[index-1].Version+1 {
			t.Fatalf("versions %v are not contiguous", deletes)
		}
	}
	// The audience fields survive the batch, or a per-record scope could not
	// address the tombstones.
	for _, mutation := range deletes {
		value, found := mutation.Record.Data.Get("customer_id")
		if !found || value.Equal(database.RecordValue{}) {
			t.Fatalf("delete for %s carried %v, want its customer_id", mutation.Record.RecordID, mutation.Record.Data)
		}
	}
	for _, rec := range records {
		if _, found, err := base.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || found {
			t.Fatalf("mirror still holds %s: found=%v err=%v", rec.RecordID, found, err)
		}
	}
}

// A batch spanning scopes is grouped, not rejected: one apply per scope.
func TestDeleteRecordsGroupsAcrossScopes(t *testing.T) {
	_, store := newDeleteTestStore(t)
	ctx := t.Context()

	records := []database.DomainRecord{
		deleteTestRecord("orders", "order_1", "cust_alice"),
		deleteTestRecord("carts", "cart_1", "cust_alice"),
		deleteTestRecord("orders", "order_2", "cust_bob"),
	}
	if _, err := store.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}

	perProjection := map[string]int{}
	cancel := store.Store().Observe(func(projection string, mutations []AppliedMutation) {
		perProjection[projection] += len(mutations)
	})
	defer cancel()

	if err := store.DeleteRecords(ctx, records); err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}
	if len(perProjection) != 2 {
		t.Fatalf("applied to %v, want one batch per scope", perProjection)
	}
	if perProjection[store.ProjectionName("marketplace", "orders", "org_1")] != 2 {
		t.Fatalf("orders batch = %v, want 2 mutations", perProjection)
	}
	if perProjection[store.ProjectionName("marketplace", "carts", "org_1")] != 1 {
		t.Fatalf("carts batch = %v, want 1 mutation", perProjection)
	}
}

// The degenerate sizes route to the single-record path and to a no-op, so a
// caller never has to special-case an empty or one-element batch.
func TestDeleteRecordsHandlesDegenerateBatches(t *testing.T) {
	base, store := newDeleteTestStore(t)
	ctx := t.Context()

	if err := store.DeleteRecords(ctx, nil); err != nil {
		t.Fatalf("DeleteRecords(nil) error = %v", err)
	}
	one := deleteTestRecord("orders", "order_1", "cust_alice")
	if _, err := store.UpsertRecord(ctx, one); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	if err := store.DeleteRecords(ctx, []database.DomainRecord{one}); err != nil {
		t.Fatalf("DeleteRecords(one) error = %v", err)
	}
	if _, found, err := base.GetRecord(ctx, one.Domain, one.Collection, one.OrganizationID, one.RecordID); err != nil || found {
		t.Fatalf("mirror still holds the record: found=%v err=%v", found, err)
	}
}

// The sweeper flushes at BatchSize, so a source yielding more than one batch
// converges in whole batches rather than per row, and the cursor still advances
// past everything that converged.
func TestMirrorSweeperDeletesInBatches(t *testing.T) {
	_, store := newDeleteTestStore(t)
	ctx := t.Context()

	deletedAt := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	records := make([]database.DomainRecord, 0, 5)
	for _, id := range []string{"order_1", "order_2", "order_3", "order_4", "order_5"} {
		records = append(records, deleteTestRecord("orders", id, "cust_alice"))
	}
	if _, err := store.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}

	var batches []int
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		batches = append(batches, len(mutations))
	})
	defer cancel()

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	source := func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		if !deletedAt.After(cursor) {
			return nil
		}
		for _, rec := range records {
			if visitErr := visit(rec, deletedAt); visitErr != nil {
				return visitErr
			}
		}
		return nil
	}
	if addErr := sweeper.AddDeleteRecordSource("orders_tombstones", source); addErr != nil {
		t.Fatalf("AddDeleteRecordSource() error = %v", addErr)
	}

	n, err := sweeper.SweepOnce(ctx)
	if err != nil || n != len(records) {
		t.Fatalf("SweepOnce() = %d err=%v, want %d", n, err, len(records))
	}
	// BatchSize 2 over 5 rows: two full batches plus the final flush.
	want := []int{2, 2, 1}
	if len(batches) != len(want) {
		t.Fatalf("applied batches %v, want %v", batches, want)
	}
	for index, size := range want {
		if batches[index] != size {
			t.Fatalf("applied batches %v, want %v", batches, want)
		}
	}
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 0 {
		t.Fatalf("idle SweepOnce() = %d err=%v, want 0", n, err)
	}
}

// A source that fails mid-stream must not advance the cursor or claim the
// queued-but-unflushed tail, so the next pass re-reads instead of skipping.
func TestMirrorSweeperDeleteFailureDoesNotAdvanceCursor(t *testing.T) {
	_, store := newDeleteTestStore(t)
	ctx := t.Context()

	records := []database.DomainRecord{
		deleteTestRecord("orders", "order_1", "cust_alice"),
		deleteTestRecord("orders", "order_2", "cust_alice"),
	}
	if _, err := store.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 16})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	failure := errors.New("tombstone source down")
	source := func(_ context.Context, _ time.Time, visit func(database.DomainRecord, time.Time) error) error {
		// One row is queued, then the source fails before the batch flushes.
		if err := visit(records[0], time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
		return failure
	}
	if err := sweeper.AddDeleteRecordSource("orders_tombstones", source); err != nil {
		t.Fatalf("AddDeleteRecordSource() error = %v", err)
	}

	if _, sweepErr := sweeper.SweepOnce(ctx); !errors.Is(sweepErr, failure) {
		// SweepOnce aggregates source errors; the sweep must report it.
		if stats := sweeper.Stats(); stats.Errors == 0 {
			t.Fatalf("SweepOnce() err=%v and Errors=%d, want the source failure counted", sweepErr, stats.Errors)
		}
	}
	if stats := sweeper.Stats(); stats.Swept != 0 {
		t.Fatalf("Swept = %d, want 0 — the queued row never flushed", stats.Swept)
	}
}

// batchDeleteMemoryDB is a base store that CAN batch its deletes, so the
// capability seam in DeleteRecords has something to detect.
type batchDeleteMemoryDB struct {
	*database.MemoryDB
	batches int
	deleted int
}

func (d *batchDeleteMemoryDB) DeleteRecordsBatch(ctx context.Context, records []database.DomainRecord) error {
	d.batches++
	for _, rec := range records {
		if err := d.MemoryDB.DeleteRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil {
			return err
		}
		d.deleted++
	}
	return nil
}

// A base store that implements DeleteRecordsBatch receives ONE call for the
// whole batch instead of one per record. PostgresDB implements it, so this is
// the seam that turns N round trips into one on a real deployment.
func TestDeleteRecordsUsesBaseStoreBatchCapability(t *testing.T) {
	base := &batchDeleteMemoryDB{MemoryDB: database.NewMemoryDB()}
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 32, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	records := []database.DomainRecord{
		deleteTestRecord("orders", "order_1", "cust_alice"),
		deleteTestRecord("orders", "order_2", "cust_bob"),
		deleteTestRecord("orders", "order_3", "cust_alice"),
	}
	if _, err := store.UpsertRecords(ctx, records); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}
	if err := store.DeleteRecords(ctx, records); err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}

	if base.batches != 1 {
		t.Fatalf("base DeleteRecordsBatch called %d times, want 1", base.batches)
	}
	if base.deleted != len(records) {
		t.Fatalf("base deleted %d records, want %d", base.deleted, len(records))
	}
	for _, rec := range records {
		if _, found, err := base.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || found {
			t.Fatalf("mirror still holds %s: found=%v err=%v", rec.RecordID, found, err)
		}
	}
}

// A single-record delete does not take the batch path, so a base store that
// batches is not called with a one-element slice for every ordinary delete.
func TestDeleteRecordsSingleSkipsBatchCapability(t *testing.T) {
	base := &batchDeleteMemoryDB{MemoryDB: database.NewMemoryDB()}
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	one := deleteTestRecord("orders", "order_1", "cust_alice")
	if _, err := store.UpsertRecord(ctx, one); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	if err := store.DeleteRecords(ctx, []database.DomainRecord{one}); err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}
	if base.batches != 0 {
		t.Fatalf("base DeleteRecordsBatch called %d times for one record, want 0", base.batches)
	}
}
