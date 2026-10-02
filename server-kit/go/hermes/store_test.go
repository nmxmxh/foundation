package hermes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
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
	rec.Data = rec.Data.With("symbol", database.StringValue("mutated"))
	rec, ok, err = store.GetRecord(ctx, "signals_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !ok || !recordDataStringEquals(rec.Data, "symbol", "OVS") {
		t.Fatalf("GetRecord() copy isolation failed: %+v ok=%v err=%v", rec, ok, err)
	}

	items, err := store.ListRecords(ctx, "signals_ticks", QueryFromRecordQuery("org_1", testRecordQuery(10, map[string]any{"symbol": "OVS"})), Fence{})
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecords() len=%d err=%v", len(items), err)
	}
	count, err := store.Count(ctx, "signals_ticks", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 2})), Fence{})
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
	seen, err := store.ForEachView(t.Context(), "ordered_ticks", QueryFromRecordQuery("org_1", testRecordQuery(3, map[string]any{"bucket": 7})), Fence{}, func(view RecordView) error {
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

func TestStorePatchArchiveDeleteAndStaleInvalidation(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "patch_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"status", "bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "patch_ticks", "org_1", "tick_1", 10, map[string]any{
		"status": "active",
		"bucket": 1,
		"title":  "original",
	})

	result, err := store.Apply(ctx, "patch_ticks", Event{
		Operation: OperationPatch,
		SourceID:  "patch_archive_11",
		Version:   11,
		Record: testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{
			"status": "archived",
			"bucket": 2,
		}),
	})
	if err != nil || result.Applied != 1 || result.Epoch == 0 {
		t.Fatalf("archive patch result=%+v err=%v", result, err)
	}
	rec, ok, err := store.GetRecord(ctx, "patch_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{MinEpoch: result.Epoch})
	if err != nil || !ok {
		t.Fatalf("GetRecord() after patch ok=%v err=%v", ok, err)
	}
	if !recordDataStringEquals(rec.Data, "title", "original") ||
		!recordDataStringEquals(rec.Data, "status", "archived") ||
		!recordDataIntEquals(rec.Data, "bucket", 2) {
		t.Fatalf("patch did not merge fields correctly: %+v", rec.Data)
	}
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"status": "active"}, 0)
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"status": "archived"}, 1)
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"bucket": 1}, 0)
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"bucket": 2}, 1)

	result, err = store.Apply(ctx, "patch_ticks", Event{
		Operation: OperationPatch,
		SourceID:  "stale_patch_9",
		Version:   9,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"status": "active", "bucket": 1}),
	})
	if err != nil || result.Applied != 0 || result.Ignored != 1 {
		t.Fatalf("stale patch result=%+v err=%v, want ignored", result, err)
	}
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"status": "archived"}, 1)

	result, err = store.Apply(ctx, "patch_ticks", Event{
		Operation: OperationDelete,
		SourceID:  "delete_12",
		Version:   12,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", nil),
	})
	if err != nil || result.Applied != 1 {
		t.Fatalf("delete result=%+v err=%v", result, err)
	}
	_, ok, err = store.GetRecord(ctx, "patch_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{MinEpoch: result.Epoch})
	if err != nil || ok {
		t.Fatalf("GetRecord() after delete ok=%v err=%v", ok, err)
	}
	assertHermesCount(t, store, "patch_ticks", "org_1", map[string]any{"status": "archived"}, 0)

	result, err = store.Apply(ctx, "patch_ticks", Event{
		Operation: OperationPatch,
		SourceID:  "patch_after_delete_13",
		Version:   13,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"status": "active"}),
	})
	if err != nil || result.Applied != 0 || result.Ignored != 1 {
		t.Fatalf("patch after delete result=%+v err=%v, want ignored", result, err)
	}
	_, ok, err = store.GetRecord(ctx, "patch_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || ok {
		t.Fatalf("patch resurrected deleted record ok=%v err=%v", ok, err)
	}
}

