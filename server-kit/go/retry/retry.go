// Package retry provides standardized retry policies with exponential backoff,
// jitter, and configurable max attempts.
//
// Usage:
//
//	policy := retry.NewPolicy(retry.Config{
//	    MaxAttempts: 3,
//	    InitialDelay: 100 * time.Millisecond,
//	    MaxDelay: 5 * time.Second,
//	    Multiplier: 2.0,
//	    Jitter: 0.1,
//	})
//
//	result, err := retry.Do(ctx, policy, func() (interface{}, error) {
//	    return httpClient.Do(req)
//	})
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// JitterMode selects the probability distribution for backoff jitter.
type JitterMode int

const (
	// JitterSymmetric spreads the delay uniformly in [delay*(1-Jitter), delay*(1+Jitter)],
	// clamped to [0, MaxDelay].
	JitterSymmetric JitterMode = iota
	// JitterFull draws uniformly from [0, delay], decorrelating synchronized retry waves.
	JitterFull
	// JitterEqual draws uniformly from [delay*(1-Jitter/2), delay].
	JitterEqual
	// JitterDecorrelated draws uniformly from [InitialDelay, min(MaxDelay, prevDelay*3)].
	JitterDecorrelated
)

// Budget enforces a token-ratio cap on retries relative to total requests so that
// cascading failures cannot amplify load past (1 + Ratio) * baseline traffic.
//
// Each first attempt deposits Ratio tokens (up to Capacity), and a floor refill rate
// guarantees that low-traffic callers and unit tests can always retry. Each retry
// consumes 1 token.
type Budget struct {
	mu       sync.Mutex
	ratio    float64
	minPerS  float64
	capacity float64
	tokens   float64
	lastTime time.Time
	nowFn    func() time.Time
}

// NewBudget constructs a retry budget with the given retry-to-request ratio
// (for example, 0.1 for at most 10% retry overhead) and minimum retries per second floor.
func NewBudget(ratio float64, minRetriesPerSec float64, capacity float64) *Budget {
	if ratio <= 0 {
		ratio = 0.1
	}
	if minRetriesPerSec < 0 {
		minRetriesPerSec = 0
	}
	if capacity < 1 {
		capacity = math.Max(10, minRetriesPerSec*2)
	}
	return &Budget{
		ratio:    ratio,
		minPerS:  minRetriesPerSec,
		capacity: capacity,
		tokens:   capacity,
		lastTime: time.Now(),
		nowFn:    time.Now,
	}
}

// RecordRequest deposits ratio tokens for an incoming initial request.
func (b *Budget) RecordRequest() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	b.tokens = math.Min(b.capacity, b.tokens+b.ratio)
}

// AllowRetry attempts to consume one retry token and reports whether a retry is permitted.
func (b *Budget) AllowRetry() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

func (b *Budget) refillLocked() {
	now := b.nowFn()
	elapsed := now.Sub(b.lastTime).Seconds()
	if elapsed > 0 && b.minPerS > 0 {
		b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.minPerS)
	}
	if elapsed > 0 {
		b.lastTime = now
	}
}

// BudgetExhaustedError is returned when the retry budget denies further retries.
type BudgetExhaustedError struct {
	Attempts int
	LastErr  error
}

func (e *BudgetExhaustedError) Error() string {
	return "retry budget exhausted: " + e.LastErr.Error()
}

func (e *BudgetExhaustedError) Unwrap() error {
	return e.LastErr
}

