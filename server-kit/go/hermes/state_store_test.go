package hermes

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestProjectedRuntimeStoreUsesHermesForWarmStateScope(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{
		MaxRecordsPerScope: 8,
		MaxBytesPerScope:   1 << 20,
		IndexedFields:      []string{"bucket"},
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	for i := range 3 {
		_, err = store.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%d", i),
			Data:           testRecordData(map[string]any{"bucket": i % 2}),
		})
		if err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}

	items, err := store.ListRecords(ctx, "signals", "ticks", "org_1", testRecordQuery(10, map[string]any{"bucket": 1}))
	if err != nil || len(items) != 1 {
		t.Fatalf("ListRecords() len=%d err=%v", len(items), err)
	}
	projection := store.ProjectionName("signals", "ticks", "org_1")
	if _, ok := store.warm.Load(projection); !ok {
		t.Fatalf("projection %q was not marked warm", projection)
	}

	base.Close()
	count, err := store.CountRecords(ctx, "signals", "ticks", "org_1", testRecordQuery(0, map[string]any{"bucket": 1}))
	if err != nil || count != 1 {
		t.Fatalf("CountRecords() after base close = %d err=%v", count, err)
	}
}

// TestWarmScopePopulatesHotPartition proves WarmScope eagerly materializes the
// hermes hot partition from rows seeded directly into the base store (simulating
// a raw SQL seed that bypassed the projected write path). This is the regression
// guard for the projection gateway returning "projection not found" for
// out-of-band seed rows: after WarmScope, the same partition the gateway reads
// (store.Store() under ProjectionName) serves the seeded records without the
// base store.
func TestWarmScopePopulatesHotPartition(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	// Seed rows directly into the base store only (bypassing the projected write
	// path), so the hot partition starts empty.
	for i := range 2 {
		if _, err = base.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "menu",
			Collection:     "dishes",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("dish_%d", i),
			Data:           testRecordData(map[string]any{"state": "published"}),
		}); err != nil {
			t.Fatalf("base UpsertRecord() error = %v", err)
		}
	}

	projection := store.ProjectionName("menu", "dishes", "org_1")
	if _, ok := store.warm.Load(projection); ok {
		t.Fatalf("projection %q should not be warm before WarmScope", projection)
	}

	if err = store.WarmScope(ctx, "menu", "dishes", "org_1"); err != nil {
		t.Fatalf("WarmScope() error = %v", err)
	}
	if _, ok := store.warm.Load(projection); !ok {
		t.Fatalf("projection %q was not marked warm after WarmScope", projection)
	}

	// With the base store closed, the seeded rows are only served if WarmScope
	// materialized them into the hot partition the gateway reads.
	base.Close()
	count, err := store.hot.Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{})
	if err != nil {
		t.Fatalf("hot Count() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("hot partition count = %d, want 2 (WarmScope should have materialized seed rows)", count)
	}
}

func TestProjectedRuntimeStoreExactReadThroughAndDelete(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	_, err = base.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: "org_1",
		RecordID:       "tick_1",
		Data:           testRecordData(map[string]any{"state": "open"}),
	})
	if err != nil {
		t.Fatalf("base UpsertRecord() error = %v", err)
	}

	rec, found, err := store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_1")
	if err != nil || !found || !recordDataStringEquals(rec.Data, "state", "open") {
		t.Fatalf("GetRecord() = %+v found=%v err=%v", rec, found, err)
	}
	base.Close()
	rec, found, err = store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_1")
	if err != nil || !found || !recordDataStringEquals(rec.Data, "state", "open") {
		t.Fatalf("hot GetRecord() = %+v found=%v err=%v", rec, found, err)
	}
}

func TestProjectedRuntimeStoreOversizedScopeFallsBackToDatabase(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 1, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	for i := range 2 {
		_, err = store.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%d", i),
			Data:           testRecordData(map[string]any{"state": "open"}),
		})
		if err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}

	items, err := store.ListRecords(ctx, "signals", "ticks", "org_1", database.RecordQuery{Limit: 10})
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecords() fallback len=%d err=%v", len(items), err)
	}
	stats := store.HermesRuntimeStats()
	if stats.Fallbacks == 0 {
		t.Fatalf("expected bounded scope fallback to be counted: %+v", stats)
	}
	if err := store.HermesHealth(context.Background()); err != nil {
		t.Fatalf("HermesHealth() should stay healthy on bounded fallback: %v", err)
	}
}

