package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var errFake = errors.New("fake failure")

// ---------------------------------------------------------------------------
// Functional Tests
// ---------------------------------------------------------------------------

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := New("test-svc", Config{
		FailureThreshold: 3,
		Timeout:          10 * time.Second,
		Clock:            clock,
	})

	if cb.State() != StateClosed {
		t.Fatalf("expected closed, got %s", cb.State())
	}

	for i := 0; i < 3; i++ {
		_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
			return nil, errFake
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected open after %d failures, got %s", 3, cb.State())
	}
}

func TestCircuitBreaker_OpenRejectsRequests(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := New("test-svc", Config{
		FailureThreshold: 1,
		Timeout:          10 * time.Second,
		Clock:            clock,
	})

	// Trip the breaker
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	_, err := cb.Execute(context.Background(), func() (interface{}, error) {
		return "should-not-reach", nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := New("test-svc", Config{
		FailureThreshold: 1,
		Timeout:          5 * time.Second,
		Clock:            clock,
	})

	// Trip the breaker
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}

	// Advance past timeout
	clock.Advance(6 * time.Second)

	// Next call should transition to half-open and succeed
	result, err := cb.Execute(context.Background(), func() (interface{}, error) {
		return "recovered", nil
	})
	if err != nil {
		t.Fatalf("expected success in half-open, got %v", err)
	}
	if result != "recovered" {
		t.Fatalf("expected 'recovered', got %v", result)
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := New("test-svc", Config{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		Timeout:          5 * time.Second,
		HalfOpenMaxCalls: 5,
		Clock:            clock,
	})

	// Trip breaker
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	// Advance past timeout
	clock.Advance(6 * time.Second)

	// Succeed enough times in half-open
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
			return "ok", nil
		})
	}

	if cb.State() != StateClosed {
		t.Fatalf("expected closed after recovery, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenBackToOpen(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	cb := New("test-svc", Config{
		FailureThreshold: 1,
		SuccessThreshold: 3,
		Timeout:          5 * time.Second,
		HalfOpenMaxCalls: 5,
		Clock:            clock,
	})

	// Trip breaker
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	clock.Advance(6 * time.Second)

	// Fail in half-open
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	if cb.State() != StateOpen {
		t.Fatalf("expected open after half-open failure, got %s", cb.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := New("test-svc", Config{FailureThreshold: 1})

	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}

	cb.Reset()
	if cb.State() != StateClosed {
		t.Fatalf("expected closed after reset, got %s", cb.State())
	}
}

func TestCircuitBreaker_ExecuteWithFallback(t *testing.T) {
	cb := New("test-svc", Config{FailureThreshold: 1})

	// Trip
	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	result, err := cb.ExecuteWithFallback(
		context.Background(),
		func() (interface{}, error) { return nil, errFake },
		func(err error) (interface{}, error) { return "fallback_value", nil },
	)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if result != "fallback_value" {
		t.Fatalf("expected 'fallback_value', got %v", result)
	}
}

func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	cb := New("test-svc", Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := cb.Execute(ctx, func() (interface{}, error) {
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := New("my-service", Config{FailureThreshold: 5})
	stats := cb.Stats()
	if stats.Name != "my-service" {
		t.Fatalf("expected name 'my-service', got '%s'", stats.Name)
	}
	if stats.State != "closed" {
		t.Fatalf("expected 'closed', got '%s'", stats.State)
	}
	if stats.Config.FailureThreshold != 5 {
		t.Fatalf("expected threshold 5, got %d", stats.Config.FailureThreshold)
	}
}

func TestCircuitBreaker_OnStateChange(t *testing.T) {
	var transitions []string
	var mu sync.Mutex

	cb := New("test-svc", Config{
		FailureThreshold: 1,
		OnStateChange: func(name string, from, to State) {
			mu.Lock()
			transitions = append(transitions, from.String()+"->"+to.String())
			mu.Unlock()
		},
	})

	_, _ = cb.Execute(context.Background(), func() (interface{}, error) {
		return nil, errFake
	})

	time.Sleep(50 * time.Millisecond) // OnStateChange is called in a goroutine

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) < 1 {
		t.Fatal("expected at least 1 state transition callback")
	}
	if transitions[0] != "closed->open" {
		t.Fatalf("expected 'closed->open', got '%s'", transitions[0])
	}
}

// ---------------------------------------------------------------------------
// Concurrency Tests
// ---------------------------------------------------------------------------

func TestCircuitBreaker_ConcurrentExecutions(t *testing.T) {
	cb := New("concurrent-svc", Config{
		FailureThreshold: 100,
		SuccessThreshold: 5,
		Timeout:          time.Second,
	})

	const goroutines = 100
	const opsPerGoroutine = 100
	var wg sync.WaitGroup
	var successCount, failCount atomic.Int64

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				_, err := cb.Execute(context.Background(), func() (interface{}, error) {
					if id%3 == 0 {
						return nil, errFake
					}
					return "ok", nil
				})
				if err == nil {
					successCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := successCount.Load() + failCount.Load()
	if total != goroutines*opsPerGoroutine {
		t.Fatalf("expected %d total ops, got %d", goroutines*opsPerGoroutine, total)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkCircuitBreaker_Execute_Closed(b *testing.B) {
	cb := New("bench-svc", Config{FailureThreshold: 1000})
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = cb.Execute(ctx, func() (interface{}, error) {
			return "ok", nil
		})
	}
}

func BenchmarkCircuitBreaker_Execute_Open(b *testing.B) {
	cb := New("bench-svc", Config{FailureThreshold: 1, Timeout: time.Hour})
	ctx := context.Background()

	// Trip the circuit
	_, _ = cb.Execute(ctx, func() (interface{}, error) {
		return nil, errFake
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = cb.Execute(ctx, func() (interface{}, error) {
			return "should-not-reach", nil
		})
	}
}

func BenchmarkCircuitBreaker_Execute_Parallel(b *testing.B) {
	cb := New("bench-svc", Config{FailureThreshold: 100000})
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cb.Execute(ctx, func() (interface{}, error) {
				return "ok", nil
			})
		}
	})
}

func BenchmarkCircuitBreaker_StateRead(b *testing.B) {
	cb := New("bench-svc", Config{})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = cb.State()
	}
}
