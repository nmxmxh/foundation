package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestStoreCheckDriftConsistentMerkle(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 6)
	store := newDriftStore(t, 16)
	if _, err := store.Rebuild(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 16, SampleSize: 3})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("drift report should be OK: %+v", report)
	}
	if report.SourceCount != 6 || report.HermesCount != 6 || report.SourceRoot == "" || report.SourceRoot != report.HermesRoot {
		t.Fatalf("unexpected report counts/root: %+v", report)
	}
	if len(report.Samples) != 3 {
		t.Fatalf("samples = %d, want 3", len(report.Samples))
	}
	for _, sample := range report.Samples {
		if !sample.Match || sample.SourceWitness.LeafHash == "" || len(sample.SourceWitness.Siblings) == 0 {
			t.Fatalf("bad sample witness: %+v", sample)
		}
	}
}

func TestStoreCheckDriftTreatsRawJSONSemantically(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	_, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_raw", map[string]any{
		"bucket":   1,
		"metadata": []byte(`{"trace_id":"abc","actor_id":"u1","labels":["a","b"]}`),
	}))
	if err != nil {
		t.Fatalf("source upsert failed: %v", err)
	}
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_raw", 1, map[string]any{
		"bucket":   1,
		"metadata": []byte(`{"actor_id":"u1","labels":["a","b"],"trace_id":"abc"}`),
	})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 1})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("semantically equivalent raw JSON should not drift: %+v", report)
	}
}

func TestStoreCheckDriftDetectsHashMismatch(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	_, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 1, "value": "source"}))
	if err != nil {
		t.Fatalf("source upsert failed: %v", err)
	}
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_1", 1, map[string]any{"bucket": 1, "value": "hot"})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 4})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() || len(report.Mismatches) != 1 || report.Mismatches[0].Reason != "hash_mismatch" {
		t.Fatalf("expected one hash mismatch, got %+v", report)
	}
}

func TestStoreCheckDriftDetectsMissingHermesRecord(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 2)
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_0", 1, map[string]any{"bucket": 0, "value": "tick_0"})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 4})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() {
		t.Fatalf("expected drift, got OK report: %+v", report)
	}
	if !hasDriftReason(report.Mismatches, "count_mismatch") || !hasDriftReason(report.Mismatches, "missing_hermes") {
		t.Fatalf("expected count and missing_hermes mismatch, got %+v", report.Mismatches)
	}
}

func TestStoreCheckDriftMarksBoundedTruncation(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 3)
	store := newDriftStore(t, 2)
	if _, err := store.Rebuild(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 2, SampleSize: 2})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() || report.Complete || !report.Truncated || !hasDriftReason(report.Mismatches, "bounded_sample_truncated") {
		t.Fatalf("expected bounded truncation report, got %+v", report)
	}
}

func newDriftStore(t *testing.T, maxRecords int) *Store {
	t.Helper()
	return newTestStore(t, ProjectionSpec{
		Name:          "drift_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    maxRecords,
		MaxBytes:      1 << 20,
	})
}

func seedDriftSource(t *testing.T, ctx context.Context, source database.StateStore, count int) {
	t.Helper()
	for i := range count {
		id := fmt.Sprintf("tick_%d", i)
		if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", id, map[string]any{
			"bucket": i % 2,
			"value":  id,
		})); err != nil {
			t.Fatalf("source upsert[%d] failed: %v", i, err)
		}
	}
}

func hasDriftReason(mismatches []DriftMismatch, reason string) bool {
	for _, mismatch := range mismatches {
		if mismatch.Reason == reason {
			return true
		}
	}
	return false
}
