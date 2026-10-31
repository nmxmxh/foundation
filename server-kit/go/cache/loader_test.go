package cache

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestContextLoaderSharesWorkAndCancelsWaitersIndependently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := NewMemoryBackend()
		defer backend.Close()
		c := New(Config{Backend: backend})
		started, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		load := func(ctx context.Context) (string, error) {
			calls.Add(1)
			close(started)
			select {
			case <-release:
				return "source", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		first := make(chan error, 1)
		second := make(chan string, 1)
		go func() { _, err := GetOrSetContext(ctx, c, "org:one", load); first <- err }()
		<-started
		go func() {
			value, err := GetOrSetContext(t.Context(), c, "org:one", load)
			if err != nil {
				t.Error(err)
			}
			second <- value
		}()
		synctest.Wait()
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("first: %v", err)
		}
		close(release)
		if got := <-second; got != "source" {
			t.Fatalf("second: %q", got)
		}
		value, err := GetOrSetContext(t.Context(), c, "org:one", load)
		if err != nil || value != "source" || calls.Load() != 1 {
			t.Fatalf("warm value=%q calls=%d error=%v", value, calls.Load(), err)
		}
	})
}

func TestContextLoaderBoundsAndLateResultPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := NewMemoryBackend()
		defer backend.Close()
		c := NewWithLoadPolicy(Config{Backend: backend}, LoadPolicy{Timeout: time.Second, MaxInflight: 1, MaxWaiters: 1})
		release, started := make(chan struct{}), make(chan struct{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := GetOrSetContext(ctx, c, "one", func(context.Context) (int, error) { close(started); <-release; return 9, nil })
			done <- err
		}()
		<-started
		for _, key := range []string{"one", "two"} {
			if _, err := GetOrSetContext(t.Context(), c, key, func(context.Context) (int, error) { t.Error("capacity admitted work"); return 0, nil }); !errors.Is(err, ErrLoadBusy) {
				t.Fatalf("capacity: %v", err)
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
		if _, err := GetOrSetContext(t.Context(), c, "one", func(context.Context) (int, error) { return 0, nil }); !errors.Is(err, ErrLoadBusy) {
			t.Fatalf("cancelled owner: %v", err)
		}
		close(release)
		synctest.Wait()
		var value int
		if err := c.Get(t.Context(), "one", &value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("late result published: %d %v", value, err)
		}
		got, err := GetOrSetContext(t.Context(), c, "one", func(context.Context) (int, error) { return 3, nil })
		if got != 3 || err != nil {
			t.Fatalf("capacity not released: %d %v", got, err)
		}
	})
}

func TestContextLoaderDeadlineErrorsAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := NewMemoryBackend()
		defer backend.Close()
		c := NewWithLoadPolicy(Config{Backend: backend}, LoadPolicy{Timeout: time.Millisecond})
		_, err := GetOrSetContext(t.Context(), c, "timeout", func(ctx context.Context) (int, error) { <-ctx.Done(); return 1, nil })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout: %v", err)
		}
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := GetOrSetContext(ctx, c, "cancelled", func(context.Context) (int, error) { t.Error("cancelled load ran"); return 0, nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
		sourceErr := errors.New("source unavailable")
		for range 2 {
			if _, err := GetOrSetContext(t.Context(), c, "failed", func(context.Context) (int, error) { return 0, sourceErr }); !errors.Is(err, sourceErr) {
				t.Fatalf("source: %v", err)
			}
		}
		if _, err := GetOrSetContext(t.Context(), c, "panic", func(context.Context) (int, error) { panic("private fixture payload") }); !errors.Is(err, ErrLoadExecution) || strings.Contains(err.Error(), "private fixture payload") {
			t.Fatalf("panic: %v", err)
		}
		synctest.Wait()
		c.loadMu.Lock()
		pending := len(c.loads)
		c.loadMu.Unlock()
		if pending != 0 {
			t.Fatalf("pending loads: %d", pending)
		}
	})
}

func TestContextLoaderBackendFailureRetainsSourceFallback(t *testing.T) {
	var errorsSeen atomic.Int64
	c := New(Config{Backend: failingBackend{err: errors.New("redis unavailable")}, OnError: func(string, error) { errorsSeen.Add(1) }})
	value, err := GetOrSetContext(t.Context(), c, "public", func(context.Context) (int, error) { return 42, nil })
	if value != 42 || err != nil || errorsSeen.Load() < 3 {
		t.Fatalf("fallback=%d errors=%d error=%v", value, errorsSeen.Load(), err)
	}
}

func TestContextLoaderRejectsMixedResultTypes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := NewMemoryBackend()
		defer backend.Close()
		c := New(Config{Backend: backend})
		release, started := make(chan struct{}), make(chan struct{})
		first, second := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := GetOrSetContext(t.Context(), c, "shared", func(context.Context) (string, error) { close(started); <-release; return "text", nil })
			first <- err
		}()
		<-started
		go func() {
			_, err := GetOrSetContext(t.Context(), c, "shared", func(context.Context) (int, error) { return 7, nil })
			second <- err
		}()
		synctest.Wait()
		close(release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; !errors.Is(err, ErrLoadType) {
			t.Fatalf("type: %v", err)
		}
	})
}

