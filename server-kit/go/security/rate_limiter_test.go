package security

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// TestRateLimiterNeverExceedsLimitInAnyWindow checks the sliding window against
// an exact log. In every window-length interval, admitted requests stay at or
// under the limit. A key that was idle for window+window/16 is fully reset.
func TestRateLimiterNeverExceedsLimitInAnyWindow(t *testing.T) {
	const limit = 7
	window := 160 * time.Millisecond
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := range 50 {
		rl := NewRateLimiter(limit, window)
		var admitted []int64
		now := int64(1_000_000_000)
		for range 2000 {
			now += rng.Int64N(int64(window) / 20)
			if rl.allowAt("k", now) {
				admitted = append(admitted, now)
			}
		}
		for i, at := range admitted {
			inWindow := 0
			for j := i; j >= 0 && at-admitted[j] < int64(window); j-- {
				inWindow++
			}
			if inWindow > limit {
				t.Fatalf("trial %d: %d admitted within one window ending at %d, limit %d", trial, inWindow, at, limit)
			}
		}
		if len(admitted) < 2000/40 {
			t.Fatalf("trial %d: only %d admitted; limiter is far stricter than the window", trial, len(admitted))
		}
		idle := now + int64(window) + int64(window)/rateSubBuckets + 1
		for i := range limit {
			if !rl.allowAt("k", idle) {
				t.Fatalf("trial %d: request %d rejected after a full idle window", trial, i)
			}
		}
	}
}

func TestRateLimiterKeysAreExactAndBounded(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	for i := range maxRateLimiterKeys * 2 {
		rl.Allow(fmt.Sprintf("flood-%d", i))
	}
	if got := rl.KeyCount(); got > maxRateLimiterKeys {
		t.Fatalf("tracked %d keys, bound is %d", got, maxRateLimiterKeys)
	}
	// Distinct clients never share a counter, so a known client keeps its own budget.
	known := NewRateLimiter(1, time.Minute)
	if !known.Allow("alice") {
		t.Fatal("alice first request")
	}
	for i := range 5000 {
		if !known.Allow(fmt.Sprintf("other-%d", i)) {
			break
		}
	}
	if known.Allow("alice") {
		t.Fatal("alice must stay limited; another client's traffic must not reset her counter")
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := NewRateLimiter(50, 50*time.Millisecond)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 2000 {
				rl.Allow(fmt.Sprintf("c-%d-%d", w, i%64))
				if i%500 == 0 {
					rl.Prune(time.Now())
					_ = rl.KeyCount()
				}
			}
		}()
	}
	wg.Wait()
}

func TestRateLimiterAllowDoesNotAllocateForKnownKeys(t *testing.T) {
	rl := NewRateLimiter(1_000_000, time.Minute)
	rl.Allow("steady")
	allocs := testing.AllocsPerRun(1000, func() { rl.Allow("steady") })
	if allocs != 0 {
		t.Fatalf("Allow on a known key allocated %.1f times, want 0", allocs)
	}
}

func BenchmarkRateLimiterAllow_HotKey(b *testing.B) {
	rl := NewRateLimiter(200, time.Minute)
	b.ReportAllocs()
	for b.Loop() {
		rl.Allow("hot")
	}
}

func BenchmarkRateLimiterAllow_ManyKeysParallel(b *testing.B) {
	rl := NewRateLimiter(200, time.Minute)
	keys := make([]string, 4096)
	for i := range keys {
		keys[i] = fmt.Sprintf("client-%d", i)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			rl.Allow(keys[i&(len(keys)-1)])
			i++
		}
	})
}