func TestStoreConcurrentPatchReadsNeverObserveTornArchiveState(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "concurrent_patch_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"status", "bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "concurrent_patch_ticks", "org_1", "tick_1", 1, map[string]any{
		"status": "active",
		"bucket": 1,
	})

	var done atomic.Bool
	var failed atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for !done.Load() {
				rec, ok, err := store.GetRecord(ctx, "concurrent_patch_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
				if err != nil || !ok {
					failed.Store(true)
					return
				}
				status, _ := rec.Data.Get("status")
				bucket, _ := rec.Data.Get("bucket")
				_, bucketText, _ := bucket.ScalarIndex()
				validActive := status.Text == "active" && bucketText == "1"
				validArchived := status.Text == "archived" && bucketText == "2"
				if !validActive && !validArchived {
					failed.Store(true)
					return
				}
			}
		})
	}

	for i := 2; i < 502; i++ {
		status := "active"
		bucket := 1
		if i%2 == 0 {
			status = "archived"
			bucket = 2
		}
		_, err := store.Apply(ctx, "concurrent_patch_ticks", Event{
			Operation: OperationPatch,
			SourceID:  fmt.Sprintf("patch_%d", i),
			Version:   uint64(i),
			Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"status": status, "bucket": bucket}),
		})
		if err != nil {
			t.Fatalf("patch %d failed: %v", i, err)
		}
	}
	done.Store(true)
	wg.Wait()
	if failed.Load() {
		t.Fatalf("concurrent reader observed missing or torn patch state")
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
	items, err := store.ListRecords(ctx, "rebuilt_ticks", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 1})), Fence{MinEpoch: result.Epoch})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListRecords() after rebuild len=%d err=%v", len(items), err)
	}
}

func TestStoreRebuildUsesStreamingStateStore(t *testing.T) {
	ctx := t.Context()
	source := streamingOnlyStateStore{
		records: []database.DomainRecord{
			testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 1}),
			testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"bucket": 1}),
		},
	}
	store := newTestStore(t, ProjectionSpec{
		Name:          "streaming_rebuild_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	result, err := store.Rebuild(ctx, "streaming_rebuild_ticks", source, Query{OrganizationID: "org_1"})
	if err != nil || result.Applied != 2 {
		t.Fatalf("Rebuild() result=%+v err=%v, want 2 streamed records", result, err)
	}
	count, err := store.Count(ctx, "streaming_rebuild_ticks", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 1})), Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() after streaming rebuild = %d err=%v, want 2", count, err)
	}
}

func TestStoreRebuildPrefersStreamingNormalizedSnapshot(t *testing.T) {
	ctx := t.Context()
	source := normalizedPreferenceStateStore{
		records: []database.DomainRecord{
			testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 1}),
		},
	}
	store := newTestStore(t, ProjectionSpec{
		Name:          "streaming_normalized_rebuild_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	result, err := store.Rebuild(ctx, "streaming_normalized_rebuild_ticks", &source, Query{OrganizationID: "org_1"})
	if err != nil || result.Applied != 1 {
		t.Fatalf("Rebuild() result=%+v err=%v, want 1 streamed normalized record", result, err)
	}
	if source.streamingCalls != 1 {
		t.Fatalf("streaming normalized calls=%d, want 1", source.streamingCalls)
	}
	if source.materializedCalls != 0 {
		t.Fatalf("materialized normalized calls=%d, want 0", source.materializedCalls)
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
	count, err := store.Count(ctx, "bulk_ticks", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 1})), Fence{})
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
	count, err = store.Count(ctx, "bulk_ticks", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 1})), Fence{})
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

type streamingOnlyStateStore struct {
	records []database.DomainRecord
}

func (s streamingOnlyStateStore) UpsertRecord(context.Context, database.DomainRecord) (database.DomainRecord, error) {
	return database.DomainRecord{}, errors.New("not implemented")
}

func (s streamingOnlyStateStore) GetRecord(context.Context, string, string, string, string) (database.DomainRecord, bool, error) {
	return database.DomainRecord{}, false, errors.New("not implemented")
}

