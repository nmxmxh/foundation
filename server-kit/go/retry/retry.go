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
	"math/rand"
	"sync"
	"time"
)

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
	rng    *rand.Rand
	rngMu  sync.Mutex
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
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// calculateDelay calculates the delay for a given attempt.
func (p *Policy) calculateDelay(attempt int) time.Duration {
	// Exponential backoff: initialDelay * multiplier^(attempt-1)
	delay := float64(p.config.InitialDelay) * math.Pow(p.config.Multiplier, float64(attempt-1))

	// Apply max delay cap
	if delay > float64(p.config.MaxDelay) {
		delay = float64(p.config.MaxDelay)
	}

	// Apply jitter
	if p.config.Jitter > 0 {
		jitterRange := delay * p.config.Jitter
		p.rngMu.Lock()
		random := p.rng.Float64()
		p.rngMu.Unlock()
		jitter := (random*2 - 1) * jitterRange // -jitterRange to +jitterRange
		delay += jitter
	}

	// Ensure non-negative
	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Do executes a function with retries according to the policy.
func (p *Policy) Do(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= p.config.MaxAttempts; attempt++ {
		// Check context before attempting
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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
func (p *Policy) DoWithResult(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	var result interface{}
	var lastErr error

	for attempt := 1; attempt <= p.config.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
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
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
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
func DoWithResult(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
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
