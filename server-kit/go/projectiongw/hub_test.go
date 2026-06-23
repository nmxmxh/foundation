package projectiongw

import (
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
)

// TestHubDropsForSlowConsumer proves the drop accounting the WS handler relies
// on to emit resync control frames: a subscriber that does not drain has frames
// shed once its bounded buffer is full, and Dropped() counts them.
func TestHubDropsForSlowConsumer(t *testing.T) {
	hub := NewHub(1)
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
	sub := hub.Subscribe(scope)
	defer sub.Cancel()

	key := ScopeKey(scope)
	// One frame fits the buffer; the next two are dropped because the consumer
	// never reads.
	hub.Broadcast(key, Frame{Watermark: "1"})
	hub.Broadcast(key, Frame{Watermark: "2"})
	hub.Broadcast(key, Frame{Watermark: "3"})

	if got := sub.Dropped(); got != 2 {
		t.Fatalf("Dropped() = %d, want 2", got)
	}
}

func TestHubBroadcastReachesSubscriber(t *testing.T) {
	hub := NewHub(4)
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
	sub := hub.Subscribe(scope)
	defer sub.Cancel()

	hub.Broadcast(ScopeKey(scope), Frame{Watermark: "7", Epoch: 3})
	frame := <-sub.Frames
	if frame.Watermark != "7" || frame.Epoch != 3 {
		t.Fatalf("frame = %+v", frame)
	}
	if sub.Dropped() != 0 {
		t.Fatalf("unexpected drops: %d", sub.Dropped())
	}
}