func (s streamingOnlyStateStore) ForEachRecord(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery, fn database.RecordVisitor) error {
	for _, rec := range s.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rec.Domain != domain || rec.Collection != collection || rec.OrganizationID != organizationID {
			continue
		}
		if !rec.Data.Matches(query.Filters) {
			continue
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return nil
}

func (s streamingOnlyStateStore) ListRecords(context.Context, string, string, string, database.RecordQuery) ([]database.DomainRecord, error) {
	return nil, errors.New("ListRecords must not be used for Hermes rebuild")
}

func (s streamingOnlyStateStore) CountRecords(context.Context, string, string, string, database.RecordQuery) (int64, error) {
	return int64(len(s.records)), nil
}

func (s streamingOnlyStateStore) EstimateCount(context.Context, string, string, string) (int64, error) {
	return int64(len(s.records)), nil
}

func (s streamingOnlyStateStore) DeleteRecord(context.Context, string, string, string, string) error {
	return errors.New("not implemented")
}

type normalizedPreferenceStateStore struct {
	streamingOnlyStateStore
	records           []database.DomainRecord
	streamingCalls    int
	materializedCalls int
}

func (s *normalizedPreferenceStateStore) ForEachRecord(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery, fn database.RecordVisitor) error {
	return streamingOnlyStateStore{records: s.records}.ForEachRecord(ctx, domain, collection, organizationID, query, fn)
}

func (s *normalizedPreferenceStateStore) ForEachNormalizedRecord(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery, fn database.RecordVisitor) error {
	s.streamingCalls++
	return streamingOnlyStateStore{records: s.records}.ForEachRecord(ctx, domain, collection, organizationID, query, fn)
}

func (s *normalizedPreferenceStateStore) ListNormalizedRecords(context.Context, string, string, string, database.RecordQuery) ([]database.DomainRecord, error) {
	s.materializedCalls++
	return append([]database.DomainRecord(nil), s.records...), nil
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
		if !recordDataIntEquals(view.Data, "value", 7) || view.Epoch == 0 {
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
			Record:    testRecord("signals", "ticks", "org_1", "tick_1", job.Payload.InterfaceMap()),
		}}, nil
	})
	if err != nil {
		t.Fatalf("NewWorkerProcessor() error = %v", err)
	}
	err = processor.Handle(t.Context(), worker.Job{
		CorrelationID:  "corr_1",
		IdempotencyKey: "idem_1",
		Payload:        extension.Object{"value": extension.Int(42)},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	rec, ok, err := store.GetRecord(t.Context(), "worker_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !ok || !recordDataIntEquals(rec.Data, "value", 42) {
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

func assertHermesCount(t *testing.T, store *Store, projection string, orgID string, filters map[string]any, want int64) {
	t.Helper()
	count, err := store.Count(t.Context(), projection, QueryFromRecordQuery(orgID, testRecordQuery(0, filters)), Fence{})
	if err != nil || count != want {
		t.Fatalf("Count(%v) = %d err=%v, want %d", filters, count, err, want)
	}
}

func testRecord(domain string, collection string, orgID string, recordID string, data map[string]any) database.DomainRecord {
	now := time.Unix(1700000000, 0).UTC()
	return database.DomainRecord{
		Domain:         domain,
		Collection:     collection,
		OrganizationID: orgID,
		RecordID:       recordID,
		Data:           testRecordData(data),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// TestStoreObserverSeamFiresOnlyForAcceptedWrites covers the universal delta seam
// (the path projectiongw fans out): registered observers receive accepted
// mutations after an apply, a deduplicated re-apply produces no observer
// callback (only real state changes are visible deltas), and cancellation stops
// delivery. This is the core invariant of the live projection stream.
func TestStoreObserverSeamFiresOnlyForAcceptedWrites(t *testing.T) {
	store := newTestStore(t, driftSpec())
	ctx := t.Context()

	var mu sync.Mutex
	var seen []AppliedMutation
	var seenProjection string
	cancel := store.Observe(func(projection string, mutations []AppliedMutation) {
		mu.Lock()
		seenProjection = projection
		seen = append(seen, mutations...)
		mu.Unlock()
	})

	evt := Event{
		Operation: OperationUpsert,
		SourceID:  "src_dedup_1",
		Version:   1,
		Record:    testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "OVS"}),
	}
	if _, err := store.Apply(ctx, "signals", evt); err != nil {
		t.Fatalf("Apply() err=%v", err)
	}
	mu.Lock()
	if seenProjection != "signals" || len(seen) != 1 || seen[0].Record.RecordID != "tick_1" {
		mu.Unlock()
		t.Fatalf("observer saw projection=%q mutations=%+v, want 1 for tick_1", seenProjection, seen)
	}
	mu.Unlock()

	// Re-applying the identical event (same SourceID+version) is deduplicated and
	// must NOT surface as a delta.
	if _, err := store.Apply(ctx, "signals", evt); err != nil {
		t.Fatalf("Apply(dup) err=%v", err)
	}
	mu.Lock()
	if len(seen) != 1 {
		mu.Unlock()
		t.Fatalf("deduplicated re-apply produced %d observed mutations, want 1", len(seen))
	}
	mu.Unlock()

	// After cancellation no further deltas are delivered.
	cancel()
	if _, err := store.Apply(ctx, "signals", Event{
		Operation: OperationUpsert, SourceID: "src_2", Version: 2,
		Record: testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"symbol": "ABC"}),
	}); err != nil {
		t.Fatalf("Apply(after cancel) err=%v", err)
	}
	mu.Lock()
	if len(seen) != 1 {
		mu.Unlock()
		t.Fatalf("cancelled observer still received %d mutations", len(seen))
	}
	mu.Unlock()

	if epoch, err := store.Epoch("signals"); err != nil || epoch == 0 {
		t.Fatalf("Epoch() = %d err=%v, want non-zero", epoch, err)
	}
}

// TestApplyBatchObservedStampsAcceptedVersions covers the source-of-truth apply
// path used by the projection gateway: the per-mutation observer is invoked once
// per accepted event, stamped with the version hermes assigned, and a duplicate
// in the batch is not re-observed.
func TestApplyBatchObservedStampsAcceptedVersions(t *testing.T) {
	store := newTestStore(t, driftSpec())
	ctx := t.Context()

	var observed []AppliedMutation
	events := []Event{
		{Operation: OperationUpsert, SourceID: "s1", Version: 1,
			Record: testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "A"})},
		{Operation: OperationUpsert, SourceID: "s2", Version: 2,
			Record: testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"symbol": "B"})},
		// Duplicate of s1 -> not accepted again.
		{Operation: OperationUpsert, SourceID: "s1", Version: 1,
			Record: testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "A"})},
	}
	if _, err := store.ApplyBatchObserved(ctx, "signals", events, func(m AppliedMutation) {
		observed = append(observed, m)
	}); err != nil {
		t.Fatalf("ApplyBatchObserved() err=%v", err)
	}
	if len(observed) != 2 {
		t.Fatalf("observed %d accepted mutations, want 2 (duplicate excluded)", len(observed))
	}
	for _, m := range observed {
		if m.Version == 0 {
			t.Fatalf("accepted mutation missing assigned version: %+v", m)
		}
	}
}
func testRecordData(values map[string]any) database.RecordData {
	if len(values) == 0 {
		return nil
	}
	fields := make(database.RecordData, 0, len(values))
	for name, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		if !ok {
			panic("unsupported record field " + name)
		}
		fields = append(fields, database.RecordField{Name: name, Value: value})
	}
	return fields.Normalize()
}

