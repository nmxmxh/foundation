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
