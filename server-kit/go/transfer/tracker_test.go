package transfer

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestTracker(total int64) *Tracker {
	return newTracker("tx-1", "corr-1", total, fixedClock())
}

func TestTracker_InitialSnapshot(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	snap := tr.Snapshot()
	if snap.Phase != PhasePending {
		t.Errorf("phase=%s want pending", snap.Phase)
	}
	if snap.BytesTotal != 100 || snap.BytesDone != 0 {
		t.Errorf("bytes=(%d/%d) want (0/100)", snap.BytesDone, snap.BytesTotal)
	}
	if snap.TransferID != "tx-1" || snap.CorrelationID != "corr-1" {
		t.Errorf("ids=(%q,%q)", snap.TransferID, snap.CorrelationID)
	}
	if snap.Seq != 0 {
		t.Errorf("initial seq=%d want 0", snap.Seq)
	}
}

func TestTracker_Identifiers(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(10)
	if tr.ID() != "tx-1" || tr.CorrelationID() != "corr-1" {
		t.Errorf("ids=(%q,%q) want (tx-1,corr-1)", tr.ID(), tr.CorrelationID())
	}
}

func TestNewTracker_Public(t *testing.T) {
	t.Parallel()
	tr, err := NewTracker(" tx-1 ", "corr-1", 100)
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	snap := tr.Snapshot()
	if snap.TransferID != "tx-1" || snap.CorrelationID != "corr-1" {
		t.Errorf("ids trimmed wrong: %+v", snap)
	}
	if snap.Phase != PhasePending || snap.BytesTotal != 100 {
		t.Errorf("unexpected initial snapshot: %+v", snap)
	}
	if _, err := NewTracker("", "corr", 0); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id=%v want ErrEmptyID", err)
	}
	if _, err := NewTracker("tx", "  ", 0); !errors.Is(err, ErrEmptyCorrelation) {
		t.Errorf("empty correlation=%v want ErrEmptyCorrelation", err)
	}
	if _, err := NewTracker("bad:id", "corr", 0); err == nil {
		t.Error("colon in id must be rejected")
	}
}

func TestTracker_NewTrackerClampsNegativeTotal(t *testing.T) {
	t.Parallel()
	tr := newTracker("tx", "corr", -5, fixedClock())
	if got := tr.Snapshot().BytesTotal; got != 0 {
		t.Errorf("BytesTotal=%d want 0", got)
	}
}

func TestTracker_AdvanceMovesToUploadingAndIsMonotonic(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)

	snap, err := tr.Advance(30)
	if err != nil {
		t.Fatalf("Advance(30): %v", err)
	}
	if snap.Phase != PhaseUploading {
		t.Errorf("phase=%s want uploading", snap.Phase)
	}
	if snap.BytesDone != 30 || snap.Seq != 1 {
		t.Errorf("after 30: bytes=%d seq=%d want 30/1", snap.BytesDone, snap.Seq)
	}

	// A lower/equal offset coalesces to a no-op: no seq bump, no regression.
	for _, lower := range []int64{30, 10, 0} {
		s, err := tr.Advance(lower)
		if err != nil {
			t.Fatalf("Advance(%d): %v", lower, err)
		}
		if s.BytesDone != 30 || s.Seq != 1 {
			t.Errorf("Advance(%d) regressed to bytes=%d seq=%d", lower, s.BytesDone, s.Seq)
		}
	}

	s, err := tr.Advance(80)
	if err != nil || s.BytesDone != 80 || s.Seq != 2 {
		t.Errorf("Advance(80): (%+v,%v)", s, err)
	}
}

func TestTracker_AdvanceRejectsNegative(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	if _, err := tr.Advance(-1); !errors.Is(err, ErrNegativeBytes) {
		t.Errorf("Advance(-1)=%v want ErrNegativeBytes", err)
	}
}

func TestTracker_AdvanceBeyondTotalRaisesTotal(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(50)
	snap, err := tr.Advance(70)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if snap.BytesTotal != 70 {
		t.Errorf("BytesTotal=%d want raised to 70", snap.BytesTotal)
	}
	if snap.Fraction() != 1 {
		t.Errorf("Fraction=%v want 1", snap.Fraction())
	}
}

func TestTracker_AdvanceWithUnknownTotal(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(0)
	snap, _ := tr.Advance(123)
	if snap.BytesTotal != 0 {
		t.Errorf("unknown total should stay 0, got %d", snap.BytesTotal)
	}
	if snap.Fraction() != 0 {
		t.Errorf("Fraction with unknown total=%v want 0", snap.Fraction())
	}
}

func TestTracker_SetTotal(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(0)
	if _, changed := tr.SetTotal(0); changed {
		t.Error("SetTotal(0) should be ignored")
	}
	snap, changed := tr.SetTotal(200)
	if !changed || snap.BytesTotal != 200 {
		t.Errorf("SetTotal(200): changed=%v total=%d", changed, snap.BytesTotal)
	}
	// Cannot set total below bytes already observed.
	mustAdvance(t, tr, 150)
	if _, changed := tr.SetTotal(100); changed {
		t.Error("SetTotal below observed bytes must be ignored")
	}
}

