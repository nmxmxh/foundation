package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func waitFor(t *testing.T, timeout time.Duration, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before deadline")
}

func newBusPair(t *testing.T) (transport rediskit.Client, busA, busB *InvalidationBus, localCache *Cache) {
	t.Helper()
	transport = rediskit.NewMemoryClient("bustest")
	config := Config{Backend: NewMemoryBackend(), DefaultTTL: time.Minute}
	localCache = New(config)
	busA = NewInvalidationBus(transport, "", localCache)
	busB = NewInvalidationBus(transport, "", New(config))
	return
}

func TestInvalidationBus_BroadcastDropsProcessLocalEntries(t *testing.T) {
	_, busA, busB, localCache := newBusPair(t)
	ctx := t.Context()

	if err := busA.Listen(ctx); err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Process A holds a hot local copy registered under the tag.
	if err := localCache.Set(ctx, "summary:org-1", "hot"); err != nil {
		t.Fatalf("local Set failed: %v", err)
	}
	inv := NewInvalidator(localCache)
	if err := inv.Tag(ctx, "summary:org-1", "summaries"); err != nil {
		t.Fatalf("Tag failed: %v", err)
	}

	// Process B mutates elsewhere and wakes every subscriber.
	if err := busB.BroadcastTag(ctx, "summaries"); err != nil {
		t.Fatalf("BroadcastTag failed: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		var val string
		return errors.Is(localCache.Get(ctx, "summary:org-1", &val), ErrNotFound)
	})
}

func TestInvalidationBus_SkipsForeignPayloads(t *testing.T) {
	transport, busA, _, _ := newBusPair(t)
	ctx := t.Context()

	if err := busA.Listen(ctx); err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	for _, payload := range [][]byte{
		[]byte("not json"),
		[]byte(`{"tags": "wrong-shape"}`),
		nil,
	} {
		if err := transport.Publish(ctx, DefaultInvalidationChannel, payload); err != nil {
			t.Fatalf("Publish failed: %v", err)
		}
	}

	// The listener must still apply well-formed notices afterwards.
	var localCache *Cache = busA.local
	if err := localCache.Set(ctx, "k", "v"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if err := NewInvalidator(localCache).Tag(ctx, "k", "t"); err != nil {
		t.Fatalf("Tag failed: %v", err)
	}

	other := NewInvalidationBus(transport, "", New(Config{Backend: NewMemoryBackend()}))
	if err := other.BroadcastTag(ctx, "t"); err != nil {
		t.Fatalf("BroadcastTag failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		var val string
		return errors.Is(localCache.Get(ctx, "k", &val), ErrNotFound)
	})
}

func TestInvalidationBus_ListenGuards(t *testing.T) {
	_, busA, _, _ := newBusPair(t)

	ctx := t.Context()
	if err := busA.Listen(ctx); err != nil {
		t.Fatalf("first Listen failed: %v", err)
	}
	if err := busA.Listen(ctx); err == nil {
		t.Fatal("second Listen must fail while active")
	}

	busA.Close()
	busA.Close()
	if err := busA.Listen(ctx); err == nil {
		t.Fatal("Listen after Close must fail")
	}
}

func TestInvalidationBus_BroadcastBounds(t *testing.T) {
	_, _, busB, _ := newBusPair(t)
	ctx := context.Background()

	if err := busB.BroadcastTag(ctx); err != nil {
		t.Fatalf("empty broadcast should no-op: %v", err)
	}
	if err := busB.BroadcastTag(ctx, " ", ""); err != nil {
		t.Fatalf("blank-only broadcast should no-op: %v", err)
	}
	tooMany := make([]string, maxBroadcastTags+1)
	for i := range tooMany {
		tooMany[i] = "tag"
	}
	if err := busB.BroadcastTag(ctx, tooMany...); err == nil {
		t.Fatal("oversized broadcast must fail")
	}
}

func TestInvalidationBus_ReportsApplyErrors(t *testing.T) {
	transport := rediskit.NewMemoryClient("bustest")
	failing := &patternFailingBackend{err: errStub}
	localCache := New(Config{Backend: failing, DefaultTTL: time.Minute})

	bus := NewInvalidationBus(transport, "", localCache)
	reported := make(chan string, 4)
	bus.SetErrorHandler(func(tag string, err error) {
		reported <- tag
	})

	ctx := t.Context()
	if err := bus.Listen(ctx); err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	if err := bus.BroadcastTag(ctx, "broken"); err != nil {
		t.Fatalf("BroadcastTag failed: %v", err)
	}
	select {
	case tag := <-reported:
		if tag != "broken" {
			t.Fatalf("reported tag = %q", tag)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("apply error was never reported")
	}
}

type patternFailingBackend struct {
	Backend
	err error
}

func (p *patternFailingBackend) DeletePattern(context.Context, string) ([]string, error) {
	return nil, errStub
}