// Config holds retry policy configuration.
type Config struct {
	// MaxAttempts is the maximum number of attempts (including the initial one).
	// 0 or 1 means no retries. Default: 3
	MaxAttempts int

	// InitialDelay is the delay before the first retry. Default: 100ms
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries. Default: 30s
	MaxDelay time.Duration

	// Multiplier is the factor by which the delay increases. Default: 2.0
	Multiplier float64

	// Jitter is the random factor added to delays (0.0-1.0). Default: 0.1
	// A jitter of 0.1 means the delay can vary by ±10%.
	Jitter float64

	// RetryIf determines whether an error should trigger a retry.
	// Default: retry all non-nil errors except context errors.
	RetryIf func(error) bool

	// OnRetry is called before each retry with the attempt number and error.
	OnRetry func(attempt int, err error, delay time.Duration)

	// JitterMode selects the backoff randomization strategy. Default: JitterSymmetric.
	JitterMode JitterMode

	// Budget bounds aggregate retry amplification across requests sharing this policy.
	Budget *Budget
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		RetryIf:      DefaultRetryIf,
	}
}

// DefaultRetryIf is the default retry predicate.
// It retries all errors except context cancellation/deadline.
func DefaultRetryIf(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// Policy represents a retry policy.
type Policy struct {
	config Config
}

// NewPolicy creates a new retry policy.
func NewPolicy(cfg Config) *Policy {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}
	if cfg.Jitter < 0 {
		cfg.Jitter = 0
	}
	if cfg.Jitter > 1 {
		cfg.Jitter = 1
	}
	if cfg.RetryIf == nil {
		cfg.RetryIf = DefaultRetryIf
	}

	return &Policy{
		config: cfg,
	}
}

// calculateDelay calculates the delay for a given attempt using lock-free math/rand/v2.
func (p *Policy) calculateDelay(attempt int) time.Duration {
	delay := float64(p.config.InitialDelay) * math.Pow(p.config.Multiplier, float64(attempt-1))
	maxDelay := float64(p.config.MaxDelay)
	if delay > maxDelay {
		delay = maxDelay
	}

	if p.config.Jitter > 0 {
		// #nosec G404 -- retry jitter spreads load and is not a cryptographic secret.
		u := rand.Float64()
		switch p.config.JitterMode {
		case JitterFull:
			delay = u * delay
		case JitterEqual:
			half := delay * (p.config.Jitter * 0.5)
			delay = (delay - half) + u*half
		case JitterDecorrelated:
			base := float64(p.config.InitialDelay)
			upper := math.Min(maxDelay, math.Max(base, delay*3.0))
			delay = base + u*(upper-base)
		default:
			jitterRange := delay * p.config.Jitter
			delay += (u*2 - 1) * jitterRange
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Do executes a function with retries according to the policy.
func (p *Policy) Do(ctx context.Context, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.config.Budget != nil {
		p.config.Budget.RecordRequest()
	}
	var lastErr error

	for attempt := 1; attempt <= p.config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry
		if !p.config.RetryIf(err) {
			return err
		}

		// Check if we've exhausted attempts
		if attempt >= p.config.MaxAttempts {
			break
		}

		if p.config.Budget != nil && !p.config.Budget.AllowRetry() {
			return &BudgetExhaustedError{
				Attempts: attempt,
				LastErr:  lastErr,
			}
		}

		// Calculate delay
		delay := p.calculateDelay(attempt)

		// Call OnRetry callback
		if p.config.OnRetry != nil {
			p.config.OnRetry(attempt, err, delay)
		}

		// Wait before retry
		if err := waitDelay(ctx, delay); err != nil {
			return err
		}
	}

	return &MaxAttemptsError{
		Attempts: p.config.MaxAttempts,
		LastErr:  lastErr,
	}
}

// DoWithResult executes a function that returns a value with retries.
func (p *Policy) DoWithResult(ctx context.Context, fn func() (any, error)) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if p.config.Budget != nil {
		p.config.Budget.RecordRequest()
	}
	var result any
	var lastErr error

	for attempt := 1; attempt <= p.config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		res, err := fn()
		if err == nil {
			return res, nil
		}

		lastErr = err

		if !p.config.RetryIf(err) {
			return nil, err
		}

		if attempt >= p.config.MaxAttempts {
			break
		}

		if p.config.Budget != nil && !p.config.Budget.AllowRetry() {
			return nil, &BudgetExhaustedError{
				Attempts: attempt,
				LastErr:  lastErr,
			}
		}

		delay := p.calculateDelay(attempt)

		if p.config.OnRetry != nil {
			p.config.OnRetry(attempt, err, delay)
		}

		if err := waitDelay(ctx, delay); err != nil {
			return nil, err
		}
	}

	return result, &MaxAttemptsError{
		Attempts: p.config.MaxAttempts,
		LastErr:  lastErr,
	}
}

