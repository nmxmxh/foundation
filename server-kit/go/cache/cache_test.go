package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Functional Tests
// ---------------------------------------------------------------------------

func TestMemoryBackend_SetGetDelete(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	if err := backend.Set(ctx, "key1", []byte("value1"), 5*time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	data, err := backend.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(data) != "value1" {
		t.Fatalf("expected 'value1', got '%s'", string(data))
	}
	if err := backend.Delete(ctx, "key1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	data, err = backend.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil after delete, got %v", data)
	}
}

func TestMemoryBackend_TTLExpiry(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	if err := backend.Set(ctx, "expiring", []byte("data"), 50*time.Millisecond); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	data, _ := backend.Get(ctx, "expiring")
	if data == nil {
		t.Fatal("expected data before expiry")
	}

	time.Sleep(60 * time.Millisecond)

	data, _ = backend.Get(ctx, "expiring")
	if data != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

func TestMemoryBackend_DeletePattern(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_ = backend.Set(ctx, fmt.Sprintf("user:%d", i), []byte("data"), time.Hour)
	}
	_ = backend.Set(ctx, "org:1", []byte("data"), time.Hour)

	if err := backend.DeletePattern(ctx, "user:*"); err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		data, _ := backend.Get(ctx, fmt.Sprintf("user:%d", i))
		if data != nil {
			t.Fatalf("expected user:%d to be deleted", i)
		}
	}
	data, _ := backend.Get(ctx, "org:1")
	if data == nil {
		t.Fatal("org:1 should not be deleted")
	}
}

func TestMemoryBackend_Exists(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	exists, _ := backend.Exists(ctx, "missing")
	if exists {
		t.Fatal("expected false for missing key")
	}

	_ = backend.Set(ctx, "present", []byte("data"), time.Hour)
	exists, _ = backend.Exists(ctx, "present")
	if !exists {
		t.Fatal("expected true for present key")
	}
}

func TestMemoryBackend_CloseIsIdempotent(t *testing.T) {
	backend := NewMemoryBackend()
	if err := backend.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestCache_GetOrSet(t *testing.T) {
	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: 5 * time.Minute,
	})
	ctx := context.Background()
	computeCount := 0

	result, err := GetOrSet(ctx, c, "computed", func() (string, error) {
		computeCount++
		return "computed_value", nil
	})
	if err != nil {
		t.Fatalf("GetOrSet failed: %v", err)
	}
	if result != "computed_value" {
		t.Fatalf("expected 'computed_value', got '%s'", result)
	}
	if computeCount != 1 {
		t.Fatalf("expected compute to be called once, got %d", computeCount)
	}

	// Second call should hit cache
	result, err = GetOrSet(ctx, c, "computed", func() (string, error) {
		computeCount++
		return "should_not_reach", nil
	})
	if err != nil {
		t.Fatalf("GetOrSet second call failed: %v", err)
	}
	if result != "computed_value" {
		t.Fatalf("expected cached 'computed_value', got '%s'", result)
	}
	if computeCount != 1 {
		t.Fatalf("expected compute not called again, got %d", computeCount)
	}
}

func TestInvalidator_TagAndInvalidate(t *testing.T) {
	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: 5 * time.Minute,
	})
	ctx := context.Background()
	inv := NewInvalidator(c)

	_ = c.Set(ctx, "user:1", "alice")
	_ = c.Set(ctx, "user:2", "bob")
	_ = c.Set(ctx, "org:1", "acme")

	inv.Tag("user:1", "users")
	inv.Tag("user:2", "users")
	inv.Tag("org:1", "orgs")

	if err := inv.InvalidateTag(ctx, "users"); err != nil {
		t.Fatalf("InvalidateTag failed: %v", err)
	}

	var val string
	err := c.Get(ctx, "user:1", &val)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after invalidation, got %v", err)
	}
	err = c.Get(ctx, "org:1", &val)
	if err != nil {
		t.Fatalf("org:1 should still exist: %v", err)
	}
}

func TestCacheKey(t *testing.T) {
	key := CacheKey("user", 123, "profile")
	if key != "user:123:profile" {
		t.Fatalf("expected 'user:123:profile', got '%s'", key)
	}
}

