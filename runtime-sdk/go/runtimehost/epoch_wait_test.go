//go:build linux || darwin

package runtimehost

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// measureParkChurn returns TotalAlloc growth across one parked wait of the
// given window with a silent doorbell and a healthy peer — i.e. every wake in
// the window is the periodic-fallback path.
func measureParkChurn(t *testing.T, window time.Duration) uint64 {
	t.Helper()
	var slot uint32
	policy := epochWaitPolicy{
		spinIterations: 0,
		maxSleep:       time.Millisecond,
		timeout:        window,
	}
	doorbell := make(chan struct{}) // never signaled
	time.AfterFunc(window-10*time.Millisecond, func() { atomic.StoreUint32(&slot, 9) })

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := waitForEpochChange(&slot, 0, policy, func() bool { return true }, doorbell); err != nil {
		t.Fatalf("waitForEpochChange(%v): %v", window, err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestWaitForEpochChangeFallbackIsAllocationStable is the regression guard for
// the timer-leak fix: the periodic wake must reuse one timer, not allocate a
// fresh `time.After` per park.
//
// The guard measures the SLOPE of allocations against parked duration rather
// than an absolute ceiling — fixed per-call costs (timers, closures, scheduler
// noise) are identical between the two windows and cancel out, while the old
// bug added roughly one timer allocation per 5ms of parking (~96 B/wake, about
// 2.9 KB across the 150 ms delta below).
func TestWaitForEpochChangeFallbackIsAllocationStable(t *testing.T) {
	if raceDetectorEnabled {
		// The detector's own allocations sit inside this budget and swamp it;
		// the budget still guards every non-race build.
		t.Skip("allocation budget is not meaningful under -race")
	}
	const (
		shortWindow = 25 * time.Millisecond
		longWindow  = 175 * time.Millisecond
		// ~30 extra fallback wakes; a returned `time.After` allocates far
		// more than this budget across them.
		wakeBudgetBytes = 512
	)

	runtime.GC()
	short := measureParkChurn(t, shortWindow)
	long := measureParkChurn(t, longWindow)

	delta := max(int64(long)-int64(short),
		// Scheduler noise can flip sign at these magnitudes; only growth
		// indicates a per-wake allocator.
		0)
	if delta > wakeBudgetBytes {
		t.Fatalf(
			"parked wait allocated %dB more over the +%v window (~%d fallback wakes); per-park allocation likely returned to waitForEpochChange",
			delta, longWindow-shortWindow, int((longWindow-shortWindow)/(5*time.Millisecond)),
		)
	}
}

func TestWaitForEpochChangeReturnsWhenTheSlotChangesWhileParked(t *testing.T) {
	var slot uint32
	policy := epochWaitPolicy{
		spinIterations: 0,
		maxSleep:       time.Millisecond,
		timeout:        2 * time.Second,
	}
	doorbell := make(chan struct{}) // parked wait must still observe the store

	time.AfterFunc(5*time.Millisecond, func() { atomic.StoreUint32(&slot, 7) })

	current, err := waitForEpochChange(&slot, 0, policy, func() bool { return true }, doorbell)
	if err != nil {
		t.Fatalf("waitForEpochChange: %v", err)
	}
	if current != 7 {
		t.Fatalf("observed = %d want 7", current)
	}
}

func TestWaitForEpochChangeTimeoutFiresWithoutProgress(t *testing.T) {
	slot := uint32(3) // Never changes.
	policy := epochWaitPolicy{
		spinIterations: 0,
		maxSleep:       time.Millisecond,
		timeout:        25 * time.Millisecond,
	}
	doorbell := make(chan struct{})

	current, err := waitForEpochChange(&slot, 3, policy, func() bool { return true }, doorbell)
	if !errors.Is(err, errEpochTimeout) {
		t.Fatalf("err = %v want errEpochTimeout", err)
	}
	if current != 3 {
		t.Fatalf("current = %d want unchanged 3", current)
	}
}

// TestWaitForEpochChangeDoorbellCloseIsPeerLost pins the closed-doorbell arm:
// a closed channel means the peer is gone and must end the wait immediately
// with errEpochPeerLost rather than riding the fallback ladder to timeout.
func TestWaitForEpochChangeDoorbellCloseIsPeerLost(t *testing.T) {
	slot := uint32(1)
	policy := epochWaitPolicy{spinIterations: 0, maxSleep: time.Millisecond, timeout: 5 * time.Second}
	closed := make(chan struct{})
	close(closed)

	current, err := waitForEpochChange(&slot, 1, policy, func() bool { return true }, closed)
	if !errors.Is(err, errEpochPeerLost) {
		t.Fatalf("err = %v want errEpochPeerLost", err)
	}
	if current != 1 {
		t.Fatalf("current = %d want unchanged", current)
	}
}
