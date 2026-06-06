package hermes

import (
	"context"
	"fmt"
	"testing"

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
