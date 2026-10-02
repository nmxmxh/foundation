package placement

import (
	"context"
	"errors"
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

func TestPublishLaneMirrorsWrapsTransportErrors(t *testing.T) {
	err := PublishLaneMirrors(context.Background(), &failingPublishClient{rediskit.NewMemoryClient("p")},
		"", "n", "r", LaneClassEdge, []LaneMirrorUpdate{{Lane: 1}})
	if !errors.Is(err, errTransport) {
		t.Fatalf("err = %v want transport wrap", err)
	}
}

func TestListenMirrorsWrapsSubscribeErrors(t *testing.T) {
	stop, err := ListenMirrors(context.Background(), &failingSubscribeClient{rediskit.NewMemoryClient("p")},
		"", &collectingSink{}, nil)
	if stop != nil {
		t.Fatal("a failed subscribe must not hand back a stop function")
	}
	if !errors.Is(err, errSubscribe) {
		t.Fatalf("err = %v want subscribe wrap", err)
	}
}

// TestListenMirrorsLifecycleBranches covers the remaining listener arms:
// nil error callbacks on both decode and apply paths, ctx cancellation, and
// the subscription-closed exit.

func TestListenMirrorsLifecycleBranches(t *testing.T) {
	bus := rediskit.NewMemoryClient("placement")
	sink := &collectingSink{}
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := ListenMirrors(ctx, bus, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()

	// Invalid frame with a NIL onError must be skipped silently.
	if err := bus.Publish(ctx, "", []byte("garbage")); err != nil {
		t.Fatalf("publish garbage: %v", err)
	}
	want := LaneMirrorUpdate{Lane: 7, EwmaNs: 2_000, TickSeen: 3}
	if err := PublishLaneMirrors(ctx, bus, "", "n", "r", LaneClassEdge, []LaneMirrorUpdate{want}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return sink.updates() == 1 })

	cancel()
	_ = bus.Close() // closes subscriber channels: exercises the !ok exit
	time.Sleep(5 * time.Millisecond)
}

// TestRemoteComputeHandlerNilSchemaAndEmptyResult covers the remaining
// handler arms: nil-ish schema passthrough and an executor returning empty
// bytes must still produce a well-formed success frame.

func TestListenMirrorsDecodeErrorCallbackAndClosedExit(t *testing.T) {
	base := rediskit.NewMemoryClient("placement")
	frames := make(chan []byte, 4)
	client := &controlledChannelClient{Client: base, frames: frames}
	sink := &collectingSink{}

	errs := make(chan error, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := ListenMirrors(ctx, client, "", sink, func(_ int, cause error) { errs <- cause })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()

	// Decode failure surfaces through the non-nil callback...
	frames <- []byte("junk")
	select {
	case <-errs:
	case <-time.After(2 * time.Second):
		t.Fatal("decode error never surfaced")
	}

	// ...and closing the stream ends the listener through the !ok arm.
	close(frames)
	time.Sleep(5 * time.Millisecond)

	// ctx cancellation while the stream is already closed must exit cleanly
	// too (double-exit race arm).
	cancel()
	time.Sleep(5 * time.Millisecond)
}

func TestListenMirrorsApplyErrorWithNilCallbackIsSilent(t *testing.T) {
	base := rediskit.NewMemoryClient("placement")
	frames := make(chan []byte, 4)
	client := &controlledChannelClient{Client: base, frames: frames}
	sink := &collectingSink{}
	ctx := t.Context()
	stop, err := ListenMirrors(ctx, client, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()
	// Sub-floor claim with a NIL onError: skipped silently, no panic.
	frames <- []byte{MirrorWireVersion, 0, 1, 'n', 1, 'r', 0, 1, 0, 0, 0, 0, 0, 0, 0, byte(LaneClassEdge)}
	frames <- func() []byte {
		frame, _ := EncodeLaneMirrorFrame("liar", "r", LaneClassEdge,
			[]LaneMirrorUpdate{{Lane: 2, EwmaNs: MinPlausibleEwmaNs - 3}})
		return frame
	}()
	time.Sleep(20 * time.Millisecond)
	if sink.updates() != 0 {
		t.Fatal("rejected update reached sink")
	}
}
