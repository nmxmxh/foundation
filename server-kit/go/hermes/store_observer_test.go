package hermes

import (
	"sync"
	"testing"
)

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
