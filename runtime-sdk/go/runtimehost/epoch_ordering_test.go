//go:build linux || darwin

package runtimehost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func epochTestSegment(t *testing.T) *sharedMemorySegment {
	t.Helper()
	if !sharedMemorySupported("") {
		t.Skip("shared memory transport is not supported on this runtime")
	}
	segment, err := newSharedMemorySegment("", int(generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		t.Fatalf("newSharedMemorySegment() error = %v", err)
	}
	t.Cleanup(func() { _ = segment.Close() })
	return segment
}

// The property the pipe used to provide for free, and the one this design can
// lose: a reader must never see a payload that is only partly written.
//
// A pipe read is a full barrier, so under the old doorbell a torn read was not
// expressible. Here it is prevented by one release store and one acquire load,
// and nothing else. This stamps every byte of the payload with a generation
// number before publishing and checks the reader observes exactly that
// generation across the whole region.
//
// Strictly ping-pong, because the protocol is: the host does not stage a second
// exchange until the first has been answered. A free-running writer tears the
// payload no matter how the epochs are ordered, which measures overwrite rather
// than visibility — the first version of this test made that mistake and caught
// itself.
//
// Deliberately over a heap buffer rather than the shared segment, and that is
// the whole reason this test can fail. Go's race detector does not instrument
// memory obtained from syscall.Mmap — the runtime does not know the region
// exists — so an ordering test run against a real mapping passes under -race
// whatever you do to it. That was the first version of this test, and replacing
// observeEpoch's atomic load with a plain deref did not fail it. The epoch
// helpers do not care where their bytes came from, so exercising them over a
// heap slice tests the property and keeps the detector's coverage.
//
// Run under -race, this fails if the atomic in observeEpoch or publishEpoch is
// removed. Independently of -race, it fails if the publish is moved above the
// payload write or the payload read above the wait: the generation assertion
// sees the wrong round.
//
// TestEpochExchangeRoundTripsThroughASharedMapping covers the same protocol
// over a real mapping, where the detector cannot follow.
func TestEpochOrderingPublishesThePayloadBeforeTheEpoch(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)

	inputSlot, err := epochSlot(raw, generated.IDX_INPUT_WRITTEN)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}
	ackSlot, err := epochSlot(raw, generated.IDX_OUTPUT_CONSUMED)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}

	// The whole input region, not a word: a torn read is only observable across
	// a span wide enough for the writer to be interrupted inside it.
	payload := raw[generated.OFFSET_INPUT_BYTES : generated.OFFSET_INPUT_BYTES+generated.INPUT_MAX_BYTES]
	const rounds = 400
	policy := epochWaitPolicy{spinIterations: 20000, maxSleep: time.Millisecond, timeout: 10 * time.Second}

	var writer sync.WaitGroup
	writer.Go(func() {
		lastAck := observeEpoch(ackSlot)
		for round := 1; round <= rounds; round++ {
			generation := byte(round)
			for i := range payload {
				payload[i] = generation
			}
			publishEpoch(inputSlot)

			next, err := waitForEpochChange(ackSlot, lastAck, policy, nil, nil)
			if err != nil {
				return
			}
			lastAck = next
		}
	})

	observed := observeEpoch(inputSlot)
	for round := 1; round <= rounds; round++ {
		next, err := waitForEpochChange(inputSlot, observed, policy, nil, nil)
		if err != nil {
			t.Fatalf("round %d: waitForEpochChange() error = %v", round, err)
		}
		observed = next

		want := byte(round)
		for i, value := range payload {
			if value != want {
				t.Fatalf("round %d: byte %d is generation %d, want %d — the epoch was published before the payload was complete",
					round, i, value, want)
			}
		}
		publishEpoch(ackSlot)
	}
	writer.Wait()
}

