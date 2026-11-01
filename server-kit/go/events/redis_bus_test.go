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

func echoTestEnvelope(source string) Envelope {
	recs := make([]any, 20)
	for i := range recs {
		recs[i] = map[string]any{"record_id": "rec_000123", "status": "available", "price_minor": 1234 * i}
	}
	return Envelope{
		EventType:     "projection:mutation:v1:success",
		Payload:       ObjectFromMap(map[string]any{"records": recs}),
		Metadata:      ObjectFromMap(map[string]any{"correlation_id": "corr_echo", "organization_id": "org_1"}),
		CorrelationID: "corr_echo",
		SchemaVersion: "1.0",
		Timestamp:     time.Unix(1762000000, 0).UTC(),
		SourceNodeID:  source,
	}
}

// Regression: the self-echo fast path must identify only this node's binary publishes.
// Anything it cannot identify goes to the full decode path, which keeps its own check.
func TestIsOwnBinaryEnvelope(t *testing.T) {
	own, err := echoTestEnvelope("node-a").ToBinary()
	if err != nil {
		t.Fatalf("ToBinary: %v", err)
	}
	other, err := echoTestEnvelope("node-b").ToBinary()
	if err != nil {
		t.Fatalf("ToBinary: %v", err)
	}
	unsourced, err := echoTestEnvelope("").ToBinary()
	if err != nil {
		t.Fatalf("ToBinary: %v", err)
	}
	asJSON, err := echoTestEnvelope("node-a").ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	cases := []struct {
		name   string
		raw    []byte
		nodeID string
		want   bool
	}{
		{"own publish", own, "node-a", true},
		{"other node", other, "node-a", false},
		{"no source field", unsourced, "node-a", false},
		{"json envelope", asJSON, "node-a", false},
		{"truncated", own[:len(own)/2], "node-a", false},
		{"garbage", []byte{0xff, 0xff, 0xff}, "node-a", false},
		{"empty node id", own, "", false},
		{"prefix of node id", own, "node-", false},
	}
	for _, tc := range cases {
		if got := isOwnBinaryEnvelope(tc.raw, tc.nodeID); got != tc.want {
			t.Fatalf("%s: isOwnBinaryEnvelope = %v, want %v", tc.name, got, tc.want)
		}
	}
	if allocs := testing.AllocsPerRun(50, func() { _ = isOwnBinaryEnvelope(own, "node-a") }); allocs != 0 {
		t.Fatalf("isOwnBinaryEnvelope allocates %.0f times, want 0", allocs)
	}
}

// A node must not re-deliver its own publish when Redis echoes it back.
func TestRedisBusDropsOwnEchoOnce(t *testing.T) {
	client, err := rediskit.Connect("", "reframe_v1", rediskit.DriverMemory)
	if err != nil {
		t.Fatalf("create memory redis client: %v", err)
	}
	defer func() { _ = client.Close() }()
	bus := NewRedisBus(client, "events", 10, nil)
	defer func() { _ = bus.Close() }()
	deliveries := make(chan Envelope, 4)
	bus.Subscribe("projection:*", func(_ context.Context, env Envelope) { deliveries <- env })
	time.Sleep(50 * time.Millisecond) // let the listener subscribe so the echo can arrive
	if err := bus.Publish(context.Background(), echoTestEnvelope("")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	<-deliveries // the local dispatch
	select {
	case env := <-deliveries:
		t.Fatalf("own publish was delivered twice: %s", env.EventType)
	case <-time.After(200 * time.Millisecond):
	}
	if n := len(bus.Recent(10)); n != 1 {
		t.Fatalf("recent holds %d events, want 1", n)
	}
}

// BenchmarkRedisBusSelfEcho compares the full decode an echoed publish used to pay
// with the field scan that now drops it.
func BenchmarkRedisBusSelfEcho(b *testing.B) {
	raw, err := echoTestEnvelope("node-a").ToBinary()
	if err != nil {
		b.Fatal(err)
	}
	b.Run("full_decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			env, err := Decode(raw)
			if err == nil {
				_ = env.MaterializeMetadata()
			}
		}
	})
	b.Run("field_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = isOwnBinaryEnvelope(raw, "node-a")
		}
	})
}
