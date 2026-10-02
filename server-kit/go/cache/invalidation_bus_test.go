package cache

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
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

func TestInvalidationBus_BroadcastRejectsOversizedTagList(t *testing.T) {
	client := rediskit.NewMemoryClient("bustest")
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	bus := NewInvalidationBus(client, "", New(Config{Backend: backend}))

	tags := make([]string, 0, maxBroadcastTags+1)
	for index := range maxBroadcastTags + 1 {
		tags = append(tags, "tag-"+strconv.Itoa(index))
	}
	if err := bus.BroadcastTag(context.Background(), tags...); err == nil {
		t.Fatalf("BroadcastTag(%d tags) err=nil, want a bound error", len(tags))
	}
	// The bound is on the trimmed list, so exactly the maximum is accepted.
	if err := bus.BroadcastTag(context.Background(), tags[:maxBroadcastTags]...); err != nil {
		t.Fatalf("BroadcastTag(%d tags) err=%v", maxBroadcastTags, err)
	}
}

// stopWatchingClient counts calls to a subscription's stop func. Close calls it
// once; the listener goroutine's own deferred call is the second, which makes
// "the goroutine has returned" an observable event instead of a sleep.

func TestInvalidationBus_ListenIsExclusiveAndClosable(t *testing.T) {
	client := &stopWatchingClient{Client: rediskit.NewMemoryClient("bustest"), exited: make(chan struct{})}
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	bus := NewInvalidationBus(client, "", New(Config{Backend: backend}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bus.Listen(ctx); err != nil {
		t.Fatalf("Listen() err=%v", err)
	}
	if err := bus.Listen(ctx); err == nil {
		t.Fatal("second Listen() err=nil, want an already-listening error")
	}

	bus.Close()
	bus.Close() // idempotent
	// Close shuts the subscription channel; the listener observes that and
	// returns. Waiting for its deferred stop keeps the assertion off the clock.
	select {
	case <-client.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not return after Close")
	}

	if err := bus.Listen(ctx); err == nil {
		t.Fatal("Listen() after Close err=nil, want a closed error")
	}
}

type stopWatchingClient struct {
	rediskit.Client
	calls  atomic.Int32
	exited chan struct{}
}

func (c *stopWatchingClient) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	messages, stop, err := c.Client.Subscribe(ctx, channel)
	if err != nil {
		return messages, stop, err
	}
	return messages, func() {
		stop()
		if c.calls.Add(1) == 2 {
			close(c.exited)
		}
	}, nil
}

// Listen is single-flight per bus: a second call would install a second stop
// func over the first, leaking the original subscription. Closing the bus ends
// the listener, and a closed bus refuses to listen again.
