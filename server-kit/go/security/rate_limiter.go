package security

import (
	"net/http"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

const (
	// maxRateLimiterKeys bounds tracked clients per limiter (CP-02).
	maxRateLimiterKeys = 10000

	// rateLimiterShards splits the key space so requests do not share one lock.
	rateLimiterShards = 16

	// rateSubBuckets splits the window. More buckets track time more finely;
	// each key always costs rateSubBuckets+1 counters, whatever the limit is.
	rateSubBuckets = 16
)

// rateState is the fixed-size sliding window for one key. ids hold absolute
// sub-bucket numbers (time / bucket width); counts hold admitted requests.
type rateState struct {
	ids    [rateSubBuckets + 1]int64
	counts [rateSubBuckets + 1]uint32
}

type rateShard struct {
	mu   sync.Mutex
	keys map[string]*rateState
	ops  uint32
}

// RateLimiter is a sliding-window rate limiter keyed by request fingerprint.
//
// Keys are exact: two clients never share a counter. Each key holds a fixed
// window of rateSubBuckets+1 counters, so memory and time per request do not
// grow with the limit. The count is conservative. It never admits more than
// limit requests in any window-length interval, and recovery can lag by up to
// window/rateSubBuckets. Rejected requests are not counted.
type RateLimiter struct {
	shards      [rateLimiterShards]rateShard
	limit       uint32
	window      time.Duration
	bucketWidth int64
	shardCap    int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 200
	}
	if window <= 0 {
		window = time.Minute
	}
	width := int64(window) / rateSubBuckets
	if width <= 0 {
		width = 1
	}
	rl := &RateLimiter{
		limit:       uint32(min(limit, int(^uint32(0)>>1))), // #nosec G115 -- clamped to uint32 range
		window:      window,
		bucketWidth: width,
		shardCap:    maxRateLimiterKeys / rateLimiterShards,
	}
	for i := range rl.shards {
		rl.shards[i].keys = make(map[string]*rateState)
	}
	return rl
}

func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fingerprint := requestFingerprint(r)
		if !rl.Allow(fingerprint) {
			domainerr.WriteHTTP(w, domainerr.RateLimited("rate_limit_exceeded", "rate limit exceeded"), domainerr.ResponseOptions{})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) shardFor(key string) *rateShard {
	// FNV-1a. The key is already a hash or an IP, so this only spreads load.
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &rl.shards[h%rateLimiterShards]
}

// Allow reports whether one more request for key fits the window, and records it if so.
func (rl *RateLimiter) Allow(key string) bool {
	return rl.allowAt(key, time.Now().UnixNano())
}

func (rl *RateLimiter) allowAt(key string, nowNanos int64) bool {
	shard := rl.shardFor(key)
	current := nowNanos / rl.bucketWidth
	oldest := current - rateSubBuckets

	shard.mu.Lock()
	defer shard.mu.Unlock()

	shard.ops++
	if shard.ops%256 == 0 {
		rl.pruneShardLocked(shard, oldest)
	}

	state, ok := shard.keys[key]
	if !ok {
		if len(shard.keys) >= rl.shardCap {
			rl.pruneShardLocked(shard, oldest)
			if len(shard.keys) >= rl.shardCap {
				// Fail closed for new keys at the bound. Known keys keep their budget.
				return false
			}
		}
		state = &rateState{}
		shard.keys[key] = state
	}

	var used uint32
	for i := range state.ids {
		if state.ids[i] >= oldest && state.ids[i] <= current {
			used += state.counts[i]
		}
	}
	if used >= rl.limit {
		return false
	}
	slot := int(current % int64(len(state.ids)))
	if state.ids[slot] != current {
		state.ids[slot] = current
		state.counts[slot] = 0
	}
	state.counts[slot]++
	return true
}

// pruneShardLocked drops keys with no admitted request inside the window.
func (rl *RateLimiter) pruneShardLocked(shard *rateShard, oldest int64) {
	for key, state := range shard.keys {
		live := false
		for i := range state.ids {
			if state.counts[i] > 0 && state.ids[i] >= oldest {
				live = true
				break
			}
		}
		if !live {
			delete(shard.keys, key)
		}
	}
}

// Prune removes expired entries from every shard.
func (rl *RateLimiter) Prune(now time.Time) {
	oldest := now.UnixNano()/rl.bucketWidth - rateSubBuckets
	for i := range rl.shards {
		shard := &rl.shards[i]
		shard.mu.Lock()
		rl.pruneShardLocked(shard, oldest)
		shard.mu.Unlock()
	}
}

// KeyCount returns the count of keys currently stored in memory.
func (rl *RateLimiter) KeyCount() int {
	total := 0
	for i := range rl.shards {
		shard := &rl.shards[i]
		shard.mu.Lock()
		total += len(shard.keys)
		shard.mu.Unlock()
	}
	return total
}
