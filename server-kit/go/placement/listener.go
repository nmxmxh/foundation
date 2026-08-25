package placement

import (
	"context"
	"fmt"
	"sync"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// DefaultMirrorChannel carries lane-mirror frames. The rediskit prefix
// qualifies it per deployment, exactly like every other bus channel.
const DefaultMirrorChannel = "compute:lane:v1"

// MirrorSink receives decoded mirror updates into a local view.
//
// runtimehost.DispatchBlock implements this through ApplyMirrorUpdate; the
// interface exists so tests and non-SHM consumers (for example a hub
// aggregating into its own table) can sit in without shared memory.
type MirrorSink interface {
	ApplyMirrorUpdate(update LaneMirrorUpdate) error
}

// PublishLaneMirrors sends one batch of updates on the mirror channel.
//
// Cadence belongs to the caller: publish on change or at most once per tick
// window. A hub relaying its edges should coalesce batches — one frame per
// region per window, not one frame per lane.
func PublishLaneMirrors(
	ctx context.Context,
	client rediskit.Client,
	channel string,
	nodeID string,
	regionID string,
	class LaneClass,
	lanes []LaneMirrorUpdate,
) error {
	frame, err := EncodeLaneMirrorFrame(nodeID, regionID, class, lanes)
	if err != nil {
		return fmt.Errorf("placement: encode mirror frame: %w", err)
	}
	if err := client.Publish(ctx, channel, frame); err != nil {
		return fmt.Errorf("placement: publish mirrors: %w", err)
	}
	return nil
}

// ListenMirrors applies inbound mirror frames to sink until ctx ends or the
// subscription closes, and returns a stop function that ends the listener and
// waits for it to finish.
//
// Callers must call stop before releasing anything the sink borrows. The
// listener runs on its own goroutine and calls sink.ApplyMirrorUpdate from
// there; cancelling ctx only *signals* it, so a caller that cancels and
// immediately tears down the sink races a call already in flight. Where the
// sink is a runtimehost.DispatchBlock that race is not benign — the block hands
// out pointers into an mmap'd region and Close munmaps it, so the in-flight
// apply reads unmapped memory rather than merely stale memory.
//
// stop cancels and then waits, so it is safe in any order and cannot deadlock
// on a caller who forgot to cancel first. Deferring it directly after the call
// gives the correct teardown order for free, because the sink's own cleanup was
// necessarily deferred earlier and therefore runs later:
//
//	block, err := OpenDispatchRegion(path)
//	defer block.Close()
//	stop, err := placement.ListenMirrors(ctx, bus, channel, block, nil)
//	defer stop()
//
// Undecodable frames are counted and skipped, never fatal: the mirror channel
// is shared transport and foreign payloads are expected. Apply errors surface
// through onError so operators see a degraded mesh picture without the
// listener crashing.
func ListenMirrors(
	ctx context.Context,
	client rediskit.Client,
	channel string,
	sink MirrorSink,
	onError func(frameIndex int, cause error),
) (func(), error) {
	// Own the cancellation so stop can end the listener without needing the
	// caller's cancel func; the parent ctx still ends it as before.
	listenCtx, cancel := context.WithCancel(ctx)
	messages, unsubscribe, err := client.Subscribe(listenCtx, channel)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("placement: subscribe mirrors: %w", err)
	}
	done := make(chan struct{})
	go func() {
		// Deferred last, so it runs first on the way out: a waiter is only
		// released once the subscription is torn down and no further apply can
		// be issued. Single owner — this goroutine is the only closer.
		defer close(done)
		defer unsubscribe()
		index := 0
		for {
			select {
			case <-listenCtx.Done():
				return
			case payload, ok := <-messages:
				if !ok {
					return
				}
				frame, decodeErr := DecodeLaneMirrorFrame(payload)
				if decodeErr != nil {
					if onError != nil {
						onError(index, decodeErr)
					}
					index++
					continue
				}
				for _, lane := range frame.Lanes {
					// Validate at the choke point, not in each sink: a
					// lying publisher must never reach application state
					// regardless of what implements MirrorSink.
					if applyErr := ValidateMirrorStats(lane); applyErr != nil {
						if onError != nil {
							onError(index, applyErr)
						}
						continue
					}
					if applyErr := sink.ApplyMirrorUpdate(lane); applyErr != nil && onError != nil {
						onError(index, applyErr)
					}
				}
				index++
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}, nil
}
