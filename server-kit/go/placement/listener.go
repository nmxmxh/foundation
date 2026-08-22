package placement

import (
	"context"
	"fmt"

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
// subscription closes.
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
) error {
	messages, stop, err := client.Subscribe(ctx, channel)
	if err != nil {
		return fmt.Errorf("placement: subscribe mirrors: %w", err)
	}
	go func() {
		defer stop()
		index := 0
		for {
			select {
			case <-ctx.Done():
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
	return nil
}
