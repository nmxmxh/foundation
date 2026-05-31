package hermes

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

func TestStoreApplyGetListCountAndCopies(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "signals_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket", "symbol"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "signals_ticks", "org_1", "tick_1", 1, map[string]any{
		"bucket": 1,
		"symbol": "OVS",
	})
	applyTestRecord(t, store, "signals_ticks", "org_1", "tick_2", 2, map[string]any{
		"bucket": 2,
		"symbol": "OVS",
	})

	rec, ok, err := store.GetRecord(ctx, "signals_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !ok {
		t.Fatalf("GetRecord() ok=%v err=%v", ok, err)
	}
	rec.Data["symbol"] = "mutated"
	rec, ok, err = store.GetRecord(ctx, "signals_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !ok || rec.Data["symbol"] != "OVS" {
		t.Fatalf("GetRecord() copy isolation failed: %+v ok=%v err=%v", rec, ok, err)
	}

	items, err := store.ListRecords(ctx, "signals_ticks", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"symbol": "OVS"},
		Limit:          10,
	}, Fence{})
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecords() len=%d err=%v", len(items), err)
	}
	count, err := store.Count(ctx, "signals_ticks", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 2},
	}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestStoreForEachViewPreservesBatchVersionOrder(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "ordered_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	events := []Event{
		{
			Operation: OperationUpsert,
			SourceID:  "ordered_1",
			Version:   1,
			Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 7}),
		},
		{
			Operation: OperationUpsert,
			SourceID:  "ordered_2",
			Version:   2,
			Record:    testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"bucket": 7}),
		},
		{
			Operation: OperationUpsert,
			SourceID:  "ordered_3",
			Version:   3,
			Record:    testRecord("signals", "ticks", "org_1", "tick_3", map[string]any{"bucket": 7}),
		},
	}
	if _, err := store.ApplyBatch(t.Context(), "ordered_ticks", events); err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}
	var got []string
	seen, err := store.ForEachView(t.Context(), "ordered_ticks", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 7},
		Limit:          3,
	}, Fence{}, func(view RecordView) error {
		got = append(got, view.RecordID)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachView() error = %v", err)
	}
	if seen != 3 {
		t.Fatalf("ForEachView() seen = %d, want 3", seen)
	}
	for i, want := range []string{"tick_3", "tick_2", "tick_1"} {
		if got[i] != want {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestStoreRejectsTenantScopeAndBounds(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "bounded_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 1,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "bounded_ticks", "org_1", "tick_1", 1, nil)
	_, err := store.Apply(ctx, "bounded_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "bad_scope",
		Version:   2,
		Record: database.DomainRecord{
			Domain:         "other",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_bad",
		},
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("scope err = %v, want ErrInvalidEvent", err)
	}
	_, err = store.Apply(ctx, "bounded_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "limit",
		Version:   3,
		Record:    testRecord("signals", "ticks", "org_1", "tick_2", nil),
	})
	if !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("limit err = %v, want ErrProjectionLimit", err)
	}
}

func TestStoreIdempotentVersionAndTombstone(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "versioned_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 10,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "versioned_ticks", "org_1", "tick_1", 10, map[string]any{"value": 10})
	result, err := store.Apply(ctx, "versioned_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "src_tick_1_10",
		Version:   10,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"value": 99}),
	})
	if err != nil || result.Duplicates != 1 {
		t.Fatalf("duplicate result=%+v err=%v", result, err)
	}
	_, err = store.Apply(ctx, "versioned_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "old",
		Version:   9,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"value": 9}),
	})
	if err != nil {
		t.Fatalf("older upsert failed: %v", err)
	}
	_, err = store.Apply(ctx, "versioned_ticks", Event{
		Operation: OperationDelete,
		SourceID:  "delete_11",
		Version:   11,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", nil),
	})
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	_, ok, err := store.GetRecord(ctx, "versioned_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || ok {
		t.Fatalf("GetRecord() after delete ok=%v err=%v", ok, err)
	}
	_, err = store.Apply(ctx, "versioned_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "old_after_delete",
		Version:   10,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"value": 10}),
	})
	if err != nil {
		t.Fatalf("old after delete failed: %v", err)
	}
	count, err := store.Count(ctx, "versioned_ticks", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 0 {
		t.Fatalf("Count() after stale replay = %d err=%v", count, err)
	}
}

func TestStoreRebuildFromStateStore(t *testing.T) {
	source := database.NewMemoryDB()
	ctx := t.Context()
	for i := range 3 {
		if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), map[string]any{"bucket": i % 2})); err != nil {
			t.Fatalf("source upsert failed: %v", err)
		}
	}
	store := newTestStore(t, ProjectionSpec{
		Name:          "rebuilt_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	result, err := store.Rebuild(ctx, "rebuilt_ticks", source, Query{OrganizationID: "org_1"})
	if err != nil || result.Applied != 3 || result.Epoch == 0 {
		t.Fatalf("Rebuild() result=%+v err=%v", result, err)
	}
	items, err := store.ListRecords(ctx, "rebuilt_ticks", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 1},
	}, Fence{MinEpoch: result.Epoch})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListRecords() after rebuild len=%d err=%v", len(items), err)
	}
}

