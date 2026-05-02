package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPolicy_Do(t *testing.T) {
	ctx := context.Background()
	var attempts atomic.Int32
	errFake := errors.New("temporary error")

	policy := NewPolicy(Config{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
		RetryIf:      func(err error) bool { return true },
	})

	err := policy.Do(ctx, func() error {
		attempts.Add(1)
		if attempts.Load() < 3 {
			return errFake
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestPolicy_ExhaustAttempts(t *testing.T) {
	ctx := context.Background()
	errFake := errors.New("persistent error")

	policy := NewPolicy(Config{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Millisecond,
	})

	err := policy.Do(ctx, func() error {
		return errFake
	})

	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	var maxErr *MaxAttemptsError
	if !errors.As(err, &maxErr) {
		t.Fatalf("expected MaxAttemptsError, got %T", err)
	}
	if maxErr.Attempts != 2 {
		t.Fatalf("expected 2 attempts in error, got %d", maxErr.Attempts)
	}
}

func TestPolicy_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	policy := NewPolicy(Config{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
	})

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := policy.Do(ctx, func() error {
		return errors.New("fail")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestPolicy_NonRetryableError(t *testing.T) {
	ctx := context.Background()
	policy := NewPolicy(Config{
		MaxAttempts: 3,
		RetryIf: func(err error) bool {
			return err.Error() != "fatal"
		},
	})

	attempts := 0
	err := policy.Do(ctx, func() error {
		attempts++
		return errors.New("fatal")
	})

	if err == nil || err.Error() != "fatal" {
		t.Fatalf("expected 'fatal' error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestPolicy_DoWithResult(t *testing.T) {
	ctx := context.Background()
	policy := NewPolicy(Config{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Millisecond,
	})

	res, err := policy.DoWithResult(ctx, func() (interface{}, error) {
		return "success", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "success" {
		t.Fatalf("expected 'success', got %v", res)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPolicy_Do_Success(b *testing.B) {
	ctx := context.Background()
	policy := NewPolicy(Config{MaxAttempts: 3})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = policy.Do(ctx, func() error {
			return nil
		})
	}
}

func BenchmarkPolicy_Do_Retry(b *testing.B) {
	ctx := context.Background()
	policy := NewPolicy(Config{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Nanosecond, // minimal delay for benchmark
		Multiplier:   1.0,
		Jitter:       0,
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		attempts := 0
		_ = policy.Do(ctx, func() error {
			attempts++
			if attempts < 2 {
				return errors.New("fail")
			}
			return nil
		})
	}
}

func BenchmarkPolicy_CalculateDelay(b *testing.B) {
	policy := NewPolicy(DefaultConfig())

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = policy.calculateDelay(i%10 + 1)
	}
}
