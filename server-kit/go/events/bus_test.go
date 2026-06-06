package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
)

func makeTestEnvelope(eventType, correlationID string) Envelope {
	env := Envelope{
		EventType:     eventType,
		Payload:       ObjectFromMap(map[string]any{"key": "value"}),
		Metadata:      ObjectFromMap(map[string]any{"correlation_id": correlationID, "organization_id": "org_1"}),
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
	observability.Default().Reset()
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
	if trace := observability.Default().Trace("corr-1", 1); len(trace) != 1 || trace[0].EventType != "media:upload:requested" {
		t.Fatalf("expected publish trace, got %+v", trace)
	}
}

func TestInMemoryBus_PublishInjectsContextMetadata(t *testing.T) {
	bus := NewInMemoryBus(10)
	ctx := metadata.IntoContext(context.Background(), metadata.EnvelopeMetadata{
		CorrelationID: "corr_ctx",
		GlobalContext: &metadata.GlobalContext{
			OrganizationID: "org_ctx",
			UserID:         "user_ctx",
			Source:         "test",
		},
	})

	if err := bus.Publish(ctx, Envelope{
		EventType:     "media:upload:requested",
		Payload:       ObjectFromMap(map[string]any{"key": "value"}),
		CorrelationID: "corr_ctx",
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	recent := bus.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("recent len = %d, want 1", len(recent))
	}
	md := metadata.FromMap(recent[0].Metadata.InterfaceMap())
	if md.CorrelationID != "corr_ctx" || md.GlobalContext == nil || md.GlobalContext.OrganizationID != "org_ctx" || md.GlobalContext.UserID != "user_ctx" {
		t.Fatalf("metadata was not injected from context: %#v", recent[0].Metadata)
	}
}

func TestInMemoryBus_PublishMergesContextTags(t *testing.T) {
	bus := NewInMemoryBus(10)
	ctx := metadata.IntoContext(context.Background(), metadata.EnvelopeMetadata{
		CorrelationID: "corr_ctx",
		Tags:          []string{"request:tag", "shared"},
		Categories:    []string{"request"},
	})

	if err := bus.Publish(ctx, Envelope{
		EventType:     "media:upload:requested",
		Payload:       ObjectFromMap(map[string]any{"key": "value"}),
		Metadata:      ObjectFromMap(map[string]any{"tags": []string{"domain:tag", "shared"}, "categories": []any{"domain"}}),
		CorrelationID: "corr_ctx",
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	recent := bus.Recent(1)
	md := metadata.FromMap(recent[0].Metadata.InterfaceMap())
	if got, want := fmt.Sprint(md.Tags), "[request:tag shared domain:tag]"; got != want {
		t.Fatalf("tags = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(md.Categories), "[request domain]"; got != want {
		t.Fatalf("categories = %s, want %s", got, want)
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

func TestInMemoryBus_MixedPrefixAndComplexWildcard(t *testing.T) {
	bus := NewInMemoryBus(100)
	var broad, tenant, complex atomic.Int32

	bus.Subscribe("tenant:*", func(_ context.Context, _ Envelope) {
		broad.Add(1)
	})
	bus.Subscribe("tenant:org_0042:*", func(_ context.Context, _ Envelope) {
		tenant.Add(1)
	})
	bus.Subscribe("tenant:*:signal:success", func(_ context.Context, _ Envelope) {
		complex.Add(1)
	})

	_ = bus.Publish(context.Background(), makeTestEnvelope("tenant:org_0042:signal:success", "c1"))
	if broad.Load() != 1 || tenant.Load() != 1 || complex.Load() != 1 {
		t.Fatalf("deliveries broad=%d tenant=%d complex=%d, want 1/1/1", broad.Load(), tenant.Load(), complex.Load())
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

	for i := range 10 {
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
	for i, env := range recent3 {
		want := fmt.Sprintf("c-%d", i+7)
		if env.CorrelationID != want {
			t.Fatalf("recent[%d] correlation = %q, want %q", i, env.CorrelationID, want)
		}
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
	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			for i := range messagesPerGoroutine {
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

func TestInMemoryBusFanoutPressureIsSynchronousAndIsolated(t *testing.T) {
	bus := NewInMemoryBus(64)
	const messages = 256
	var all, tenantA, tenantB atomic.Int64

	bus.Subscribe("*", func(_ context.Context, _ Envelope) {
		all.Add(1)
	})
	bus.Subscribe("tenant:a:*", func(_ context.Context, env Envelope) {
		if orgID, _ := env.Metadata.GetString("organization_id"); orgID != "org_a" {
			t.Errorf("tenant a metadata leaked: %+v", env.Metadata)
		}
		tenantA.Add(1)
	})
	bus.Subscribe("tenant:b:*", func(_ context.Context, env Envelope) {
		if orgID, _ := env.Metadata.GetString("organization_id"); orgID != "org_b" {
			t.Errorf("tenant b metadata leaked: %+v", env.Metadata)
		}
		tenantB.Add(1)
	})

	for i := range messages {
		org := "org_a"
		eventType := "tenant:a:signal:requested"
		if i%2 == 1 {
			org = "org_b"
			eventType = "tenant:b:signal:requested"
		}
		env := makeTestEnvelope(eventType, fmt.Sprintf("corr-%03d", i))
		env.Metadata["organization_id"] = objectFromMap(map[string]any{"value": org})["value"]
		if err := bus.Publish(context.Background(), env); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	if all.Load() != messages {
		t.Fatalf("all deliveries = %d, want %d", all.Load(), messages)
	}
	if tenantA.Load() != messages/2 || tenantB.Load() != messages/2 {
		t.Fatalf("tenant deliveries a=%d b=%d, want %d each", tenantA.Load(), tenantB.Load(), messages/2)
	}
	if recent := bus.Recent(0); len(recent) != 64 {
		t.Fatalf("recent length = %d, want bounded 64", len(recent))
	}
}

func TestInMemoryBus_ConcurrentSubscribePublish(t *testing.T) {
	bus := NewInMemoryBus(500)
	var totalReceived atomic.Int64

	var wg sync.WaitGroup
	// Add subscribers concurrently
	wg.Add(10)
	for range 10 {
		go func() {
			defer wg.Done()
			bus.Subscribe("*", func(_ context.Context, _ Envelope) {
				totalReceived.Add(1)
			})
		}()
	}
	wg.Wait()

	// Now publish
	for i := range 10 {
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
	for range 10 {
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

func BenchmarkTerminalState(b *testing.B) {
	eventType := "media:upload:v1:success"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if TerminalState(eventType) != "success" {
			b.Fatal("unexpected terminal state")
		}
	}
}
