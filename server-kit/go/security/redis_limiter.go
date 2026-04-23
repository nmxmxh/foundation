package security

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// RedisRateLimiter is a distributed rate limiter using Redis.
type RedisRateLimiter struct {
	client redis.Client
	limit  int
	window time.Duration
}

func NewRedisRateLimiter(client redis.Client, limit int, window time.Duration) *RedisRateLimiter {
	if limit <= 0 {
		limit = 200
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RedisRateLimiter{
		client: client,
		limit:  limit,
		window: window,
	}
}

func (rl *RedisRateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl.client == nil {
			next.ServeHTTP(w, r)
			return
		}

		ip := GetClientIP(r)
		if !rl.Allow(r.Context(), ip) {
			domainerr.WriteHTTP(w, domainerr.RateLimited("rate_limit_exceeded", "rate limit exceeded"), domainerr.ResponseOptions{})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) bool {
	redisKey := fmt.Sprintf("ratelimit:%s", key)
	count, err := rl.client.Incr(ctx, redisKey)
	if err != nil {
		// Fallback to allow if Redis is down
		return true
	}

	if count == 1 {
		_, _ = rl.client.Expire(ctx, redisKey, rl.window)
	}

	return int(count) <= rl.limit
}
