package events

import (
	"context"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestRedisBusPublishAndSubscribeWithMemoryDriver(t *testing.T) {
	client, err := rediskit.Connect("", "reframe_v1", rediskit.DriverMemory)
	if err != nil {
		t.Fatalf("create memory redis client: %v", err)
	}
	bus := NewRedisBus(client, "events", 10, nil)
	defer func() {
		_ = bus.Close()
	}()

	seen := make(chan Envelope, 1)
	bus.Subscribe("operations:*", func(_ context.Context, env Envelope) {
		seen <- env
	})

	err = bus.Publish(context.Background(), Envelope{
		EventType:     "operations:create_work_order:v1:requested",
		Payload:       ObjectFromMap(map[string]any{"work_order_id": "wo_1"}),
		Metadata:      ObjectFromMap(map[string]any{"correlation_id": "corr_1"}),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case got := <-seen:
		if got.EventType != "operations:create_work_order:v1:requested" {
			t.Fatalf("unexpected event type: %s", got.EventType)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("subscriber did not receive event")
	}

	recent := bus.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("expected one recent event, got %d", len(recent))
	}
}

// TestRedisBusDeliversAcrossNodes exercises the cross-node path the single-bus
// test never reaches: two buses share one client, so a publish on node A flows
// through node B's background listener (Subscribe → consumeLoop → dispatch) and
// out to B's subscribers. Blocking on B's subscriber makes the listener's
// progress deterministic instead of scheduling-dependent — the source of the
// flaky coverage on this package — and closing B while it idles in the consume
// select drives the ctx-cancel teardown branch.
func TestRedisBusDeliversAcrossNodes(t *testing.T) {
	client, err := rediskit.Connect("", "reframe_v1", rediskit.DriverMemory)
	if err != nil {
		t.Fatalf("create memory redis client: %v", err)
	}
	// Shared client → A's publish reaches B's listener over the same channel.
	nodeA := NewRedisBus(client, "events", 10, nil)
	nodeB := NewRedisBus(client, "events", 10, nil)
	// LIFO teardown: close B and A (cancel their listeners) before the client,
	// so the listeners exit via ctx-cancel rather than a closed message channel.
	defer func() { _ = client.Close() }()
	defer func() { _ = nodeA.Close() }()
	defer func() { _ = nodeB.Close() }()

	seen := make(chan Envelope, 1)
	nodeB.Subscribe("operations:*", func(_ context.Context, env Envelope) {
		select {
		case seen <- env:
		default:
		}
	})

	event := Envelope{
		EventType:     "operations:create_work_order:v1:requested",
		Payload:       ObjectFromMap(map[string]any{"work_order_id": "wo_x"}),
		Metadata:      ObjectFromMap(map[string]any{"correlation_id": "corr_x"}),
		CorrelationID: "corr_x",
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}

	// Pub/sub has no replay: retry until node B's listener has subscribed and a
	// publish gets through, then assert it arrived by the cross-node path.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(2 * time.Second)
	for {
		if err := nodeA.Publish(context.Background(), event); err != nil {
			t.Fatalf("publish failed: %v", err)
		}
		select {
		case got := <-seen:
			if got.EventType != event.EventType {
				t.Fatalf("cross-node event type = %s", got.EventType)
			}
			if got.SourceNodeID != "" {
				t.Fatalf("received envelope should have source node stripped, got %q", got.SourceNodeID)
			}
			return
		case <-tick.C:
			continue
		case <-deadline:
			t.Fatal("cross-node event was not delivered through node B's listener")
		}
	}
}