// A wait must end when the kernel dies, not when the timeout does.
//
// This is the liveness hole the pipe covered: a dead kernel closed stdout and
// the blocking read failed immediately. With no read in the hot path a dead
// kernel and a slow one produce the same silent slot, so the wait consults the
// supervisor instead.
func TestEpochWaitEndsWhenThePeerIsGoneRatherThanOnTimeout(t *testing.T) {
	segment := epochTestSegment(t)
	slot, err := epochSlot(segment.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}

	policy := epochWaitPolicy{spinIterations: 4, maxSleep: 50 * time.Microsecond, timeout: time.Hour}
	started := time.Now()
	if _, err := waitForEpochChange(slot, observeEpoch(slot), policy, func() bool { return false }, nil); !errors.Is(err, errEpochPeerLost) {
		t.Fatalf("waitForEpochChange() error = %v, want errEpochPeerLost", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("wait took %v; it should end on the liveness check, not the timeout", elapsed)
	}
}

// A result published just before the kernel exited is still a result.
//
// The liveness check runs after the load for exactly this case — reporting the
// peer as lost here would discard an exchange that completed.
func TestEpochWaitReturnsAResultPublishedBeforeThePeerDied(t *testing.T) {
	segment := epochTestSegment(t)
	slot, err := epochSlot(segment.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}
	previous := observeEpoch(slot)
	publishEpoch(slot)

	policy := epochWaitPolicy{spinIterations: 0, maxSleep: time.Millisecond, timeout: time.Second}
	if _, err := waitForEpochChange(slot, previous, policy, func() bool { return false }, nil); err != nil {
		t.Fatalf("waitForEpochChange() discarded a completed exchange: %v", err)
	}
}

func TestEpochWaitTimesOutRatherThanHanging(t *testing.T) {
	segment := epochTestSegment(t)
	slot, err := epochSlot(segment.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}
	policy := epochWaitPolicy{spinIterations: 4, maxSleep: 50 * time.Microsecond, timeout: 20 * time.Millisecond}
	if _, err := waitForEpochChange(slot, observeEpoch(slot), policy, func() bool { return true }, nil); !errors.Is(err, errEpochTimeout) {
		t.Fatalf("waitForEpochChange() error = %v, want errEpochTimeout", err)
	}
}

// A waiter compares against what it last saw, never against an expected value.
// Epochs only move forward, so a consumer that missed an exchange must not
// block waiting for a number that has already gone past.
func TestEpochWaitDoesNotBlockOnAChangeItAlreadyMissed(t *testing.T) {
	segment := epochTestSegment(t)
	slot, err := epochSlot(segment.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}
	publishEpoch(slot)
	publishEpoch(slot)

	policy := epochWaitPolicy{spinIterations: 1, maxSleep: time.Millisecond, timeout: 50 * time.Millisecond}
	if _, err := waitForEpochChange(slot, 0, policy, func() bool { return true }, nil); err != nil {
		t.Fatalf("waitForEpochChange() blocked on a missed change: %v", err)
	}
}

// The route is fixed-width, so an id that does not fit has to be refused.
// Truncating one resolves to a different unit or to none, and both fail later
// and less clearly than an error naming the limit.
func TestWriteRouteRefusesAnIDThatDoesNotFit(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	oversized := strings.Repeat("u", int(generated.ROUTE_MAX_BYTES)+1)
	err := writeRoute(raw, oversized)
	if err == nil || !strings.Contains(err.Error(), "epoch route holds") {
		t.Fatalf("writeRoute() with an oversized id error = %v", err)
	}
	if err := writeRoute(raw, ""); err == nil {
		t.Fatal("writeRoute() must refuse an empty unit id")
	}
}

// A route must not carry anything of the previous exchange's.
func TestWriteRouteClearsThePreviousRoute(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	if err := writeRoute(raw, "runtime.a.very.long.unit.identifier"); err != nil {
		t.Fatalf("writeRoute() error = %v", err)
	}
	if err := writeRoute(raw, "runtime.b"); err != nil {
		t.Fatalf("writeRoute() error = %v", err)
	}
	region := raw[generated.OFFSET_ROUTE_BYTES : generated.OFFSET_ROUTE_BYTES+generated.ROUTE_MAX_BYTES]
	if got := string(region[:len("runtime.b")]); got != "runtime.b" {
		t.Fatalf("route = %q", got)
	}
	for i, value := range region[len("runtime.b"):] {
		if value != 0 {
			t.Fatalf("stale route byte %d = %d; a kernel would read the previous unit id",
				i+len("runtime.b"), value)
		}
	}
}

// A full exchange against a kernel that exists only as a goroutine over the
// same mapping. Proves the two halves of the protocol agree without needing a
// child process, which is what makes it runnable under -race.
func TestEpochExchangeRoundTripsThroughASharedMapping(t *testing.T) {
	segment := epochTestSegment(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go fakeEpochKernel(t, segment, stop, done)
	t.Cleanup(func() {
		close(stop)
		<-done
	})

	// Wait for the kernel exactly as a worker does. Publishing before it has
	// taken its baseline snapshot loses the exchange — see waitForKernelReady.
	// The first version of this test skipped the handshake and hung, which is
	// precisely the production symptom it now guards against.
	readyPolicy := epochWaitPolicy{spinIterations: 2000, maxSleep: time.Millisecond, timeout: 5 * time.Second}
	if err := waitForKernelReady(segment.raw, readyPolicy, func() bool { return true }, nil); err != nil {
		t.Fatalf("waitForKernelReady() error = %v", err)
	}

	exchange := epochExchange{
		shm:    segment,
		policy: epochWaitPolicy{spinIterations: 2000, maxSleep: time.Millisecond, timeout: 5 * time.Second},
		alive:  func() bool { return true },
	}

	buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
	control, err := NewBuffer(buffer)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	control.Initialize(1)
	if err := control.SetInputBytes([]byte("epoch")); err != nil {
		t.Fatalf("SetInputBytes() error = %v", err)
	}

	if err := exchange.Exchange(context.Background(), "runtime.echo", buffer); err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	result, err := NewBuffer(buffer)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	output, err := result.OutputBytes()
	if err != nil {
		t.Fatalf("OutputBytes() error = %v", err)
	}
	if string(output) != "EPOCH" {
		t.Fatalf("output = %q, want %q", output, "EPOCH")
	}
}

// An exchange against a kernel that is already gone must fail, not wait out its
// timeout. This is the whole reason epochExchange carries a liveness function.
func TestEpochExchangeFailsFastWhenTheKernelIsGone(t *testing.T) {
	segment := epochTestSegment(t)
	exchange := epochExchange{
		shm:    segment,
		policy: epochWaitPolicy{spinIterations: 4, maxSleep: 50 * time.Microsecond, timeout: time.Hour},
		alive:  func() bool { return false },
	}

	started := time.Now()
	err := exchange.Exchange(context.Background(), "runtime.echo", make([]byte, generated.BUFFER_TOTAL_BYTES))
	if !errors.Is(err, errEpochPeerLost) {
		t.Fatalf("Exchange() error = %v, want errEpochPeerLost", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Exchange() took %v against a dead kernel", elapsed)
	}
}

// fakeEpochKernel mirrors serve_epoch_loop: observe the input epoch, read the
// route, answer in place, publish the output epoch.
func fakeEpochKernel(t *testing.T, segment *sharedMemorySegment, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	inputSlot, err := epochSlot(segment.raw, generated.IDX_INPUT_WRITTEN)
	if err != nil {
		t.Errorf("epochSlot() error = %v", err)
		return
	}
	outputSlot, err := epochSlot(segment.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		t.Errorf("epochSlot() error = %v", err)
		return
	}

	policy := epochWaitPolicy{spinIterations: 200, maxSleep: time.Millisecond, timeout: 50 * time.Millisecond}

	// Snapshot, then announce — the same order as serve_epoch_loop, and for the
	// same reason.
	observed := observeEpoch(inputSlot)
	readySlot, err := epochSlot(segment.raw, generated.IDX_KERNEL_READY)
	if err != nil {
		t.Errorf("epochSlot() error = %v", err)
		return
	}
	publishEpoch(readySlot)

	for {
		select {
		case <-stop:
			return
		default:
		}
		next, err := waitForEpochChange(inputSlot, observed, policy, func() bool { return true }, nil)
		if err != nil {
			continue
		}
		observed = next

		route := segment.raw[generated.OFFSET_ROUTE_BYTES : generated.OFFSET_ROUTE_BYTES+generated.ROUTE_MAX_BYTES]
		if length := indexOfZero(route); length == 0 {
			t.Errorf("kernel observed an exchange with no route")
			return
		}
		buffer, err := NewBuffer(segment.raw)
		if err != nil {
			t.Errorf("NewBuffer() error = %v", err)
			return
		}
		input, err := buffer.InputBytes()
		if err != nil {
			t.Errorf("InputBytes() error = %v", err)
			return
		}
		if err := buffer.SetOutputBytes([]byte(strings.ToUpper(string(input)))); err != nil {
			t.Errorf("SetOutputBytes() error = %v", err)
			return
		}
		publishEpoch(outputSlot)
	}
}

func indexOfZero(region []byte) int {
	for i, value := range region {
		if value == 0 {
			return i
		}
	}
	return len(region)
}

// A kernel that announces readiness before taking its baseline snapshot loses
// the first exchange, and only sometimes — the worst kind of intermittent.
//
// This reproduces that ordering directly rather than trusting the comment in
// serve_epoch_loop, because the two lanes have to agree and only one of them is
// Go.
func TestEpochReadyHandshakePrecedesTheFirstPublish(t *testing.T) {
	segment := epochTestSegment(t)
	readySlot, err := epochSlot(segment.raw, generated.IDX_KERNEL_READY)
	if err != nil {
		t.Fatalf("epochSlot() error = %v", err)
	}

	policy := epochWaitPolicy{spinIterations: 4, maxSleep: 50 * time.Microsecond, timeout: 50 * time.Millisecond}
	if err := waitForKernelReady(segment.raw, policy, func() bool { return true }, nil); err == nil {
		t.Fatal("waitForKernelReady() returned before any kernel announced itself")
	}

	publishEpoch(readySlot)
	if err := waitForKernelReady(segment.raw, policy, func() bool { return true }, nil); err != nil {
		t.Fatalf("waitForKernelReady() after the announcement: %v", err)
	}
}

// A host must not wait out its timeout for a kernel that never started.
func TestEpochReadyHandshakeFailsFastWhenTheChildDied(t *testing.T) {
	segment := epochTestSegment(t)
	policy := epochWaitPolicy{spinIterations: 4, maxSleep: 50 * time.Microsecond, timeout: time.Hour}

	started := time.Now()
	err := waitForKernelReady(segment.raw, policy, func() bool { return false }, nil)
	if !errors.Is(err, errEpochPeerLost) {
		t.Fatalf("waitForKernelReady() error = %v, want errEpochPeerLost", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waitForKernelReady() took %v against a child that never started", elapsed)
	}
}

func TestEpochSlotsAndRouteUnix(t *testing.T) {
	raw := make([]byte, 100)
	_, err := epochSlot(raw, 99999)
	if err == nil {
		t.Fatalf("expected error for out of bounds slot")
	}

	shortRaw := make([]byte, 2)
	_, err = epochSlot(shortRaw, 0)
	if err == nil {
		t.Fatalf("expected error for short buffer")
	}

	if err := writeRoute(nil, "echo"); err == nil {
		t.Fatalf("expected error on nil raw for writeRoute")
	}
	if err := writeRoute(make([]byte, 1000), ""); err == nil {
		t.Fatalf("expected error on empty unit id")
	}
	if err := writeRoute(make([]byte, 1000), string(make([]byte, int(generated.ROUTE_MAX_BYTES)+10))); err == nil {
		t.Fatalf("expected error on overly long unit id")
	}
}
