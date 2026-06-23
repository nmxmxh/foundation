package hermes

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// newRuntimeStore wraps an in-memory database as a projected runtime store. The
// in-memory DB implements the same database.RuntimeStore contract Postgres does,
// so the adapter surface is exercised against a real (if ephemeral) substrate —
// no service-backed harness required for these read/write/transaction paths.
func newRuntimeStore(t *testing.T) (*ProjectedRuntimeStore, *database.MemoryDB) {
	t.Helper()
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{
		IndexedFields: []string{"symbol"}, MaxRecordsPerScope: 64, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	return store, base
}

func stateRecord(recordID, symbol string) database.DomainRecord {
	return database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: recordID,
		Data: database.RecordData{{Name: "symbol", Value: database.StringValue(symbol)}},
	}
}

// TestProjectedRuntimeStoreCRUDThroughHotPlane covers the record lifecycle through
// the projected store: an upsert lands in the hot plane and is read back, a count
// reflects it, a list returns it, and a delete removes it from both planes.
func TestProjectedRuntimeStoreCRUDThroughHotPlane(t *testing.T) {
	store, _ := newRuntimeStore(t)
	ctx := t.Context()

	if _, err := store.UpsertRecord(ctx, stateRecord("tick_1", "OVS")); err != nil {
		t.Fatalf("UpsertRecord() err=%v", err)
	}

	got, ok, err := store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_1")
	if err != nil || !ok || got.RecordID != "tick_1" {
		t.Fatalf("GetRecord() = %+v ok=%v err=%v", got, ok, err)
	}

	count, err := store.CountRecords(ctx, "signals", "ticks", "org_1", database.RecordQuery{})
	if err != nil || count != 1 {
		t.Fatalf("CountRecords() = %d err=%v, want 1", count, err)
	}

	list, err := store.ListRecords(ctx, "signals", "ticks", "org_1", database.RecordQuery{})
	if err != nil || len(list) != 1 {
		t.Fatalf("ListRecords() = %d err=%v, want 1", len(list), err)
	}

	if err := store.DeleteRecord(ctx, "signals", "ticks", "org_1", "tick_1"); err != nil {
		t.Fatalf("DeleteRecord() err=%v", err)
	}
	if _, ok, _ := store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_1"); ok {
		t.Fatal("record should be gone after delete")
	}
}

// TestProjectedRuntimeStoreRawJSONPath covers the raw-JSON record lane
// (UpsertRecordJSON/GetRecordJSON) which projects typed fields into the hot plane.
func TestProjectedRuntimeStoreRawJSONPath(t *testing.T) {
	store, _ := newRuntimeStore(t)
	ctx := t.Context()

	raw := database.RawDomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_json",
		DataJSON: []byte(`{"symbol":"OVS"}`),
	}
	if _, err := store.UpsertRecordJSON(ctx, raw); err != nil {
		t.Fatalf("UpsertRecordJSON() err=%v", err)
	}
	got, ok, err := store.GetRecordJSON(ctx, "signals", "ticks", "org_1", "tick_json")
	if err != nil || !ok || got.RecordID != "tick_json" {
		t.Fatalf("GetRecordJSON() = %+v ok=%v err=%v", got, ok, err)
	}
}

// TestProjectedRuntimeStoreSQLAdapterDelegation covers the SQL-adapter surface the
// projected store forwards to the underlying substrate: Exec/ExecResult/QueryRow/
// Query and Stats all delegate, and EstimateCount reads through.
func TestProjectedRuntimeStoreSQLAdapterDelegation(t *testing.T) {
	store, _ := newRuntimeStore(t)
	ctx := t.Context()

	if err := store.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Exec() err=%v", err)
	}
	if _, err := store.ExecResult(ctx, "SELECT 1"); err != nil {
		t.Fatalf("ExecResult() err=%v", err)
	}
	if scanner := store.QueryRow(ctx, "SELECT 1"); scanner == nil {
		t.Fatal("QueryRow() returned nil scanner")
	}
	if _, err := store.Query(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Query() err=%v", err)
	}
	if _, err := store.EstimateCount(ctx, "signals", "ticks", "org_1"); err != nil {
		t.Fatalf("EstimateCount() err=%v", err)
	}
	_ = store.Stats() // delegates to the base store; must not panic
}

// TestProjectedRuntimeStorePassthroughTransaction covers BeginTx when the base
// store does not implement its own transaction boundary: a passthrough tx is
// returned that forwards statements to the base and treats commit/rollback as
// no-ops. (The in-memory DB has no native tx, which is exactly this case.)
func TestProjectedRuntimeStorePassthroughTransaction(t *testing.T) {
	store, _ := newRuntimeStore(t)
	ctx := t.Context()

	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx() err=%v", err)
	}
	if err := tx.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("tx.Exec() err=%v", err)
	}
	if scanner := tx.QueryRow(ctx, "SELECT 1"); scanner == nil {
		t.Fatal("tx.QueryRow() returned nil")
	}
	if _, err := tx.Query(ctx, "SELECT 1"); err != nil {
		t.Fatalf("tx.Query() err=%v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("tx.Commit() err=%v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("tx.Rollback() err=%v", err)
	}
}

// TestProjectedRuntimeStoreHealthTracksDegradation covers the health/stats surface:
// a freshly-wrapped store is healthy, marking a projection scope degraded surfaces
// through both HermesHealth and HermesRuntimeStats, and recovery clears it.
func TestProjectedRuntimeStoreHealthTracksDegradation(t *testing.T) {
	store, _ := newRuntimeStore(t)
	ctx := t.Context()

	if err := store.HermesHealth(ctx); err != nil {
		t.Fatalf("fresh store HermesHealth() = %v, want nil", err)
	}

	store.markDegraded("signals:ticks:org_1")
	if err := store.HermesHealth(ctx); err == nil {
		t.Fatal("HermesHealth() = nil after degradation, want error")
	}
	if stats := store.HermesRuntimeStats(); stats.DegradedScopes != 1 {
		t.Fatalf("DegradedScopes = %d, want 1", stats.DegradedScopes)
	}

	store.markHealthy("signals:ticks:org_1")
	if err := store.HermesHealth(ctx); err != nil {
		t.Fatalf("HermesHealth() after recovery = %v, want nil", err)
	}

	if store.Store() == nil {
		t.Fatal("Store() should expose the hot plane")
	}
	store.Close() // delegates to base Close; must not panic
}
