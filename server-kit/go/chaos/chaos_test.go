package chaos

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInjectedFailureAndPartition(t *testing.T) {
	injector := NewInjector()
	injector.InjectFailure("redis", 1)
	if err := injector.Apply(context.Background(), "redis", "op"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Apply() error = %v, want injected failure", err)
	}
	injector.Clear("redis")
	injector.InjectPartition("worker-queue")
	if err := injector.Apply(context.Background(), "worker-queue", "op"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Apply() partition error = %v", err)
	}
}

func TestInjectedLatencyHonorsContext(t *testing.T) {
	injector := NewInjector()
	injector.InjectLatency("database", time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := injector.Apply(ctx, "database", "op"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Apply() error = %v, want deadline exceeded", err)
	}
}

func TestChaosInjectorEdges(t *testing.T) {
	if err := (*Injector)(nil).Apply(context.Background(), "none", "op"); err != nil {
		t.Fatalf("nil injector should no-op: %v", err)
	}
	injector := NewInjector()
	injector.InjectFailure("redis", -1)
	if err := injector.Apply(context.Background(), "redis", "op"); err != nil {
		t.Fatalf("negative failure rate should clamp to no failure: %v", err)
	}
	injector.InjectFailure("redis", 2)
	if err := injector.Apply(context.Background(), "redis", "op"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("failure rate > 1 should clamp to failure, got %v", err)
	}
	injector.Clear("redis")
	injector.InjectLatency("redis", time.Millisecond)
	start := time.Now()
	if err := injector.Apply(context.Background(), "redis", "op"); err != nil {
		t.Fatalf("latency apply error = %v", err)
	}
	if time.Since(start) < time.Millisecond {
		t.Fatalf("latency was not applied")
	}
	if shouldFail("redis", "op", 0) || !shouldFail("redis", "op", 1) {
		t.Fatalf("shouldFail bounds failed")
	}
	_ = shouldFail("redis", "stable-operation", 0.5)
}
