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

// Set with an explicit TTL takes the per-call override rather than the
// configured default, which is the whole point of the variadic argument.
func TestCache_SetHonorsExplicitTTL(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	c := New(Config{Backend: backend, DefaultTTL: time.Hour})
	ctx := context.Background()

	if err := c.Set(ctx, "short", "value", 20*time.Millisecond); err != nil {
		t.Fatalf("Set() err=%v", err)
	}
	exists, err := c.Exists(ctx, "short")
	if err != nil || !exists {
		t.Fatalf("Exists() = %v, %v; want true, nil", exists, err)
	}
	time.Sleep(40 * time.Millisecond)
	// The entry is past its TTL, so Exists reports absence even though the
	// janitor has not swept the map yet.
	if exists, err = c.Exists(ctx, "short"); err != nil || exists {
		t.Fatalf("Exists() after TTL = %v, %v; want false, nil", exists, err)
	}
	// A non-positive TTL is not an override: the default still applies.
	if setErr := c.Set(ctx, "default-ttl", "value", 0); setErr != nil {
		t.Fatalf("Set(0) err=%v", setErr)
	}
	if exists, err = c.Exists(ctx, "default-ttl"); err != nil || !exists {
		t.Fatalf("Exists(default-ttl) = %v, %v; want true, nil", exists, err)
	}
}

// A nil MemoryBackend closes cleanly. Close is called from teardown paths that
// may run before construction succeeded.
func TestMemoryBackend_NilCloseIsSafe(t *testing.T) {
	var backend *MemoryBackend
	if err := backend.Close(); err != nil {
		t.Fatalf("(*MemoryBackend)(nil).Close() err=%v", err)
	}
}

// The invalidator's nil guards are the difference between a clear error and a
// panic in a caller that built one from a cache it never checked.
func TestInvalidator_NilGuards(t *testing.T) {
	ctx := context.Background()
	for name, inv := range map[string]*Invalidator{
		"nil invalidator": nil,
		"nil cache":       NewInvalidator(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if err := inv.Tag(ctx, "key", "tag"); err == nil {
				t.Fatal("Tag() err=nil, want an error")
			}
			if err := inv.InvalidateTag(ctx, "tag"); err == nil {
				t.Fatal("InvalidateTag() err=nil, want an error")
			}
		})
	}
}

// deleteFailingBackend serves markers normally but refuses to delete the keys
// they point at, which is how a tagged key survives its own invalidation.
type deleteFailingBackend struct {
	Backend
	err error
}

func (b deleteFailingBackend) Delete(ctx context.Context, key string) error {
	return b.err
}

// InvalidateTag reports the first failure to delete a tagged key rather than
// returning nil: the markers are gone, so a silent failure would leave the
// entry cached and permanently unreachable by any later invalidation.
func TestInvalidator_ReportsTaggedKeyDeleteFailure(t *testing.T) {
	deleteErr := errors.New("delete refused")
	memory := NewMemoryBackend()
	t.Cleanup(func() { _ = memory.Close() })
	c := New(Config{Backend: deleteFailingBackend{Backend: memory, err: deleteErr}, DefaultTTL: time.Minute})
	inv := NewInvalidator(c)
	ctx := context.Background()

	if err := c.Set(ctx, "user:1", "alice"); err != nil {
		t.Fatalf("Set() err=%v", err)
	}
	if err := inv.Tag(ctx, c.key("user:1"), "users"); err != nil {
		t.Fatalf("Tag() err=%v", err)
	}
	if err := inv.InvalidateTag(ctx, "users"); !errors.Is(err, deleteErr) {
		t.Fatalf("InvalidateTag() err=%v, want %v", err, deleteErr)
	}
}

// The bus bounds one notice: an unbounded tag list would let one caller decide
// the payload size every subscriber has to decode.
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
