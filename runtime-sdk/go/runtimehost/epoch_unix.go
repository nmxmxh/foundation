//go:build linux || darwin

package runtimehost

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Epoch slots are the crossing, and this file is the host's half of them.
//
// The shm transport moves no bytes but still pays two context switches per
// exchange: a unit-id frame down a pipe and a blocking read of an
// acknowledgement. With the copies gone that pipe is essentially the whole
// remaining cost. Here the host writes the route and payload into the mapping
// and increments IDX_INPUT_WRITTEN; that store is the crossing. The kernel is
// already watching.
//
// Two things the pipe was doing have to be replaced explicitly, and both are:
//
// Ordering. A pipe read is a full barrier, so the old design could not observe
// a half-written buffer. Here the pairing is manual — a release store to
// publish, an acquire load to observe — and it is the one property this design
// can violate and the previous one could not. See
// TestEpochOrderingPublishesThePayloadBeforeTheEpoch.
//
// Liveness. A dead kernel closed stdout and readFrame returned an error. With
// no read in the hot path, dead and slow are indistinguishable from the slot,
// so the child is watched separately and the wait consults that.

// epochWaitPolicy bounds how a host waits on a slot.
type epochWaitPolicy struct {
	spinIterations int
	maxSleep       time.Duration
	timeout        time.Duration
}

const (
	// DefaultEpochSpinIterations is roughly a same-core handoff: long enough
	// that a kernel answering in microseconds is never slept on, short enough
	// that an unresponsive one does not hold a core for meaningful time.
	DefaultEpochSpinIterations = 2000

	// DefaultEpochMaxSleep caps the backoff ladder, and it is small on purpose.
	//
	// A parked waiter overshoots the reply by up to this much, so the cap is a
	// direct tax on every exchange the spin does not catch. It was 200us, which
	// made this transport *slower than the pipe it replaces* for any kernel
	// taking hundreds of microseconds to answer — measured 32% slower at 500us
	// of service time, and reported independently at 38% from a real workload.
	// The pipe cannot overshoot: it blocks once and the peer's write wakes it.
	//
	// A tighter cap trades accuracy for wakeups, and the trade is lopsided.
	// Dropping to 20us costs nothing measurable at the fast end (the spin still
	// catches those) and removes the regression at the slow end: 500us of
	// service went 707us -> 538us, level with the pipe, and 2000us went 2.24ms
	// -> 2.04ms, marginally ahead of it.
	DefaultEpochMaxSleep = 20 * time.Microsecond
)

func (t EpochWaitTuning) policy(timeout time.Duration) epochWaitPolicy {
	if timeout <= 0 {
		timeout = DefaultProcessExchangeTimeout
	}
	policy := epochWaitPolicy{
		spinIterations: DefaultEpochSpinIterations,
		maxSleep:       DefaultEpochMaxSleep,
		timeout:        timeout,
	}
	if t.SpinIterations > 0 {
		policy.spinIterations = t.SpinIterations
	}
	if t.SpinIterations < 0 {
		// Negative means none, distinct from zero meaning unset. A pool whose
		// kernel always takes milliseconds should not spin at all.
		policy.spinIterations = 0
	}
	if t.MaxSleep > 0 {
		policy.maxSleep = t.MaxSleep
	}
	return policy
}

func defaultEpochWaitPolicy(timeout time.Duration) epochWaitPolicy {
	return EpochWaitTuning{}.policy(timeout)
}

// epochSlot returns the atomic word for one epoch index inside a mapping.
//
// The unsafe.Pointer is unavoidable and deliberately confined to this function:
// the slot is a word in another process's address space as much as this one's,
// and only an atomic access to it means anything. Alignment is guaranteed —
// mmap returns page-aligned memory and every slot offset is a multiple of
// EPOCH_SLOT_BYTES, which is 4 — and is asserted rather than assumed.
func epochSlot(raw []byte, index uint32) (*uint32, error) {
	if index >= generated.EPOCH_SLOT_COUNT {
		return nil, fmt.Errorf("epoch index %d is outside the %d slot table", index, generated.EPOCH_SLOT_COUNT)
	}
	offset := int(generated.OFFSET_EPOCHS + index*generated.EPOCH_SLOT_BYTES)
	if offset+4 > len(raw) {
		return nil, fmt.Errorf("epoch slot %d runs past the %d byte mapping", index, len(raw))
	}
	pointer := unsafe.Pointer(&raw[offset]) // #nosec G103 -- page-aligned mmap offset conversion verified by bounds and alignment checks.
	if uintptr(pointer)%4 != 0 {
		return nil, fmt.Errorf("epoch slot %d is not 4-byte aligned", index)
	}
	return (*uint32)(pointer), nil
}

