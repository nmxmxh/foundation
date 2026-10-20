package retry

import (
	"context"
	"errors"
	"strings"
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

	res, err := policy.DoWithResult(ctx, func() (any, error) {
		return "success", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "success" {
		t.Fatalf("expected 'success', got %v", res)
	}
}

func TestRetryDefaultsDelayContextAndConveniences(t *testing.T) {
	if DefaultRetryIf(nil) || DefaultRetryIf(context.Canceled) || DefaultRetryIf(context.DeadlineExceeded) {
		t.Fatal("context errors should not retry")
	}
	if !DefaultRetryIf(errors.New("temporary")) {
		t.Fatal("plain errors should retry")
	}
	policy := NewPolicy(Config{MaxAttempts: -1, InitialDelay: -1, MaxDelay: -1, Multiplier: -1, Jitter: 2})
	if policy.config.MaxAttempts != 3 || policy.config.Jitter != 1 {
		t.Fatalf("normalized policy = %+v", policy.config)
	}
	noJitter := NewPolicy(Config{MaxAttempts: 1, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Multiplier: 10, Jitter: -1})
	if delay := noJitter.calculateDelay(3); delay != 2*time.Millisecond {
		t.Fatalf("capped delay = %s", delay)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitDelay(canceled, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitDelay canceled = %v", err)
	}
	if err := waitDelay(context.Background(), 0); err != nil {
		t.Fatalf("waitDelay zero = %v", err)
	}
	if err := DoWithConfig(context.Background(), Config{MaxAttempts: 1}, func() error { return nil }); err != nil {
		t.Fatalf("DoWithConfig() error = %v", err)
	}
	if err := Do(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got, err := DoWithResult(context.Background(), func() (any, error) { return "ok", nil }); err != nil || got != "ok" {
		t.Fatalf("DoWithResult() = %v err=%v", got, err)
	}
	for _, p := range []*Policy{AggressiveRetry(), GentleRetry(), NoRetry(), HTTPRetry(), DatabaseRetry()} {
		if p == nil || p.config.MaxAttempts <= 0 {
			t.Fatalf("bad preset: %+v", p)
		}
	}
	wrapped := &Retryable{Err: errors.New("retry")}
	if wrapped.Error() != "retry" || !errors.Is(wrapped, wrapped.Err) {
		t.Fatal("Retryable unwrap failed")
	}
	maxErr := &MaxAttemptsError{Attempts: 2, LastErr: errors.New("last")}
	if maxErr.Error() == "" || !errors.Is(maxErr, maxErr.LastErr) {
		t.Fatal("MaxAttemptsError unwrap failed")
	}
}

func TestDoWithResultExhaustionAndNonRetryable(t *testing.T) {
	policy := NewPolicy(Config{MaxAttempts: 2, InitialDelay: time.Millisecond})
	var attempts int
	got, err := policy.DoWithResult(context.Background(), func() (any, error) {
		attempts++
		return "last", errors.New("fail")
	})
	if got != nil || err == nil || attempts != 2 {
		t.Fatalf("DoWithResult exhaustion got=%v err=%v attempts=%d", got, err, attempts)
	}
	policy = NewPolicy(Config{MaxAttempts: 3, RetryIf: func(error) bool { return false }})
	got, err = policy.DoWithResult(context.Background(), func() (any, error) {
		return "ignored", errors.New("fatal")
	})
	if got != nil || err == nil || err.Error() != "fatal" {
		t.Fatalf("nonretryable result got=%v err=%v", got, err)
	}
}

func TestRetryWrappersSmartPredicateAndHTTPPredicate(t *testing.T) {
	base := errors.New("temporary")
	retryable := AsRetryable(base)
	if !SmartRetryIf(retryable) {
		t.Fatalf("expected retryable wrapper to retry")
	}
	if !errors.Is(retryable, base) || !strings.Contains(retryable.Error(), "temporary") {
		t.Fatalf("retryable wrapper did not preserve error")
	}
	nonRetryable := AsNonRetryable(base)
	if SmartRetryIf(nonRetryable) {
		t.Fatalf("expected non-retryable wrapper to stop")
	}
	if !errors.Is(nonRetryable, base) || !strings.Contains(nonRetryable.Error(), "temporary") {
		t.Fatalf("non-retryable wrapper did not preserve error")
	}
	if SmartRetryIf(nil) || SmartRetryIf(context.Canceled) || SmartRetryIf(context.DeadlineExceeded) {
		t.Fatalf("smart retry predicate should reject nil/context errors")
	}
	if !SmartRetryIf(base) {
		t.Fatalf("smart retry predicate should fall back to default")
	}
	httpRetry := HTTPRetry()
	if httpRetry.config.RetryIf(context.Canceled) || httpRetry.config.RetryIf(context.DeadlineExceeded) {
		t.Fatalf("HTTP retry should reject context errors")
	}
	if !httpRetry.config.RetryIf(base) {
		t.Fatalf("HTTP retry should accept non-context errors")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPolicy_Do_Success(b *testing.B) {
	ctx := context.Background()
	policy := NewPolicy(Config{MaxAttempts: 3})

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		_ = policy.calculateDelay(i%10 + 1)
	}
}

func BenchmarkPolicy_CalculateDelay_Parallel(b *testing.B) {
	policy := NewPolicy(DefaultConfig())

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			_ = policy.calculateDelay(i%5 + 1)
		}
	})
}

func TestJitterModesAndThunderingHerdSpread(t *testing.T) {
	const (
		clients = 1000
		base    = 100 * time.Millisecond
		bucket  = 10 * time.Millisecond
	)

	peakPerBucket := func(mode JitterMode, jitter float64) int {
		p := NewPolicy(Config{
			MaxAttempts:  3,
			InitialDelay: base,
			MaxDelay:     time.Second,
			Multiplier:   2.0,
			Jitter:       jitter,
			JitterMode:   mode,
		})
		counts := make(map[int64]int)
		peak := 0
		for range clients {
			d := p.calculateDelay(2) // nominal 200ms
			if d < 0 || d > time.Second {
				t.Fatalf("delay out of bounds for mode %d: %v", mode, d)
			}
			b := int64(d / bucket)
			counts[b]++
			if counts[b] > peak {
				peak = counts[b]
			}
		}
		return peak
	}

	peakSymmetric := peakPerBucket(JitterSymmetric, 0.1)
	peakFull := peakPerBucket(JitterFull, 1.0)
	peakEqual := peakPerBucket(JitterEqual, 1.0)
	peakDecorrelated := peakPerBucket(JitterDecorrelated, 1.0)

	// Narrow ±10% around 200ms lands in only 4 buckets of 10ms (~250/bucket),
	// whereas JitterFull spreads across 20 buckets (~50/bucket).
	if peakFull >= peakSymmetric {
		t.Fatalf("expected JitterFull peak (%d) < JitterSymmetric peak (%d)", peakFull, peakSymmetric)
	}
	if peakEqual <= 0 || peakDecorrelated <= 0 {
		t.Fatalf("unexpected peaks: equal=%d decorrelated=%d", peakEqual, peakDecorrelated)
	}
}

func TestRetryBudgetCapsAmplification(t *testing.T) {
	now := time.Now()
	budget := NewBudget(0.1, 0, 5) // 10% retry ratio, no time refill, initial burst 5
	budget.nowFn = func() time.Time { return now }

	policy := NewPolicy(Config{
		MaxAttempts:  4,
		InitialDelay: time.Nanosecond,
		Jitter:       0,
		Budget:       budget,
	})

	// Under 100% outage over 200 requests, without budget 4 attempts/req = 800 calls (4.0x).
	// With 10% budget + 5 initial capacity: at most 200 + 25 = 225 calls (<= 1.15x).
	totalCalls := 0
	budgetErrs := 0
	for range 200 {
		err := policy.Do(context.Background(), func() error {
			totalCalls++
			return errors.New("outage")
		})
		var bErr *BudgetExhaustedError
		if errors.As(err, &bErr) {
			budgetErrs++
			if bErr.Error() == "" || bErr.Unwrap() == nil {
				t.Fatal("BudgetExhaustedError unwrap/message failed")
			}
		}
	}
	if totalCalls > 230 {
		t.Fatalf("expected retry budget to cap total calls <= 230, got %d", totalCalls)
	}
	if budgetErrs == 0 {
		t.Fatal("expected BudgetExhaustedError to trigger during outage")
	}

	// Verify DoWithResult also enforces budget and nil-receiver safety.
	var nilBudget *Budget
	nilBudget.RecordRequest()
	if !nilBudget.AllowRetry() {
		t.Fatal("nil budget should allow retry")
	}

	b2 := NewBudget(-1, -1, 0)
	b2.tokens = 0
	p2 := NewPolicy(Config{
		MaxAttempts:  3,
		InitialDelay: time.Nanosecond,
		Budget:       b2,
		OnRetry:      func(int, error, time.Duration) {},
	})
	_, err := p2.DoWithResult(context.Background(), func() (any, error) {
		return nil, errors.New("boom")
	})
	var bErr *BudgetExhaustedError
	if !errors.As(err, &bErr) {
		t.Fatalf("expected BudgetExhaustedError from DoWithResult, got %v", err)
	}

	// Time refill restores tokens.
	now2 := time.Now()
	b3 := NewBudget(0.1, 10, 5)
	b3.nowFn = func() time.Time { return now2 }
	b3.tokens = 0
	now2 = now2.Add(200 * time.Millisecond)
	if !b3.AllowRetry() {
		t.Fatal("expected refill to allow retry")
	}
}