func TestContextLoaderPreservesNilResults(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	c := New(Config{Backend: backend})
	calls := 0
	for range 2 {
		value, err := GetOrSetContext(t.Context(), c, "nil-interface", func(context.Context) (any, error) {
			calls++
			return nil, nil
		})
		if value != nil || err != nil {
			t.Fatalf("nil interface value=%v error=%v", value, err)
		}
		pointer, err := GetOrSetContext(t.Context(), c, "nil-pointer", func(context.Context) (*int, error) {
			calls++
			return nil, nil
		})
		if pointer != nil || err != nil {
			t.Fatalf("nil pointer value=%v error=%v", pointer, err)
		}
	}
	if calls != 2 {
		t.Fatalf("source calls=%d, want 2", calls)
	}
}

func BenchmarkContextLoaderWarm(b *testing.B) {
	backend := NewMemoryBackend()
	defer backend.Close()
	c := New(Config{Backend: backend})
	if err := c.Set(b.Context(), "warm", "value"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := GetOrSetContext(b.Context(), c, "warm", func(context.Context) (string, error) { return "source", nil }); err != nil {
			b.Fatal(err)
		}
	}
}

type loaderReadBackend struct {
	Backend
	read func(context.Context, string) ([]byte, error)
}

func (b loaderReadBackend) Get(ctx context.Context, key string) ([]byte, error) {
	return b.read(ctx, key)
}

func TestContextLoaderCancellationDuringCacheRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := New(Config{Backend: loaderReadBackend{read: func(context.Context, string) ([]byte, error) { cancel(); return nil, nil }}})
	_, err := GetOrSetContext(ctx, c, "key", func(context.Context) (int, error) { t.Error("cancelled read reached source"); return 0, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cache-read cancellation: %v", err)
	}
}

func TestContextLoaderRechecksBeforeSourceWork(t *testing.T) {
	var reads atomic.Int64
	c := New(Config{Backend: loaderReadBackend{read: func(context.Context, string) ([]byte, error) {
		if reads.Add(1) == 1 {
			return nil, nil
		}
		return []byte("7"), nil
	}}})
	value, err := GetOrSetContext(t.Context(), c, "filled-concurrently", func(context.Context) (int, error) { t.Error("source ran after concurrent fill"); return 0, nil })
	if err != nil || value != 7 {
		t.Fatalf("recheck: %d %v", value, err)
	}
}

func TestContextLoaderWarmAllocationBound(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	c := New(Config{Backend: backend})
	if err := c.Set(t.Context(), "warm", "value"); err != nil {
		t.Fatal(err)
	}
	load := func(context.Context) (string, error) { t.Error("warm cache reached source"); return "", nil }
	allocations := testing.AllocsPerRun(100, func() {
		if _, err := GetOrSetContext(t.Context(), c, "warm", load); err != nil {
			t.Error(err)
		}
	})
	if allocations > 3 {
		t.Fatalf("warm read allocated %.1f times; budget is 3", allocations)
	}
}

func BenchmarkLegacyLoaderWarm(b *testing.B) {
	backend := NewMemoryBackend()
	defer backend.Close()
	c := New(Config{Backend: backend})
	if err := c.Set(b.Context(), "warm", "value"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := GetOrSet(b.Context(), c, "warm", func() (string, error) { return "source", nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func TestContextLoaderPreservesContextValuesAndScopedKeys(t *testing.T) {
	type contextKey struct{}
	backend := NewMemoryBackend()
	defer backend.Close()
	c := New(Config{Backend: backend})
	for _, organization := range []string{"one", "two", "one"} {
		ctx := context.WithValue(t.Context(), contextKey{}, organization)
		value, err := GetOrSetContext(ctx, c, "organization:"+organization, func(loadCtx context.Context) (string, error) {
			return loadCtx.Value(contextKey{}).(string), nil
		})
		if value != organization || err != nil {
			t.Fatalf("scope=%q value=%q error=%v", organization, value, err)
		}
	}
}

// TestLoadDetachesRequestArena guards the detached load goroutine. It can
// outlive the request, so it must not see the request's pooled arena.
func TestLoadDetachesRequestArena(t *testing.T) {
	c := New(Config{Backend: NewMemoryBackend()})
	ctx := scratch.WithArena(context.Background(), scratch.New(1024))
	var sawArena atomic.Bool
	_, err := GetOrSetContext(ctx, c, "detached", func(loadCtx context.Context) (string, error) {
		sawArena.Store(scratch.FromContext(loadCtx) != nil)
		return "v", nil
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sawArena.Load() {
		t.Fatal("detached load must not inherit the request arena")
	}
}