func TestStoreBulkLoadReplacesSnapshotAndKeepsOldStateOnLimitError(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "bulk_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    2,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	result, err := store.Apply(ctx, "bulk_ticks", Event{
		Operation: OperationUpsert,
		SourceID:  "seed_1",
		Version:   100,
		Record:    testRecord("signals", "ticks", "org_1", "tick_old", map[string]any{"bucket": 0}),
	})
	if err != nil || result.Applied != 1 {
		t.Fatalf("seed Apply() result=%+v err=%v", result, err)
	}
	if _, err = store.Apply(ctx, "bulk_ticks", Event{
		Operation: OperationDelete,
		SourceID:  "delete_1",
		Version:   101,
		Record:    testRecord("signals", "ticks", "org_1", "tick_reborn", nil),
	}); err != nil {
		t.Fatalf("seed delete failed: %v", err)
	}

	result, err = store.BulkLoad(ctx, "bulk_ticks", []database.DomainRecord{
		testRecord("signals", "ticks", "org_1", "tick_reborn", map[string]any{"bucket": 1}),
		testRecord("signals", "ticks", "org_1", "tick_new", map[string]any{"bucket": 1}),
	})
	if err != nil || result.Applied != 2 {
		t.Fatalf("BulkLoad() result=%+v err=%v", result, err)
	}
	count, err := store.Count(ctx, "bulk_ticks", Query{OrganizationID: "org_1", Filters: map[string]any{"bucket": 1}}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() after BulkLoad = %d err=%v", count, err)
	}
	stats, err := store.Stats("bulk_ticks")
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.AppliedEvents != 0 || stats.Tombstones != 0 {
		t.Fatalf("BulkLoad should reset event/tombstone control-plane state: %+v", stats)
	}
	if stats.SourceWatermark != 2 || stats.RejectedApplies != 0 {
		t.Fatalf("BulkLoad metrics were not reset to snapshot state: %+v", stats)
	}

	_, err = store.BulkLoad(ctx, "bulk_ticks", []database.DomainRecord{
		testRecord("signals", "ticks", "org_1", "tick_1", nil),
		testRecord("signals", "ticks", "org_1", "tick_2", nil),
		testRecord("signals", "ticks", "org_1", "tick_3", nil),
	})
	if !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("BulkLoad over limit err=%v, want ErrProjectionLimit", err)
	}
	count, err = store.Count(ctx, "bulk_ticks", Query{OrganizationID: "org_1", Filters: map[string]any{"bucket": 1}}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() after failed BulkLoad = %d err=%v", count, err)
	}
	stats, err = store.Stats("bulk_ticks")
	if err != nil {
		t.Fatalf("Stats() after failed BulkLoad error = %v", err)
	}
	if stats.RejectedApplies == 0 {
		t.Fatalf("expected failed BulkLoad to record a rejected apply: %+v", stats)
	}
}

func TestStoreForEachViewBorrowed(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "views",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 10,
		MaxBytes:   1 << 20,
	})
	applyTestRecord(t, store, "views", "org_1", "tick_1", 1, map[string]any{"value": 7})
	var got int
	seen, err := store.ForEachView(t.Context(), "views", Query{OrganizationID: "org_1"}, Fence{}, func(view RecordView) error {
		if view.Data["value"] != 7 || view.Epoch == 0 {
			t.Fatalf("unexpected view: %+v", view)
		}
		got++
		return nil
	})
	if err != nil || seen != 1 || got != 1 {
		t.Fatalf("ForEachView() seen=%d got=%d err=%v", seen, got, err)
	}
}

func TestWorkerProcessorAppliesBatch(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "worker_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 10,
		MaxBytes:   1 << 20,
	})
	processor, err := NewWorkerProcessor(store, "worker_ticks", "hermes_project", "hotplane", func(_ context.Context, job worker.Job) ([]Event, error) {
		return []Event{{
			Operation: OperationUpsert,
			Version:   1,
			Record:    testRecord("signals", "ticks", "org_1", "tick_1", job.Payload),
		}}, nil
	})
	if err != nil {
		t.Fatalf("NewWorkerProcessor() error = %v", err)
	}
	err = processor.Handle(t.Context(), worker.Job{
		CorrelationID:  "corr_1",
		IdempotencyKey: "idem_1",
		Payload:        map[string]any{"value": 42},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	rec, ok, err := store.GetRecord(t.Context(), "worker_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !ok || rec.Data["value"] != 42 {
		t.Fatalf("worker projection record = %+v ok=%v err=%v", rec, ok, err)
	}
}

func newTestStore(t *testing.T, spec ProjectionSpec) *Store {
	t.Helper()
	store, err := NewStore(spec)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func applyTestRecord(t *testing.T, store *Store, projection string, orgID string, recordID string, version uint64, data map[string]any) {
	t.Helper()
	_, err := store.Apply(t.Context(), projection, Event{
		Operation: OperationUpsert,
		SourceID:  fmt.Sprintf("src_%s_%d", recordID, version),
		Version:   version,
		Record:    testRecord("signals", "ticks", orgID, recordID, data),
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
}

func testRecord(domain string, collection string, orgID string, recordID string, data map[string]any) database.DomainRecord {
	now := time.Unix(1700000000, 0).UTC()
	return database.DomainRecord{
		Domain:         domain,
		Collection:     collection,
		OrganizationID: orgID,
		RecordID:       recordID,
		Data:           data,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}
