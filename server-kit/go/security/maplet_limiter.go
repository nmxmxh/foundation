package security

import (
	"net/http"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

// MapletRateLimiter is a zero-allocation, bounded-memory rate limiter backed
// by a metadata.CountingMaplet. It avoids heap-allocated string maps and timestamp
// slices under heavy traffic or DDoS attacks.
type MapletRateLimiter struct {
	mu          sync.Mutex
	maplet      *metadata.CountingMaplet
	limit       uint8
	window      time.Duration
	windowStart time.Time
}

// NewMapletRateLimiter creates a rate limiter with bounded memory capacity for expectedKeys.
func NewMapletRateLimiter(limit uint8, window time.Duration, expectedKeys int) *MapletRateLimiter {
	if limit == 0 {
		limit = 100
	}
	if window <= 0 {
		window = time.Minute
	}
	if expectedKeys <= 0 {
		expectedKeys = 10000
	}
	return &MapletRateLimiter{
		maplet:      metadata.NewCountingMaplet(expectedKeys),
		limit:       limit,
		window:      window,
		windowStart: time.Now(),
	}
}

// Allow reports whether a request for key is permitted under the rate limit.
func (rl *MapletRateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.windowStart) >= rl.window {
		rl.maplet.Reset()
		rl.windowStart = now
	}

	count := rl.maplet.Increment(key)
	return count <= rl.limit
}

// Limit wraps next with HTTP rate limiting using client request fingerprints.
func (rl *MapletRateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fingerprint := requestFingerprint(r)
		if !rl.Allow(fingerprint) {
			domainerr.WriteHTTP(w, domainerr.RateLimited("rate_limit_exceeded", "rate limit exceeded"), domainerr.ResponseOptions{})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MemoryBytes returns the raw memory size in bytes of the backing maplet slab.
func (rl *MapletRateLimiter) MemoryBytes() int {
	return rl.maplet.MemoryBytes()
}

// WindowRemaining returns remaining time until current rate limit window resets.
func (rl *MapletRateLimiter) WindowRemaining() time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	elapsed := time.Since(rl.windowStart)
	if elapsed >= rl.window {
		return 0
	}
	return rl.window - elapsed
}
