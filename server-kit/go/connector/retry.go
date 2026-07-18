package connector

import (
	"math"
	"math/rand"
	"time"
)

// RetryConfig controls the request-level retry policy applied by Connector.Call.
// Backoff is exponential with full jitter, capped at MaxDelay.
type RetryConfig struct {
	MaxAttempts int           // total attempts including the first (1 disables retry; <=0 adopts DefaultRetryConfig's count)
	BaseDelay   time.Duration // delay before the first retry
	MaxDelay    time.Duration // upper bound on any single backoff
	Multiplier  float64       // growth factor per attempt (default 2.0)
}

// DefaultRetryConfig returns a conservative retry policy.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		Multiplier:  2.0,
	}
}

func (c RetryConfig) normalized() RetryConfig {
	// The zero value adopts the default policy (Config documents this);
	// MaxAttempts: 1 is the explicit way to disable retry.
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultRetryConfig().MaxAttempts
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 50 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 2 * time.Second
	}
	if c.Multiplier < 1 {
		c.Multiplier = 2.0
	}
	return c
}

// backoff computes the delay before the given retry attempt (1-based: attempt 1
// is the delay before the second try) using exponential growth with full jitter.
func (c RetryConfig) backoff(attempt int, rng *rand.Rand) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exp := float64(c.BaseDelay) * math.Pow(c.Multiplier, float64(attempt-1))
	if exp > float64(c.MaxDelay) {
		exp = float64(c.MaxDelay)
	}
	// Full jitter: uniform in [0, exp].
	if rng != nil {
		return time.Duration(rng.Int63n(int64(exp) + 1))
	}
	return time.Duration(exp)
}
