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

// Client is the pub/sub and stream transport abstraction used by runtime components.
type Client interface {
	// Pub/Sub
	Publish(context.Context, string, []byte) error
	Subscribe(context.Context, string) (<-chan []byte, func(), error)
	PSubscribe(context.Context, ...string) ([]<-chan []byte, func(), error)

	// Streams (Reliable event delivery)
	XAdd(ctx context.Context, stream string, values map[string]interface{}) (string, error)
	XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error)
	XAck(ctx context.Context, stream, group string, ids ...string) error

	// Coordination & Locks
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Lock(ctx context.Context, key string, ttl time.Duration) (string, error)
	Unlock(ctx context.Context, key, token string) (bool, error)

	// Analytics & Cardinality (HyperLogLog)
	PFAdd(ctx context.Context, key string, els ...interface{}) (int64, error)
	PFCount(ctx context.Context, keys ...string) (int64, error)

	// Primitives
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	Close() error
}

// StreamMessage represents a message read from a Redis stream.
type StreamMessage struct {
	ID     string
	Values map[string]interface{}
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

func (c *memoryClient) XAdd(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return "msg-123", nil
}

func (c *memoryClient) XReadGroup(_ context.Context, _, _, _ string, _ int64) ([]StreamMessage, error) {
	return nil, nil
}

func (c *memoryClient) XAck(_ context.Context, _, _ string, _ ...string) error {
	return nil
}

func (c *memoryClient) Lock(_ context.Context, key string, _ time.Duration) (string, error) {
	return "token-" + key, nil
}

func (c *memoryClient) Unlock(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

func (c *memoryClient) Set(_ context.Context, _ string, _ interface{}, _ time.Duration) error {
	return nil
}

func (c *memoryClient) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}

func (c *memoryClient) Del(_ context.Context, _ ...string) error {
	return nil
}

func (c *memoryClient) PFAdd(_ context.Context, _ string, _ ...interface{}) (int64, error) {
	return 1, nil
}

func (c *memoryClient) PFCount(_ context.Context, _ ...string) (int64, error) {
	return 0, nil
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

func (c *redisClient) XAdd(ctx context.Context, stream string, values map[string]interface{}) (string, error) {
	return c.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: c.qualify(stream),
		Values: values,
	}).Result()
}

func (c *redisClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	res, err := c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{c.qualify(stream), ">"},
		Count:    count,
		Block:    0,
	}).Result()
	if err != nil {
		return nil, err
	}

	messages := make([]StreamMessage, 0)
	for _, xstream := range res {
		for _, xmsg := range xstream.Messages {
			messages = append(messages, StreamMessage{
				ID:     xmsg.ID,
				Values: xmsg.Values,
			})
		}
	}
	return messages, nil
}

func (c *redisClient) XAck(ctx context.Context, stream, group string, ids ...string) error {
	return c.client.XAck(ctx, c.qualify(stream), group, ids...).Err()
}

func (c *redisClient) Lock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	qualified := c.qualify(key)
	ok, err := c.client.SetNX(ctx, qualified, token, ttl).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("lock already held for key: %s", key)
	}
	return token, nil
}

func (c *redisClient) Unlock(ctx context.Context, key, token string) (bool, error) {
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`
	res, err := c.client.Eval(ctx, script, []string{c.qualify(key)}, token).Int64()
	return res == 1, err
}

func (c *redisClient) PFAdd(ctx context.Context, key string, els ...interface{}) (int64, error) {
	return c.client.PFAdd(ctx, c.qualify(key), els...).Result()
}

func (c *redisClient) PFCount(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	qualified := make([]string, len(keys))
	for i, k := range keys {
		qualified[i] = c.qualify(k)
	}
	return c.client.PFCount(ctx, qualified...).Result()
}

func (c *redisClient) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.client.Set(ctx, c.qualify(key), value, ttl).Err()
}

func (c *redisClient) Get(ctx context.Context, key string) ([]byte, error) {
	return c.client.Get(ctx, c.qualify(key)).Bytes()
}

func (c *redisClient) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	qualified := make([]string, len(keys))
	for i, k := range keys {
		qualified[i] = c.qualify(k)
	}
	return c.client.Del(ctx, qualified...).Err()
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