func waitDelay(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// MaxAttemptsError is returned when all retry attempts have been exhausted.
type MaxAttemptsError struct {
	Attempts int
	LastErr  error
}

func (e *MaxAttemptsError) Error() string {
	return "max retry attempts exceeded: " + e.LastErr.Error()
}

func (e *MaxAttemptsError) Unwrap() error {
	return e.LastErr
}

// Convenience functions

// Do executes a function with the default retry policy.
func Do(ctx context.Context, fn func() error) error {
	return NewPolicy(DefaultConfig()).Do(ctx, fn)
}

// DoWithConfig executes a function with a custom retry configuration.
func DoWithConfig(ctx context.Context, cfg Config, fn func() error) error {
	return NewPolicy(cfg).Do(ctx, fn)
}

// DoWithResult executes a function that returns a value with the default retry policy.
func DoWithResult(ctx context.Context, fn func() (any, error)) (any, error) {
	return NewPolicy(DefaultConfig()).DoWithResult(ctx, fn)
}

// Preset policies for common use cases

// AggressiveRetry returns a policy for critical operations that should be retried aggressively.
func AggressiveRetry() *Policy {
	return NewPolicy(Config{
		MaxAttempts:  5,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   1.5,
		Jitter:       0.2,
	})
}

// GentleRetry returns a policy for non-critical operations with longer delays.
func GentleRetry() *Policy {
	return NewPolicy(Config{
		MaxAttempts:  3,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     60 * time.Second,
		Multiplier:   3.0,
		Jitter:       0.1,
	})
}

// NoRetry returns a policy that doesn't retry.
func NoRetry() *Policy {
	return NewPolicy(Config{
		MaxAttempts: 1,
	})
}

// HTTPRetry returns a policy suitable for HTTP requests.
func HTTPRetry() *Policy {
	return NewPolicy(Config{
		MaxAttempts:  3,
		InitialDelay: 200 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		RetryIf: func(err error) bool {
			// Don't retry context errors
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false
			}
			// Retry network errors
			return true
		},
	})
}

// DatabaseRetry returns a policy suitable for database operations.
func DatabaseRetry() *Policy {
	return NewPolicy(Config{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
	})
}

// Retryable wraps an error to indicate it should be retried.
type Retryable struct {
	Err error
}

func (r *Retryable) Error() string {
	return r.Err.Error()
}

func (r *Retryable) Unwrap() error {
	return r.Err
}

// AsRetryable wraps an error as retryable.
func AsRetryable(err error) error {
	return &Retryable{Err: err}
}

// NonRetryable wraps an error to indicate it should NOT be retried.
type NonRetryable struct {
	Err error
}

func (r *NonRetryable) Error() string {
	return r.Err.Error()
}

func (r *NonRetryable) Unwrap() error {
	return r.Err
}

// AsNonRetryable wraps an error as non-retryable.
func AsNonRetryable(err error) error {
	return &NonRetryable{Err: err}
}

// SmartRetryIf returns a retry predicate that respects Retryable/NonRetryable wrappers.
func SmartRetryIf(err error) bool {
	if err == nil {
		return false
	}

	// Check for NonRetryable wrapper
	var nonRetryable *NonRetryable
	if errors.As(err, &nonRetryable) {
		return false
	}

	// Check for Retryable wrapper
	var retryable *Retryable
	if errors.As(err, &retryable) {
		return true
	}

	// Default behavior
	return DefaultRetryIf(err)
}
