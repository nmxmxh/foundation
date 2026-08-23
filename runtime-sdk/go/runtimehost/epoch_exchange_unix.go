//go:build linux || darwin

package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

var (
	errEpochTimeout  = errors.New("epoch exchange timed out")
	errEpochPeerLost = errors.New("runtime kernel is gone")
)

// epochExchange swaps a control buffer using only stores to shared memory.
//
// The pipe is still held — see the liveness field — but never read or written
// during an exchange. That is the whole difference from sharedMemoryExchange,
// and it is worth roughly the cost of two context switches per call.
type epochExchange struct {
	shm    *sharedMemorySegment
	policy epochWaitPolicy

	// alive reports whether the child is still running. Supplied by the worker
	// rather than probed here, because the pool owns the process and this owns
	// the protocol. A nil alive means the wait can only end on timeout, which
	// is correct but slow — it is never nil in production.
	alive func() bool

	// stdin is used as a doorbell to unpark the sleeping worker.
	stdin io.Writer

	// doorbell is used to receive a wake up signal from the worker.
	doorbell <-chan struct{}
}

var doorbellFrame = []byte{1}

func (x epochExchange) Exchange(ctx context.Context, unitID string, buffer []byte) error {
	if x.shm == nil || len(x.shm.raw) == 0 {
		return errors.New("shared memory segment is not initialized")
	}
	if len(buffer) > len(x.shm.raw) {
		return fmt.Errorf("control buffer is %d bytes, shared segment is %d", len(buffer), len(x.shm.raw))
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	inputSlot, err := epochSlot(x.shm.raw, generated.IDX_INPUT_WRITTEN)
	if err != nil {
		return err
	}
	outputSlot, err := epochSlot(x.shm.raw, generated.IDX_OUTPUT_WRITTEN)
	if err != nil {
		return err
	}

	// Snapshotted before the payload is staged, not after. Between staging and
	// publishing, the kernel is not yet looking; after publishing it may answer
	// at any moment, and a snapshot taken then could already be the reply.
	lastOutput := observeEpoch(outputSlot)

	// Everything except the epoch region. The caller's buffer carries its own
	// epoch words, and under the pipe doorbell copying them in was harmless
	// because nothing signalled through them. Here they *are* the channel:
	// a wholesale copy resets IDX_INPUT_WRITTEN to the caller's value, the
	// kernel sees no change or a change it already handled, and the exchange
	// hangs until timeout. The round-trip test found this on its first run.
	copy(x.shm.raw[generated.OFFSET_HEADER_INTS:], buffer[generated.OFFSET_HEADER_INTS:])
	if err := writeRoute(x.shm.raw, unitID); err != nil {
		return err
	}

	// Every store above must be visible before this one. publishEpoch carries
	// the release; nothing may be added between it and the copy.
	publishEpoch(inputSlot)

	if x.stdin != nil {
		// Doorbell fallback: wake up the parked worker immediately.
		if _, err := x.stdin.Write(doorbellFrame); err != nil {
			return err
		}
	}

	newOutput, err := waitForEpochChange(outputSlot, lastOutput, x.policy, x.alive, x.doorbell)
	if err != nil {
		return err
	}

	// The acquire in waitForEpochChange makes this copy safe against the
	// publish that produced newOutput — but a fast kernel may already be into
	// its NEXT write if it never blocks on the consumed ack. Validate the read
	// seqlock-style: the output epoch must still be newOutput after the copy,
	// otherwise the bytes are a torn mix of two generations. Bounded retries;
	// each pass re-arms on the latest observed epoch.
	for range 8 {
		copy(buffer, x.shm.raw)
		if current := observeEpoch(outputSlot); current == newOutput {
			break
		}
		next, waitErr := waitForEpochChange(outputSlot, observeEpoch(outputSlot), x.policy, x.alive, x.doorbell)
		if waitErr != nil {
			return waitErr
		}
		newOutput = next
	}

	consumed, err := epochSlot(x.shm.raw, generated.IDX_OUTPUT_CONSUMED)
	if err != nil {
		return err
	}
	publishEpoch(consumed)
	return nil
}

// Close leaves the segment alone: the worker owns it and closes it on shutdown.
func (x epochExchange) Close() error { return nil }

// Restart has nothing to reset. Epochs are compared against what the caller
// last saw, so a restarted kernel that begins from zero is observed as a change
// like any other rather than as a stall.
func (x epochExchange) Restart() error { return nil }