func TestCache_Prefix(t *testing.T) {
	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: time.Minute,
		Prefix:     "app:",
	})
	ctx := context.Background()

	_ = c.Set(ctx, "key", "value")
	var val string
	if err := c.Get(ctx, "key", &val); err != nil {
		t.Fatalf("Get with prefix failed: %v", err)
	}
	if val != "value" {
		t.Fatalf("expected 'value', got '%s'", val)
	}
}

func TestCache_HitMissCallbacks(t *testing.T) {
	var hits, misses atomic.Int32

	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: time.Minute,
		OnHit:      func(_ string) { hits.Add(1) },
		OnMiss:     func(_ string) { misses.Add(1) },
	})
	ctx := context.Background()

	var val string
	_ = c.Get(ctx, "missing", &val) // miss
	_ = c.Set(ctx, "found", "data")
	_ = c.Get(ctx, "found", &val) // hit

	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit, got %d", hits.Load())
	}
	if misses.Load() != 1 {
		t.Fatalf("expected 1 miss, got %d", misses.Load())
	}
}

// ---------------------------------------------------------------------------
// Concurrency Tests
// ---------------------------------------------------------------------------

func TestMemoryBackend_ConcurrentAccess(t *testing.T) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	const goroutines = 100
	const opsPerGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := fmt.Sprintf("key:%d:%d", id, i%10)
				_ = backend.Set(ctx, key, []byte(fmt.Sprintf("val:%d", i)), time.Minute)
				_, _ = backend.Get(ctx, key)
				_, _ = backend.Exists(ctx, key)
				if i%5 == 0 {
					_ = backend.Delete(ctx, key)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestGetOrSet_ConcurrentStampede(t *testing.T) {
	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: 5 * time.Minute,
	})
	ctx := context.Background()
	var computeCount atomic.Int32
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = GetOrSet(ctx, c, "stampede-key", func() (string, error) {
				computeCount.Add(1)
				time.Sleep(10 * time.Millisecond)
				return "value", nil
			})
		}()
	}
	wg.Wait()

	// Note: without singleflight, multiple goroutines will compute.
	// This test documents the current behavior - cache stampede is possible.
	if computeCount.Load() < 1 {
		t.Fatal("expected at least one compute call")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkMemoryBackend_Set(b *testing.B) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	data := []byte(`{"user_id":123,"name":"benchmark_user","email":"bench@test.com"}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = backend.Set(ctx, fmt.Sprintf("key:%d", i), data, 5*time.Minute)
	}
}

func BenchmarkMemoryBackend_Get(b *testing.B) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	data := []byte(`{"user_id":123}`)
	_ = backend.Set(ctx, "bench-key", data, time.Hour)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = backend.Get(ctx, "bench-key")
	}
}

func BenchmarkMemoryBackend_SetGet_Parallel(b *testing.B) {
	backend := NewMemoryBackend()
	ctx := context.Background()
	data := []byte(`{"user_id":123}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key:%d", i%1000)
			_ = backend.Set(ctx, key, data, 5*time.Minute)
			_, _ = backend.Get(ctx, key)
			i++
		}
	})
}

func BenchmarkGetOrSet(b *testing.B) {
	c := New(Config{
		Backend:    NewMemoryBackend(),
		DefaultTTL: 5 * time.Minute,
	})
	ctx := context.Background()

	// Pre-warm cache
	_, _ = GetOrSet(ctx, c, "bench-key", func() (string, error) {
		return "cached_value", nil
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = GetOrSet(ctx, c, "bench-key", func() (string, error) {
			return "should_not_compute", nil
		})
	}
}

func BenchmarkCacheKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = CacheKey("user", 123, "profile", "v2")
	}
}

func BenchmarkMemoryBackend_DeletePattern(b *testing.B) {
	backend := NewMemoryBackend()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := 0; j < 100; j++ {
			_ = backend.Set(ctx, fmt.Sprintf("prefix:%d", j), []byte("data"), time.Hour)
		}
		b.StartTimer()
		_ = backend.DeletePattern(ctx, "prefix:*")
	}
}
