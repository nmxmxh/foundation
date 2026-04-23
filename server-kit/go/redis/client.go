package redis

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	DriverMemory = "memory"
	DriverRedis  = "redis"
)

// Client is the pub/sub transport abstraction used by runtime components.
type Client interface {
	Publish(context.Context, string, []byte) error
	Subscribe(context.Context, string) (<-chan []byte, func(), error)
	PSubscribe(context.Context, ...string) ([]<-chan []byte, func(), error)
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Close() error
}

// Connect creates a redis pub/sub client using the selected driver.
func Connect(url, prefix, driver string) (Client, error) {
	switch normalizeDriver(driver) {
	case DriverRedis:
		return newRedisClient(url, prefix)
	default:
		return NewMemoryClient(prefix), nil
	}
}

func normalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case DriverRedis:
		return DriverRedis
	default:
		return DriverMemory
	}
}

type memoryClient struct {
	prefix string

	mu          sync.RWMutex
	closed      bool
	subscribers map[string][]chan []byte
	counters    map[string]int64
	expiries    map[string]time.Time
}

func NewMemoryClient(prefix string) Client {
	if prefix == "" {
		prefix = "ovasabi"
	}
	return &memoryClient{
		prefix:      prefix,
		subscribers: map[string][]chan []byte{},
		counters:    map[string]int64{},
		expiries:    map[string]time.Time{},
	}
}

func (c *memoryClient) Publish(_ context.Context, channel string, payload []byte) error {
	qualified := c.qualify(channel)
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("memory redis client is closed")
	}
	subs := c.subscribers[qualified]
	c.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub <- payload:
		default:
		}
	}
	return nil
}

func (c *memoryClient) Subscribe(_ context.Context, channel string) (<-chan []byte, func(), error) {
	qualified := c.qualify(channel)
	ch := make(chan []byte, 256)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, fmt.Errorf("memory redis client is closed")
	}
	c.subscribers[qualified] = append(c.subscribers[qualified], ch)

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			subs := c.subscribers[qualified]
			filtered := make([]chan []byte, 0, len(subs))
			wasRegistered := false
			for _, sub := range subs {
				if sub == ch {
					wasRegistered = true
					continue
				}
				if sub != ch {
					filtered = append(filtered, sub)
				}
			}
			c.subscribers[qualified] = filtered
			if wasRegistered {
				close(ch)
			}
		})
	}
	return ch, cancel, nil
}

func (c *memoryClient) PSubscribe(ctx context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	if len(patterns) == 0 {
		return nil, nil, fmt.Errorf("at least one pattern is required")
	}
	channels := make([]<-chan []byte, 0, len(patterns))
	cancelFns := make([]func(), 0, len(patterns))
	for _, pattern := range patterns {
		ch, cancel, err := c.Subscribe(ctx, pattern)
		if err != nil {
			for _, fn := range cancelFns {
				fn()
			}
			return nil, nil, err
		}
		channels = append(channels, ch)
		cancelFns = append(cancelFns, cancel)
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			for _, fn := range cancelFns {
				fn()
			}
		})
	}
	return channels, cancel, nil
}

func (c *memoryClient) Incr(_ context.Context, key string) (int64, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()

	if expiry, ok := c.expiries[qualified]; ok && time.Now().After(expiry) {
		delete(c.counters, qualified)
		delete(c.expiries, qualified)
	}

	c.counters[qualified]++
	return c.counters[qualified], nil
}

func (c *memoryClient) Expire(_ context.Context, key string, ttl time.Duration) (bool, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expiries[qualified] = time.Now().Add(ttl)
	return true, nil
}

func (c *memoryClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for channel, subs := range c.subscribers {
		for _, sub := range subs {
			close(sub)
		}
		c.subscribers[channel] = nil
	}
	return nil
}

func (c *memoryClient) qualify(channel string) string {
	trimmed := strings.TrimSpace(channel)
	if strings.HasPrefix(trimmed, c.prefix+":") {
		return trimmed
	}
	return fmt.Sprintf("%s:%s", c.prefix, trimmed)
}

type redisClient struct {
	client *goredis.Client
	prefix string
}

func newRedisClient(url, prefix string) (*redisClient, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("redis url is required when redis driver is enabled")
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "ovasabi"
	}

	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := goredis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &redisClient{
		client: client,
		prefix: prefix,
	}, nil
}

func (c *redisClient) Publish(ctx context.Context, channel string, payload []byte) error {
	return c.client.Publish(ctx, c.qualify(channel), payload).Err()
}

func (c *redisClient) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	pubsub := c.client.Subscribe(ctx, c.qualify(channel))
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, err
	}
	src := pubsub.Channel(goredis.WithChannelSize(256))
	out := make(chan []byte, 256)

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			_ = pubsub.Close()
			close(out)
		})
	}

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-src:
				if !ok {
					return
				}
				payload := []byte(msg.Payload)
				select {
				case out <- payload:
				default:
				}
			}
		}
	}()
	return out, cancel, nil
}

func (c *redisClient) PSubscribe(ctx context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	if len(patterns) == 0 {
		return nil, nil, fmt.Errorf("at least one pattern is required")
	}
	qualified := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		qualified = append(qualified, c.qualify(pattern))
	}
	pubsub := c.client.PSubscribe(ctx, qualified...)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, err
	}
	src := pubsub.Channel(goredis.WithChannelSize(256))

	out := make(chan []byte, 256)
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			_ = pubsub.Close()
			close(out)
		})
	}

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-src:
				if !ok {
					return
				}
				select {
				case out <- []byte(msg.Payload):
				default:
				}
			}
		}
	}()

	channels := make([]<-chan []byte, 0, len(patterns))
	for range patterns {
		channels = append(channels, out)
	}
	return channels, cancel, nil
}

func (c *redisClient) Incr(ctx context.Context, key string) (int64, error) {
	return c.client.Incr(ctx, c.qualify(key)).Result()
}

func (c *redisClient) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return c.client.Expire(ctx, c.qualify(key), ttl).Result()
}

func (c *redisClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *redisClient) qualify(channel string) string {
	trimmed := strings.TrimSpace(channel)
	if strings.HasPrefix(trimmed, c.prefix+":") {
		return trimmed
	}
	return fmt.Sprintf("%s:%s", c.prefix, trimmed)
}
