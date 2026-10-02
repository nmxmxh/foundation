package hermes

import (
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// TestApplyBatchSkipsEventLevelRejections pins the batch-tolerance contract:
// an event-level rejection (invalid scope, capacity) poisons only the
// offending event. The rest of the batch still applies, accepted mutations
// still fan out to observers, and the rejection surfaces as a joined error so
// single-event Apply callers keep their pinned ErrInvalidEvent /
// ErrProjectionLimit contract. Before this, one bad event aborted the batch
// and suppressed fan-out of everything applied before it.
func TestApplyBatchSkipsEventLevelRejections(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "tolerant_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 2,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()

	var accepted []AppliedMutation
	cancel := store.Observe(func(_ string, mutations []AppliedMutation) {
		accepted = append(accepted, mutations...)
	})
	defer cancel()

	result, err := store.ApplyBatch(ctx, "tolerant_ticks", []Event{
		// invalid scope: rejected, must not halt the batch
		{Operation: OperationUpsert, SourceID: "bad_scope", Version: 1,
			Record: database.DomainRecord{Domain: "other", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_bad"}},
		// two valid events fill the partition
		{Operation: OperationUpsert, SourceID: "ok_1", Version: 2, Record: testRecord("signals", "ticks", "org_1", "tick_1", nil)},
		{Operation: OperationUpsert, SourceID: "ok_2", Version: 3, Record: testRecord("signals", "ticks", "org_1", "tick_2", nil)},
		// over capacity: rejected, must not halt the batch either
		{Operation: OperationUpsert, SourceID: "over", Version: 4, Record: testRecord("signals", "ticks", "org_1", "tick_3", nil)},
	})
	if !errors.Is(err, ErrInvalidEvent) || !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("err = %v, want joined ErrInvalidEvent + ErrProjectionLimit", err)
	}
	if result.Applied != 2 || result.Ignored != 2 {
		t.Fatalf("result = %+v, want Applied=2 Ignored=2", result)
	}
	if len(accepted) != 2 {
		t.Fatalf("observer saw %d mutations, want 2 (accepted events must fan out despite batch error)", len(accepted))
	}
	count, err := store.Count(ctx, "tolerant_ticks", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d err=%v, want 2", count, err)
	}
}

// TestTailerQuarantinesPoisonMessage proves a message whose decode fails is
// quarantined (acked, dropped, counted) instead of halting the tail loop:
// healthy messages in the same batch still apply, and the poison message is
// not redelivered on the next poll. Before this, one poison message failed
// PollOnce before any apply, nothing was acked, and Run redelivered it
// forever.

func TestApplyRecordsSkipsEventLevelRejections(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "tolerant_records",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 1,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()
	result, err := store.ApplyRecords(ctx, "tolerant_records", "state", 1, []database.DomainRecord{
		testRecord("signals", "ticks", "org_1", "tick_1", nil),
		testRecord("signals", "ticks", "org_1", "tick_2", nil), // over capacity
	})
	if !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("err = %v, want joined ErrProjectionLimit", err)
	}
	if result.Applied != 1 || result.Ignored != 1 {
		t.Fatalf("result = %+v, want Applied=1 Ignored=1 (overflow must not poison the batch)", result)
	}
	count, err := store.Count(ctx, "tolerant_records", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v, want 1", count, err)
	}
}

// TestSnapshotShadowEvidenceCycle drives the shadow-mode snapshot rollout
// across three simulated process generations sharing one durable base and one
// snapshot store:
//
//	gen 1: cold warm, no artifact yet → rebuild serves, artifact saved.
//	gen 2: cold warm, artifact matches the rebuild → clean-match evidence.
//	gen 3: base mutated out-of-band since the artifact → mismatch evidence,
//	       artifact refreshed to the new truth.
//
// Throughout, the served warm path is the source rebuild — the shadow lane
// only accumulates the evidence counters that gate ever preferring snapshots.

func mustFilter(t *testing.T, field string, value any) QueryFilter {
	t.Helper()
	f, ok := NewQueryFilter(field, value)
	if !ok {
		t.Fatalf("NewQueryFilter(%q,%v) not ok", field, value)
	}
	return f
}