func testRecordQuery(limit int, values map[string]any) database.RecordQuery {
	query := database.RecordQuery{Limit: limit}
	if len(values) == 0 {
		return query
	}
	query.Filters = make([]database.RecordFilter, 0, len(values))
	for field, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		if !ok {
			panic("unsupported record filter " + field)
		}
		query.Filters = append(query.Filters, database.RecordFilter{Field: field, Value: value})
	}
	return query.Normalize()
}

func recordDataStringEquals(data database.RecordData, field string, want string) bool {
	value, ok := data.Get(field)
	return ok && value.Kind == database.RecordValueString && value.Text == want
}

func recordDataIntEquals(data database.RecordData, field string, want int64) bool {
	value, ok := data.Get(field)
	_, text, scalar := value.ScalarIndex()
	return ok && scalar && text == database.IntValue(want).Text
}

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

func TestUpsertRecordsBatchGroupsScopesAndStaysCoherent(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	var accepted []AppliedMutation
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		accepted = append(accepted, mutations...)
	})
	defer cancel()

	saved, err := store.UpsertRecords(ctx, []database.DomainRecord{
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1",
			Data: testRecordData(map[string]any{"state": "published"})},
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_2",
			Data: testRecordData(map[string]any{"state": "published"})},
		{Domain: "billing", Collection: "subscriptions", OrganizationID: "org_1", RecordID: "sub_1",
			Data: testRecordData(map[string]any{"status": "active"})},
	})
	if err != nil || len(saved) != 3 {
		t.Fatalf("UpsertRecords() len=%d err=%v", len(saved), err)
	}
	if len(accepted) != 3 {
		t.Fatalf("observer saw %d mutations, want 3 (batch applies must fan out)", len(accepted))
	}
	for _, check := range []struct {
		domain, collection string
		want               int64
	}{{"menu", "dishes", 2}, {"billing", "subscriptions", 1}} {
		name := store.ProjectionName(check.domain, check.collection, "org_1")
		count, err := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{})
		if err != nil || count != check.want {
			t.Fatalf("hot count %s/%s = %d err=%v, want %d", check.domain, check.collection, count, err, check.want)
		}
	}
	if _, found, err := base.GetRecord(ctx, "billing", "subscriptions", "org_1", "sub_1"); err != nil || !found {
		t.Fatalf("base record missing after batch: found=%v err=%v", found, err)
	}

	// A later single-record upsert must win LWW over the batch (shared counter).
	if _, err := store.UpsertRecord(ctx, database.DomainRecord{
		Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1",
		Data: testRecordData(map[string]any{"state": "sold_out"}),
	}); err != nil {
		t.Fatalf("follow-up UpsertRecord() error = %v", err)
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	rec, found, err := store.Store().GetRecord(ctx, name, Query{OrganizationID: "org_1"}, "dish_1", Fence{})
	if err != nil || !found || !recordDataStringEquals(rec.Data, "state", "sold_out") {
		t.Fatalf("post-batch LWW record = %+v found=%v err=%v, want state=sold_out", rec, found, err)
	}
}