func TestProjectedRuntimeStoreRawJSONProjectsTypedFields(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{IndexedFields: []string{"state"}})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	_, err = store.UpsertRecordJSON(ctx, database.RawDomainRecord{
		Domain:         "orders",
		Collection:     "book",
		OrganizationID: "org_1",
		RecordID:       "order_1",
		DataJSON:       []byte(`{"state":"open"}`),
	})
	if err != nil {
		t.Fatalf("UpsertRecordJSON() error = %v", err)
	}
	count, err := store.CountRecords(ctx, "orders", "book", "org_1", testRecordQuery(0, map[string]any{"state": "open"}))
	if err != nil || count != 1 {
		t.Fatalf("CountRecords() = %d err=%v", count, err)
	}
}

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
	_ = store.Stats()
}

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
	store.Close()
}

// TestTTLExpiryReachesObservers pins the drift fix. hermes retires records on
// TTL inside the apply, and it used to do so silently, so every downstream
// aggregate kept counting a record the projection no longer held and no replica
// was told to drop it.
func TestTTLExpiryReachesObservers(t *testing.T) {
	store, err := NewStore(ProjectionSpec{
		Name: "ttl_observe", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    1024, MaxBytes: 8 << 20,
		TTL: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	var mu sync.Mutex
	var observed []AppliedMutation
	cancel := store.Observe(func(_ string, mutations []AppliedMutation) {
		mu.Lock()
		observed = append(observed, mutations...)
		mu.Unlock()
	})
	defer cancel()

	rec := database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "r1",
		Data: database.RecordData{{Name: "bucket", Value: database.IntValue(1)}},
	}
	if _, err := store.ApplyRecords(context.Background(), "ttl_observe", "src:1", 1, []database.DomainRecord{rec}); err != nil {
		t.Fatalf("ApplyRecords: %v", err)
	}
	if _, _, err := store.GetRecord(context.Background(), "ttl_observe", Query{OrganizationID: "org_1"}, "r1", Fence{}); err != nil {
		t.Fatalf("record should be live before expiry: %v", err)
	}

	time.Sleep(80 * time.Millisecond)

	// The next apply sweeps the expired record, which must be reported.
	if _, err := store.ApplyRecords(context.Background(), "ttl_observe", "src:2", 2, []database.DomainRecord{{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "r2",
		Data: database.RecordData{{Name: "bucket", Value: database.IntValue(2)}},
	}}); err != nil {
		t.Fatalf("second ApplyRecords: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawExpiry bool
	for _, m := range observed {
		if m.Operation == OperationDelete && m.Record.RecordID == "r1" {
			sawExpiry = true
			if m.Version != 1 {
				t.Fatalf("expiry delete version = %d, want the record's own version 1", m.Version)
			}
			if m.Record.OrganizationID != "org_1" {
				t.Fatalf("expiry delete org = %q, want org_1", m.Record.OrganizationID)
			}
		}
	}
	if !sawExpiry {
		t.Fatalf("expiry was not reported to observers; observed %d mutations", len(observed))
	}
}

// TestAccumulatorTracksHermesTTLRoundTrip checks the two expiry paths agree: an
// accumulator attached to a TTL projection should lose the record's aggregate
// when the projection retires it, without needing its own TTL configured.
func TestAccumulatorTracksHermesTTLRoundTrip(t *testing.T) {
	store, err := NewStore(ProjectionSpec{
		Name: "ttl_acc", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"country"},
		MaxRecords:    1024, MaxBytes: 8 << 20,
		TTL: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	acc := NewAccumulatorStateStore(AccumulatorConfig{Dimensions: []string{"country"}})
	defer acc.AttachToStore(store)()

	scope := ScopeKey("signals", "ticks", "org_1")
	rec := database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "r1",
		Data: database.RecordData{{Name: "country", Value: database.StringValue("C_1")}},
	}
	if _, err := store.ApplyRecords(context.Background(), "ttl_acc", "src:1", 1, []database.DomainRecord{rec}); err != nil {
		t.Fatalf("ApplyRecords: %v", err)
	}
	if got := acc.GetDimensionSummary(scope, "country", 10).DistinctCount; got != 1 {
		t.Fatalf("distinct count before expiry = %d, want 1", got)
	}

	time.Sleep(80 * time.Millisecond)
	if _, err := store.ApplyRecords(context.Background(), "ttl_acc", "src:2", 2, []database.DomainRecord{{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "r2",
		Data: database.RecordData{{Name: "country", Value: database.StringValue("C_2")}},
	}}); err != nil {
		t.Fatalf("second ApplyRecords: %v", err)
	}

	// r1 must be gone and only r2 counted. Before the fix the count would be 2.
	if got := acc.GetDimensionSummary(scope, "country", 10).DistinctCount; got != 1 {
		t.Fatalf("distinct count after expiry = %d, want 1 (only the live record)", got)
	}
	values := acc.GetDimensionSummary(scope, "country", 10).TopValues
	if len(values) != 1 || values[0].Value != "C_2" {
		t.Fatalf("top values = %+v, want only C_2", values)
	}
}
