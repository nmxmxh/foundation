package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHandlerExecutionControllerReturnsExplicitConcurrencyError(t *testing.T) {
	controller := NewHandlerExecutionController(ConcurrencyOptions{
		MaxConcurrent:  1,
		AcquireTimeout: 20 * time.Millisecond,
	})

	release := make(chan struct{})
	started := make(chan struct{})
	wrapped := controller.Wrap(func(ctx context.Context, payload map[string]any) (any, error) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = wrapped(context.Background(), map[string]any{"id": "first"})
	}()

	<-started

	_, err := wrapped(context.Background(), map[string]any{"id": "second"})
	if !errors.Is(err, ErrConcurrencyLimitReached) {
		t.Fatalf("expected ErrConcurrencyLimitReached, got %v", err)
	}

	close(release)
	<-done
}

func TestTokenBucketLimiterUsesBurstAndRefill(t *testing.T) {
	limiter := newTokenBucketLimiter(ConcurrencyOptions{
		RateLimitRate:   1,
		RateLimitPeriod: 120 * time.Millisecond,
		RateLimitBurst:  1,
	})
	if limiter == nil {
		t.Fatal("expected limiter to be configured")
	}

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error consuming initial token: %v", err)
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelShort()
	if err := limiter.Wait(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded before refill, got %v", err)
	}

	time.Sleep(140 * time.Millisecond)

	refillCtx, cancelRefill := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelRefill()
	if err := limiter.Wait(refillCtx); err != nil {
		t.Fatalf("expected limiter to refill, got %v", err)
	}
}