// publishEpoch increments a slot with release semantics.
//
// Go's sync/atomic is sequentially consistent, which is stronger than the
// release this needs. Named for the ordering it provides rather than the
// instruction it emits, because the Rust side pairs with it explicitly and a
// reader comparing the two lanes should see the same word.
func publishEpoch(slot *uint32) uint32 {
	// Wrapping, not saturating. An epoch is a change marker, not a count; a
	// saturated counter would stop signalling after four billion exchanges
	// instead of wrapping past zero harmlessly.
	return atomic.AddUint32(slot, 1)
}

// observeEpoch reads a slot with acquire semantics.
//
// Every read of a region an epoch describes must follow one of these. A plain
// load of the byte would be a data race and, worse, would let the payload read
// be hoisted above it.
func observeEpoch(slot *uint32) uint32 {
	return atomic.LoadUint32(slot)
}

// waitForEpochChange blocks until slot differs from previous.
//
// Compares against what the caller last saw rather than an expected value:
// epochs only move forward, and a waiter that missed one exchange must not
// block forever waiting for a number that has already gone past.
//
// peerAlive is consulted only while parked. During the spin the answer cannot
// have meaningfully changed and the check would cost more than the spin does.
func waitForEpochChange(slot *uint32, previous uint32, policy epochWaitPolicy, peerAlive func() bool) (uint32, error) {
	for i := 0; i < policy.spinIterations; i++ {
		if current := observeEpoch(slot); current != previous {
			return current, nil
		}
		runtime.Gosched()
	}

	deadline := time.Now().Add(policy.timeout)
	sleep := time.Microsecond
	for {
		if current := observeEpoch(slot); current != previous {
			return current, nil
		}
		// Checked after the load, never before. A kernel that published a result
		// and then exited did the work, and calling that a lost peer would throw
		// away a completed exchange.
		if peerAlive != nil && !peerAlive() {
			return previous, errEpochPeerLost
		}
		if time.Now().After(deadline) {
			return previous, errEpochTimeout
		}
		time.Sleep(sleep)
		if sleep < policy.maxSleep {
			sleep *= 2
		}
	}
}

// writeRoute places the unit id where a kernel with no pipe can find it.
//
// Refused rather than truncated when it does not fit: a truncated route either
// resolves to a different unit or to none, and both are worse than an error
// naming the limit.
func writeRoute(raw []byte, unitID string) error {
	start := int(generated.OFFSET_ROUTE_BYTES)
	end := start + int(generated.ROUTE_MAX_BYTES)
	if end > len(raw) {
		return fmt.Errorf("route region runs past the %d byte buffer", len(raw))
	}
	if unitID == "" {
		return fmt.Errorf("unit id is required")
	}
	if len(unitID) > int(generated.ROUTE_MAX_BYTES) {
		return fmt.Errorf("unit id %q is %d bytes; the epoch route holds %d",
			unitID, len(unitID), generated.ROUTE_MAX_BYTES)
	}
	region := raw[start:end]
	clear(region)
	copy(region, unitID)
	return nil
}

// waitForKernelReady blocks until the child has mapped the segment and is
// watching the input slot.
//
// Without this the first exchange races the kernel's own startup: the host
// spawns the child and immediately publishes its warm-up call, and a kernel
// that has not yet taken its baseline snapshot either misses that publication
// or, worse, sees it as already-observed and waits for the next one. The
// symptom is a single exchange that times out at startup and nothing wrong
// afterwards, which is the hardest kind of intermittent to chase.
func waitForKernelReady(raw []byte, policy epochWaitPolicy, peerAlive func() bool) error {
	slot, err := epochSlot(raw, generated.IDX_KERNEL_READY)
	if err != nil {
		return err
	}
	// Against zero rather than a snapshot: the host created this mapping zeroed,
	// so any non-zero value is the kernel having announced itself.
	if _, err := waitForEpochChange(slot, 0, policy, peerAlive); err != nil {
		return fmt.Errorf("runtime kernel did not become ready: %w", err)
	}
	return nil
}
