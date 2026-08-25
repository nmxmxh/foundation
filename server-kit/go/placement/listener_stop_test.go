package placement

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// TE-20 regression for a defect repaired on 2026-08-25.
//
// ListenMirrors returned only an error. The listener ran on a goroutine the
// caller had no handle on, so cancelling ctx merely *signalled* it: a caller
// that cancelled and then released whatever the sink borrowed raced an
// ApplyMirrorUpdate already in flight. With runtimehost.DispatchBlock as the
// sink that is not a benign race — the block hands out pointers into an mmap'd
// region and Close munmaps it, so the in-flight apply reads pages that no
// longer belong to the process. The race detector caught it as a write/read
// pair on DispatchBlock.raw; in production it is a segfault or silent
// corruption, whichever the allocator arranges.
//
// The repair returns a stop function that cancels and then joins. These tests
// pin the join, because cancelling alone always looked correct.

// blockingSink holds each apply open long enough that a stop racing it is
// observable rather than a matter of luck.
type blockingSink struct {
	entered  chan struct{}
	release  chan struct{}
	inFlight atomic.Int32
	finished atomic.Int32
	notified atomic.Bool
}

func (b *blockingSink) ApplyMirrorUpdate(LaneMirrorUpdate) error {
	b.inFlight.Add(1)
	if b.notified.CompareAndSwap(false, true) {
		close(b.entered)
	}
	<-b.release
	b.inFlight.Add(-1)
	b.finished.Add(1)
	return nil
}

// TestListenMirrorsStopJoinsInFlightApply is the core guarantee: when stop
// returns, no apply is still running. A stop that only cancelled would return
// while inFlight was still 1.
func TestListenMirrorsStopJoinsInFlightApply(t *testing.T) {
	bus := rediskit.NewMemoryClient("stopjoin")
	sink := &blockingSink{entered: make(chan struct{}), release: make(chan struct{})}

	stop, err := ListenMirrors(context.Background(), bus, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	if err := PublishLaneMirrors(context.Background(), bus, "", "node-1", "eu", LaneClassEdge,
		[]LaneMirrorUpdate{{Lane: 1, EwmaNs: 10_000, TickSeen: 1}}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait until the listener is genuinely inside the sink, so stop below has
	// something to join rather than an already-idle goroutine.
	select {
	case <-sink.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never entered the sink")
	}
	if got := sink.inFlight.Load(); got != 1 {
		t.Fatalf("in-flight applies = %d, want 1 before stop", got)
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()

	// stop must not return while the apply is held open.
	select {
	case <-stopped:
		t.Fatal("stop returned while an apply was still in flight; it cancelled without joining")
	case <-time.After(150 * time.Millisecond):
	}

	close(sink.release)

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop never returned after the apply completed")
	}

	if got := sink.inFlight.Load(); got != 0 {
		t.Fatalf("in-flight applies = %d after stop returned, want 0", got)
	}
	if got := sink.finished.Load(); got != 1 {
		t.Fatalf("finished applies = %d, want 1", got)
	}
}

// countingSink records applies without blocking.
type countingSink struct{ applied atomic.Int32 }

func (c *countingSink) ApplyMirrorUpdate(LaneMirrorUpdate) error {
	c.applied.Add(1)
	return nil
}

// TestListenMirrorsStopEndsFurtherApplies pins the other half: once stop has
// returned, later traffic on the channel must not reach the sink at all. This
// is what makes it safe to release the sink's backing memory afterwards.
func TestListenMirrorsStopEndsFurtherApplies(t *testing.T) {
	bus := rediskit.NewMemoryClient("stopend")
	sink := &countingSink{}

	stop, err := ListenMirrors(context.Background(), bus, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	if err := PublishLaneMirrors(context.Background(), bus, "", "node-1", "eu", LaneClassEdge,
		[]LaneMirrorUpdate{{Lane: 1, EwmaNs: 10_000, TickSeen: 1}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return sink.applied.Load() == 1 })

	stop()
	settled := sink.applied.Load()

	for i := range 5 {
		if err := PublishLaneMirrors(context.Background(), bus, "", "node-1", "eu", LaneClassEdge,
			[]LaneMirrorUpdate{{Lane: 2, EwmaNs: 20_000, TickSeen: uint64(i + 2)}}); err != nil {
			// A closed subscription may refuse the publish outright, which is
			// an equally good outcome: nothing reached the sink.
			break
		}
	}
	time.Sleep(50 * time.Millisecond)

	if got := sink.applied.Load(); got != settled {
		t.Fatalf("sink applied %d updates after stop returned (was %d); the listener outlived its stop", got, settled)
	}
}

// TestListenMirrorsStopIsIdempotent covers the ordinary caller shape, where a
// deferred stop can run after an explicit one on an error path.
func TestListenMirrorsStopIsIdempotent(t *testing.T) {
	bus := rediskit.NewMemoryClient("stoptwice")
	stop, err := ListenMirrors(context.Background(), bus, "", &countingSink{}, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stop()
	stop()
	stop()
}

// TestListenMirrorsStopAfterParentCancelDoesNotHang pins that stop stays safe
// when the parent context ended first, which is the path a caller takes when
// the surrounding request is cancelled.
func TestListenMirrorsStopAfterParentCancelDoesNotHang(t *testing.T) {
	bus := rediskit.NewMemoryClient("stopcancel")
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := ListenMirrors(ctx, bus, "", &countingSink{}, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cancel()

	returned := make(chan struct{})
	go func() { stop(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("stop hung after the parent context was already cancelled")
	}
}
