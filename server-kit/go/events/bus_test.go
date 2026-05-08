package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func makeTestEnvelope(eventType, correlationID string) Envelope {
	env := Envelope{
		EventType:     eventType,
		Payload:       map[string]any{"key": "value"},
		Metadata:      map[string]any{"correlation_id": correlationID, "organization_id": "org_1"},
		CorrelationID: correlationID,
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}
	env.Normalize()
	return env
}

// ---------------------------------------------------------------------------
// Functional Tests
// ---------------------------------------------------------------------------

func TestInMemoryBus_PublishSubscribe(t *testing.T) {
	bus := NewInMemoryBus(100)
	var received atomic.Int32

	bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {
		received.Add(1)
	})

	env := makeTestEnvelope("media:upload:requested", "corr-1")
	if err := bus.Publish(context.Background(), env); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	if received.Load() != 1 {
		t.Fatalf("expected 1 delivery, got %d", received.Load())
	}
}

func TestInMemoryBus_WildcardSubscribe(t *testing.T) {
	bus := NewInMemoryBus(100)
	var received atomic.Int32

	bus.Subscribe("*", func(_ context.Context, _ Envelope) {
		received.Add(1)
	})

	_ = bus.Publish(context.Background(), makeTestEnvelope("media:upload:requested", "c1"))
	_ = bus.Publish(context.Background(), makeTestEnvelope("user:create:requested", "c2"))

	if received.Load() != 2 {
		t.Fatalf("expected 2 deliveries with wildcard, got %d", received.Load())
	}
}

func TestInMemoryBus_PrefixWildcard(t *testing.T) {
	bus := NewInMemoryBus(100)
	var mediaCount, userCount atomic.Int32

	bus.Subscribe("media:*", func(_ context.Context, _ Envelope) {
		mediaCount.Add(1)
	})
	bus.Subscribe("user:*", func(_ context.Context, _ Envelope) {
		userCount.Add(1)
	})

	_ = bus.Publish(context.Background(), makeTestEnvelope("media:upload:requested", "c1"))
	_ = bus.Publish(context.Background(), makeTestEnvelope("media:delete:requested", "c2"))
	_ = bus.Publish(context.Background(), makeTestEnvelope("user:create:requested", "c3"))

	if mediaCount.Load() != 2 {
		t.Fatalf("expected 2 media events, got %d", mediaCount.Load())
	}
	if userCount.Load() != 1 {
		t.Fatalf("expected 1 user event, got %d", userCount.Load())
	}
}

func TestInMemoryBus_MultipleSubscribers(t *testing.T) {
	bus := NewInMemoryBus(100)
	var count1, count2 atomic.Int32

	bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {
		count1.Add(1)
	})
	bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {
		count2.Add(1)
	})

	_ = bus.Publish(context.Background(), makeTestEnvelope("media:upload:requested", "c1"))

	if count1.Load() != 1 || count2.Load() != 1 {
		t.Fatalf("expected both subscribers to receive, got %d and %d", count1.Load(), count2.Load())
	}
}

func TestInMemoryBus_Recent(t *testing.T) {
	bus := NewInMemoryBus(5)

	for i := 0; i < 10; i++ {
		_ = bus.Publish(context.Background(), makeTestEnvelope("media:upload:requested", fmt.Sprintf("c-%d", i)))
	}

	recent := bus.Recent(0)
	if len(recent) != 5 {
		t.Fatalf("expected 5 recent (maxRecent), got %d", len(recent))
	}

	recent3 := bus.Recent(3)
	if len(recent3) != 3 {
		t.Fatalf("expected 3 recent, got %d", len(recent3))
	}
}

func TestInMemoryBusDefaultsAndMiddleWildcardMatching(t *testing.T) {
	bus := NewInMemoryBus(0)
	if bus.maxRecent != 200 {
		t.Fatalf("default max recent = %d, want 200", bus.maxRecent)
	}
	cases := []struct {
		pattern   string
		eventType string
		expected  bool
	}{
		{"media:*:requested", "media:upload:requested", true},
		{"media:*:success", "media:upload:requested", false},
		{"media:upload:requested:extra", "media:upload:requested", false},
	}
	for _, tc := range cases {
		if got := Matches(tc.pattern, tc.eventType); got != tc.expected {
			t.Fatalf("Matches(%q, %q) = %v, want %v", tc.pattern, tc.eventType, got, tc.expected)
		}
	}
}

