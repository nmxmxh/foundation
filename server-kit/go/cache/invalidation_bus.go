package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// DefaultInvalidationChannel is the bus channel for tag invalidation wakeups.
// The rediskit prefix qualifies it per deployment.
const DefaultInvalidationChannel = "cache:invalidate"

// maxBroadcastTags bounds one notice payload.
const maxBroadcastTags = 128

// InvalidationNotice is the wire format for tag invalidation wakeups.
type InvalidationNotice struct {
	Tags []string `json:"tags"`
}

// InvalidationBus pairs durable marker-based invalidation (shared backend)
// with instant pub/sub wakeups over the same Redis deployment projects use as
// their transport bus. Shared InvalidateTag calls remain authoritative: the
// broadcast only accelerates process-local caches. Listeners reconcile from
// markers after downtime, so missed wakeups degrade to slower convergence,
// never staleness beyond the freshness contract.
type InvalidationBus struct {
	client  rediskit.Client
	channel string
	local   *Cache
	onError func(tag string, err error)

	mu     sync.Mutex
	stop   func()
	closed bool
}

// NewInvalidationBus wires invalidation wakeups for localCache through the
// given rediskit transport client. An empty channel selects
// DefaultInvalidationChannel.
func NewInvalidationBus(client rediskit.Client, channel string, local *Cache) *InvalidationBus {
	if strings.TrimSpace(channel) == "" {
		channel = DefaultInvalidationChannel
	}
	return &InvalidationBus{
		client:  client,
		channel: channel,
		local:   local,
		onError: func(_ string, _ error) {},
	}
}

// SetErrorHandler installs a callback for best-effort local apply failures.
func (b *InvalidationBus) SetErrorHandler(onError func(tag string, err error)) {
	if onError != nil {
		b.onError = onError
	}
}

// BroadcastTag publishes invalidation wakeups for the listed tags. Call it
// after the shared Invalidator.InvalidateTag succeeds; subscribers treat the
// notice as a hint, not truth.
func (b *InvalidationBus) BroadcastTag(ctx context.Context, tags ...string) error {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if len(names) > maxBroadcastTags {
		return fmt.Errorf("cache: broadcast accepts at most %d tags, got %d", maxBroadcastTags, len(names))
	}
	payload, err := json.Marshal(InvalidationNotice{Tags: names})
	if err != nil {
		return fmt.Errorf("cache: encode invalidation notice: %w", err)
	}
	if err := b.client.Publish(ctx, b.channel, payload); err != nil {
		return fmt.Errorf("cache: publish invalidation notice: %w", err)
	}
	return nil
}

// Listen applies incoming notices to the local cache until ctx ends or Close
// is called. Undecodable payloads are skipped: other lanes share the bus.
func (b *InvalidationBus) Listen(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return fmt.Errorf("cache: invalidation bus is closed")
	}
	if b.stop != nil {
		b.mu.Unlock()
		return fmt.Errorf("cache: invalidation bus is already listening")
	}
	messages, stop, err := b.client.Subscribe(ctx, b.channel)
	if err != nil {
		b.mu.Unlock()
		return fmt.Errorf("cache: subscribe invalidation channel: %w", err)
	}
	b.stop = stop
	b.mu.Unlock()

	inv := NewInvalidator(b.local)
	go func() {
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messages:
				if !ok {
					return
				}
				var notice InvalidationNotice
				if json.Unmarshal(message, &notice) != nil {
					continue
				}
				for _, tag := range notice.Tags {
					if applyErr := inv.InvalidateTag(ctx, tag); applyErr != nil {
						b.onError(tag, applyErr)
					}
				}
			}
		}
	}()
	return nil
}

// Close stops the listener. It is idempotent.
func (b *InvalidationBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.stop != nil {
		b.stop()
		b.stop = nil
	}
}