func TestTracker_TransitionHappyPath(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	mustAdvance(t, tr, 100)

	for _, p := range []Phase{PhaseStaged, PhaseProcessing} {
		if _, err := tr.Transition(p); err != nil {
			t.Fatalf("Transition(%s): %v", p, err)
		}
	}
	snap, err := tr.Transition(PhaseReady, WithChecksum("abc123"))
	if err != nil {
		t.Fatalf("Transition(ready): %v", err)
	}
	if snap.Phase != PhaseReady || snap.Checksum != "abc123" {
		t.Errorf("ready snap=%+v", snap)
	}
	if !tr.IsTerminal() {
		t.Error("tracker should be terminal after ready")
	}
}

func TestTracker_TransitionReadySnapsBytesToTotal(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	mustAdvance(t, tr, 40)
	mustTransition(t, tr, PhaseStaged)
	mustTransition(t, tr, PhaseProcessing)
	snap := mustTransition(t, tr, PhaseReady)
	if snap.BytesDone != 100 {
		t.Errorf("Ready should snap bytes to total: got %d want 100", snap.BytesDone)
	}
	if snap.Fraction() != 1 {
		t.Errorf("Fraction=%v want 1", snap.Fraction())
	}
}

func TestTracker_TransitionIllegalRejected(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	if _, err := tr.Transition(PhaseReady); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("pending->ready=%v want ErrInvalidTransition", err)
	}
}

func TestTracker_TransitionTerminalRejected(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	mustTransition(t, tr, PhaseFailed)
	if _, err := tr.Advance(10); !errors.Is(err, ErrTerminal) {
		t.Errorf("Advance after terminal=%v want ErrTerminal", err)
	}
	if _, err := tr.Transition(PhaseAborted); !errors.Is(err, ErrTerminal) {
		t.Errorf("Transition after terminal=%v want ErrTerminal", err)
	}
}

func TestTracker_FailFromPendingCarriesReason(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	snap, err := tr.Transition(PhaseFailed, WithReason("disk full"))
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if snap.Phase != PhaseFailed || snap.Reason != "disk full" {
		t.Errorf("fail snap=%+v", snap)
	}
}

func TestTracker_SubscribeDeliversCurrentThenUpdates(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(100)
	mustAdvance(t, tr, 25)

	var mu sync.Mutex
	var got []Update
	cancel := tr.Subscribe(func(u Update) {
		mu.Lock()
		got = append(got, u)
		mu.Unlock()
	})

	mustAdvance(t, tr, 50)
	cancel()
	mustAdvance(t, tr, 75) // after cancel, should not be delivered

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d updates want 2 (initial snapshot + one advance): %+v", len(got), got)
	}
	if got[0].BytesDone != 25 {
		t.Errorf("first delivery should be current snapshot (25), got %d", got[0].BytesDone)
	}
	if got[1].BytesDone != 50 {
		t.Errorf("second delivery=%d want 50", got[1].BytesDone)
	}
}

func TestTracker_SubscribeNilAndDoubleCancel(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(10)
	cancel := tr.Subscribe(nil)
	cancel() // must not panic
	c := tr.Subscribe(func(Update) {})
	c()
	c() // idempotent cancel
}

// TestTracker_ConcurrentAdvance asserts monotonicity and no lost terminal under
// concurrent producers (race detector exercises this in CI).
func TestTracker_ConcurrentAdvance(t *testing.T) {
	t.Parallel()
	tr := newTestTracker(1000)

	var maxSeen atomic.Int64
	var wg sync.WaitGroup
	for i := int64(1); i <= 100; i++ {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			offset := n * 10
			snap, err := tr.Advance(offset)
			if err != nil {
				t.Errorf("Advance(%d): %v", offset, err)
				return
			}
			for {
				cur := maxSeen.Load()
				if snap.BytesDone <= cur || maxSeen.CompareAndSwap(cur, snap.BytesDone) {
					break
				}
			}
		}(i)
	}
	wg.Wait()

	final := tr.Snapshot()
	if final.BytesDone != 1000 {
		t.Errorf("final bytes=%d want 1000", final.BytesDone)
	}
	if final.Phase != PhaseUploading {
		t.Errorf("final phase=%s want uploading", final.Phase)
	}
}

func mustAdvance(t *testing.T, tr *Tracker, n int64) Update {
	t.Helper()
	snap, err := tr.Advance(n)
	if err != nil {
		t.Fatalf("Advance(%d): %v", n, err)
	}
	return snap
}

func mustTransition(t *testing.T, tr *Tracker, p Phase) Update {
	t.Helper()
	snap, err := tr.Transition(p)
	if err != nil {
		t.Fatalf("Transition(%s): %v", p, err)
	}
	return snap
}