func TestInMemoryBus_NilSubscriberIgnored(t *testing.T) {
	bus := NewInMemoryBus(100)
	bus.Subscribe("test:event:requested", nil) // should not panic

	_ = bus.Publish(context.Background(), makeTestEnvelope("test:event:requested", "c1"))
}

func TestInMemoryBus_ValidationRejectsInvalid(t *testing.T) {
	bus := NewInMemoryBus(100)

	err := bus.Publish(context.Background(), Envelope{
		EventType: "", // invalid
	})
	if err == nil {
		t.Fatal("expected validation error for empty event type")
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		pattern   string
		eventType string
		expected  bool
	}{
		{"*", "media:upload:requested", true},
		{"", "media:upload:requested", true},
		{"media:upload:requested", "media:upload:requested", true},
		{"media:upload:requested", "media:delete:requested", false},
		{"media:*", "media:upload:requested", true},
		{"user:*", "media:upload:requested", false},
	}

	for _, tc := range cases {
		result := Matches(tc.pattern, tc.eventType)
		if result != tc.expected {
			t.Errorf("Matches(%q, %q) = %v, want %v", tc.pattern, tc.eventType, result, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrency Tests
// ---------------------------------------------------------------------------

func TestInMemoryBus_ConcurrentPublish(t *testing.T) {
	bus := NewInMemoryBus(1000)
	var totalReceived atomic.Int64
	const goroutines = 50
	const messagesPerGoroutine = 100

	bus.Subscribe("*", func(_ context.Context, _ Envelope) {
		totalReceived.Add(1)
	})

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < messagesPerGoroutine; i++ {
				_ = bus.Publish(context.Background(), makeTestEnvelope(
					"media:upload:requested",
					fmt.Sprintf("corr-%d-%d", id, i),
				))
			}
		}(g)
	}
	wg.Wait()

	expected := int64(goroutines * messagesPerGoroutine)
	if totalReceived.Load() != expected {
		t.Fatalf("expected %d deliveries, got %d", expected, totalReceived.Load())
	}
}

func TestInMemoryBus_ConcurrentSubscribePublish(t *testing.T) {
	bus := NewInMemoryBus(500)
	var totalReceived atomic.Int64

	var wg sync.WaitGroup
	// Add subscribers concurrently
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			bus.Subscribe("*", func(_ context.Context, _ Envelope) {
				totalReceived.Add(1)
			})
		}()
	}
	wg.Wait()

	// Now publish
	for i := 0; i < 10; i++ {
		_ = bus.Publish(context.Background(), makeTestEnvelope("media:upload:requested", fmt.Sprintf("c-%d", i)))
	}

	// Each message should reach all 10 subscribers
	if totalReceived.Load() != 100 {
		t.Fatalf("expected 100 total deliveries (10 msgs * 10 subs), got %d", totalReceived.Load())
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkInMemoryBus_Publish_NoSubscribers(b *testing.B) {
	bus := NewInMemoryBus(100)
	env := makeTestEnvelope("media:upload:requested", "bench-corr")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bus.Publish(context.Background(), env)
	}
}

func BenchmarkInMemoryBus_Publish_1Subscriber(b *testing.B) {
	bus := NewInMemoryBus(100)
	bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {})
	env := makeTestEnvelope("media:upload:requested", "bench-corr")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bus.Publish(context.Background(), env)
	}
}

func BenchmarkInMemoryBus_Publish_10Subscribers(b *testing.B) {
	bus := NewInMemoryBus(100)
	for i := 0; i < 10; i++ {
		bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {})
	}
	env := makeTestEnvelope("media:upload:requested", "bench-corr")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bus.Publish(context.Background(), env)
	}
}

func BenchmarkInMemoryBus_Publish_Parallel(b *testing.B) {
	bus := NewInMemoryBus(1000)
	bus.Subscribe("media:upload:requested", func(_ context.Context, _ Envelope) {})
	env := makeTestEnvelope("media:upload:requested", "bench-corr")

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = bus.Publish(context.Background(), env)
		}
	})
}

func BenchmarkMatches(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Matches("media:*", "media:upload:requested")
	}
}

func BenchmarkMatches_Exact(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Matches("media:upload:requested", "media:upload:requested")
	}
}