// TestApplyRecordsSkipsEventLevelRejections extends the batch-tolerance
// contract to the trusted-records lane (the batch-upsert hot path): a capacity
// rejection mid-batch poisons only the overflowing record — the records before
// it stay applied and the error surfaces joined at the end.

func TestRecordWorkerProcessorUpsertsThroughProjectedStore(t *testing.T) {
	base := database.NewMemoryDB()
	projected, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	processor, err := NewRecordWorkerProcessor(projected, "menu_projection", "hotplane", func(_ context.Context, job worker.Job) ([]database.DomainRecord, error) {
		return []database.DomainRecord{{
			Domain:         "menu",
			Collection:     "dishes",
			OrganizationID: "org_1",
			RecordID:       job.IdempotencyKey,
			Data:           testRecordData(map[string]any{"state": "published"}),
		}}, nil
	})
	if err != nil {
		t.Fatalf("NewRecordWorkerProcessor() error = %v", err)
	}
	if processor.Kind() != "menu_projection" || processor.Queue() != "hotplane" || processor.MaxAttempts() != 3 {
		t.Fatalf("processor identity = %s/%s/%d", processor.Kind(), processor.Queue(), processor.MaxAttempts())
	}

	if err := processor.Handle(ctx, worker.Job{IdempotencyKey: "dish_1"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	// Durable side: the base store holds the record (rebuildable after restart).
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_1"); err != nil || !found {
		t.Fatalf("base GetRecord() found=%v err=%v, want durable record", found, err)
	}
	// Hot side: the partition the gateway reads serves it without the base.
	projection := projected.ProjectionName("menu", "dishes", "org_1")
	count, err := projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("hot Count() = %d err=%v, want 1", count, err)
	}
	// Idempotent replay (River retry) stays a single record.
	if err := processor.Handle(ctx, worker.Job{IdempotencyKey: "dish_1"}); err != nil {
		t.Fatalf("replay Handle() error = %v", err)
	}
	if count, _ = projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{}); count != 1 {
		t.Fatalf("count after replay = %d, want 1", count)
	}
}

// TestScopeBackfillMakesWarmSelfSufficient proves the self-backfilling warm:
// with an empty record mirror and a configured ScopeBackfill enumerator, a
// WarmScope pulls the scope's rows from the authoritative source through the
// batch lane, so eagerly configured warm scopes work on first boot with no
// separate backfill step. A non-empty mirror never re-triggers the backfill.

func TestCountIndexedDoesNotAllocate(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "count_alloc", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"bucket"}, MaxRecords: 1024, MaxBytes: 8 << 20,
	})
	records := make([]database.DomainRecord, 256)
	for i := range records {
		records[i] = testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%03d", i), map[string]any{"bucket": i % 8})
	}
	if _, err := store.BulkLoad(t.Context(), "count_alloc", records); err != nil {
		t.Fatalf("BulkLoad: %v", err)
	}
	query := QueryWithFilters("org_1", 0, mustFilter(t, "bucket", 7))
	if got := testing.AllocsPerRun(100, func() {
		count, countErr := store.Count(context.Background(), "count_alloc", query, Fence{})
		if countErr != nil || count != 32 {
			t.Fatalf("Count = %d, %v; want 32, nil", count, countErr)
		}
	}); got != 0 {
		t.Fatalf("Count allocations = %g, want 0", got)
	}
}
