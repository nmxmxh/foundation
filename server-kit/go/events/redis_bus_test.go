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
		Payload:       map[string]any{"work_order_id": "wo_1"},
		Metadata:      map[string]any{"correlation_id": "corr_1"},
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
