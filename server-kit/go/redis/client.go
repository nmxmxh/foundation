package redis

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	goredis "github.com/redis/go-redis/v9"
)

const (
	DriverMemory = "memory"
	DriverRedis  = "redis"
)

type Options struct {
	URL          string
	URLs         []string
	Prefix       string
	Driver       string
	PoolSize     int
	MinIdle      int
	MaxRetries   int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

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

// BatchClient is the optional round-trip amortization surface for cache
// hydration and write-through paths. Callers should use it when several
// independent Redis keys cross the network boundary together.
type BatchClient interface {
	SetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) error
	GetMany(ctx context.Context, keys ...string) (map[string][]byte, error)
	SetGetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) (map[string][]byte, error)
}

// StreamMessage represents a message read from a Redis stream.
type StreamMessage struct {
	ID     string
	Values map[string]interface{}
}

// Connect creates a redis pub/sub client using the selected driver.
func Connect(url, prefix, driver string) (Client, error) {
	return ConnectWithOptions(Options{URL: url, Prefix: prefix, Driver: driver})
}

func ConnectWithOptions(opts Options) (Client, error) {
	opts = normalizeOptions(opts)
	switch normalizeDriver(opts.Driver) {
	case DriverRedis:
		if len(opts.URLs) > 1 {
			return newShardedClient(opts)
		}
		return newRedisClient(opts)
	default:
		return NewMemoryClient(opts.Prefix), nil
	}
}

func normalizeOptions(opts Options) Options {
	if strings.TrimSpace(opts.URL) != "" && len(opts.URLs) == 0 {
		opts.URLs = []string{opts.URL}
	}
	if strings.TrimSpace(opts.Prefix) == "" {
		opts.Prefix = "ovasabi"
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = 32
	}
	if opts.MinIdle < 0 {
		opts.MinIdle = 0
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 2 * time.Second
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = 500 * time.Millisecond
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 500 * time.Millisecond
	}
	return opts
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

	mu                 sync.RWMutex
	closed             bool
	subscribers        map[string][]chan []byte
	patternSubscribers map[string][]chan []byte
	values             map[string]memoryValue
	counters           map[string]int64
	expiries           map[string]time.Time
	locks              map[string]memoryLock
	streams            map[string][]StreamMessage
	streamSequences    map[string]int64
	streamGroups       map[string]map[string]*memoryStreamGroup
	hyperLogLogs       map[string]map[string]struct{}
	lockSequence       int64
}

type memoryValue struct {
	data []byte
}

type memoryLock struct {
	token     string
	expiresAt time.Time
}

type memoryStreamGroup struct {
	next    int
	pending map[string]StreamMessage
}

func NewMemoryClient(prefix string) Client {
	if prefix == "" {
		prefix = "ovasabi"
	}
	return &memoryClient{
		prefix:             prefix,
		subscribers:        map[string][]chan []byte{},
		patternSubscribers: map[string][]chan []byte{},
		values:             map[string]memoryValue{},
		counters:           map[string]int64{},
		expiries:           map[string]time.Time{},
		locks:              map[string]memoryLock{},
		streams:            map[string][]StreamMessage{},
		streamSequences:    map[string]int64{},
		streamGroups:       map[string]map[string]*memoryStreamGroup{},
		hyperLogLogs:       map[string]map[string]struct{}{},
	}
}

func (c *memoryClient) Publish(_ context.Context, channel string, payload []byte) error {
	qualified := c.qualify(channel)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	for _, sub := range c.subscribers[qualified] {
		publishMemoryPayload(sub, payload)
	}
	for pattern, patternSubs := range c.patternSubscribers {
		if redisPatternMatches(pattern, qualified) {
			for _, sub := range patternSubs {
				publishMemoryPayload(sub, payload)
			}
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
			if removeMemorySubscriber(c.subscribers, qualified, ch) {
				close(ch)
			}
		})
	}
	return ch, cancel, nil
}

func (c *memoryClient) PSubscribe(_ context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	if len(patterns) == 0 {
		return nil, nil, fmt.Errorf("at least one pattern is required")
	}
	channels := make([]<-chan []byte, 0, len(patterns))
	registered := make([]struct {
		pattern string
		ch      chan []byte
	}, 0, len(patterns))

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("memory redis client is closed")
	}
	for _, pattern := range patterns {
		qualified := c.qualify(pattern)
		ch := make(chan []byte, 256)
		c.patternSubscribers[qualified] = append(c.patternSubscribers[qualified], ch)
		channels = append(channels, ch)
		registered = append(registered, struct {
			pattern string
			ch      chan []byte
		}{pattern: qualified, ch: ch})
	}
	c.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			for _, sub := range registered {
				if removeMemorySubscriber(c.patternSubscribers, sub.pattern, sub.ch) {
					close(sub.ch)
				}
			}
		})
	}
	return channels, cancel, nil
}

func (c *memoryClient) Incr(_ context.Context, key string) (int64, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expireIfNeededLocked(qualified, time.Now())
	if value, ok := c.values[qualified]; ok {
		current, err := strconv.ParseInt(string(value.data), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("value is not an integer or out of range")
		}
		current++
		c.values[qualified] = memoryValue{data: []byte(strconv.FormatInt(current, 10))}
		c.counters[qualified] = current
		return current, nil
	}

	c.counters[qualified]++
	c.values[qualified] = memoryValue{data: []byte(strconv.FormatInt(c.counters[qualified], 10))}
	return c.counters[qualified], nil
}

func (c *memoryClient) Expire(_ context.Context, key string, ttl time.Duration) (bool, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.memoryKeyExistsLocked(qualified, time.Now()) {
		return false, nil
	}
	c.expiries[qualified] = time.Now().Add(ttl)
	return true, nil
}

func (c *memoryClient) XAdd(_ context.Context, stream string, values map[string]interface{}) (string, error) {
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", fmt.Errorf("memory redis client is closed")
	}
	c.streamSequences[qualified]++
	id := fmt.Sprintf("%d-%d", time.Now().UnixMilli(), c.streamSequences[qualified])
	c.streams[qualified] = append(c.streams[qualified], StreamMessage{
		ID:     id,
		Values: copyInterfaceMap(values),
	})
	return id, nil
}

func (c *memoryClient) XReadGroup(_ context.Context, stream, group, _ string, count int64) ([]StreamMessage, error) {
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	g := c.memoryStreamGroupLocked(qualified, group)
	messages := c.streams[qualified]
	if g.next >= len(messages) {
		return nil, nil
	}
	limit := len(messages) - g.next
	if count > 0 && int(count) < limit {
		limit = int(count)
	}
	out := make([]StreamMessage, 0, limit)
	for i := 0; i < limit; i++ {
		msg := cloneStreamMessage(messages[g.next+i])
		g.pending[msg.ID] = msg
		out = append(out, msg)
	}
	g.next += limit
	return out, nil
}

func (c *memoryClient) XAck(_ context.Context, stream, group string, ids ...string) error {
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	g := c.memoryStreamGroupLocked(qualified, group)
	for _, id := range ids {
		delete(g.pending, id)
	}
	return nil
}

func (c *memoryClient) Lock(_ context.Context, key string, ttl time.Duration) (string, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expireIfNeededLocked(qualified, now)
	if lock, ok := c.locks[qualified]; ok && lock.expiresAt.After(now) {
		return "", fmt.Errorf("lock already held for key: %s", key)
	}
	c.lockSequence++
	token := fmt.Sprintf("token-%d", c.lockSequence)
	expiresAt := now.Add(ttl)
	if ttl <= 0 {
		expiresAt = now.Add(time.Second)
	}
	c.locks[qualified] = memoryLock{token: token, expiresAt: expiresAt}
	c.expiries[qualified] = expiresAt
	return token, nil
}

func (c *memoryClient) Unlock(_ context.Context, key, token string) (bool, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireIfNeededLocked(qualified, time.Now())
	lock, ok := c.locks[qualified]
	if !ok || lock.token != token {
		return false, nil
	}
	delete(c.locks, qualified)
	delete(c.expiries, qualified)
	return true, nil
}

func (c *memoryClient) Set(_ context.Context, key string, value interface{}, ttl time.Duration) error {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	c.values[qualified] = memoryValue{data: bytesFromValue(value)}
	if ttl > 0 {
		c.expiries[qualified] = time.Now().Add(ttl)
	} else {
		delete(c.expiries, qualified)
	}
	return nil
}

func (c *memoryClient) SetMany(_ context.Context, values map[string]interface{}, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	now := time.Now()
	for key, value := range values {
		qualified := c.qualify(key)
		c.values[qualified] = memoryValue{data: bytesFromValue(value)}
		if ttl > 0 {
			c.expiries[qualified] = now.Add(ttl)
		} else {
			delete(c.expiries, qualified)
		}
	}
	return nil
}

func (c *memoryClient) Get(_ context.Context, key string) ([]byte, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	c.expireIfNeededLocked(qualified, time.Now())
	value, ok := c.values[qualified]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value.data...), nil
}

func (c *memoryClient) GetMany(_ context.Context, keys ...string) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	now := time.Now()
	out := make(map[string][]byte, len(keys))
	for _, key := range keys {
		qualified := c.qualify(key)
		c.expireIfNeededLocked(qualified, now)
		value, ok := c.values[qualified]
		if !ok {
			continue
		}
		out[key] = append([]byte(nil), value.data...)
	}
	return out, nil
}

func (c *memoryClient) SetGetMany(_ context.Context, values map[string]interface{}, ttl time.Duration) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	now := time.Now()
	out := make(map[string][]byte, len(values))
	for key, value := range values {
		qualified := c.qualify(key)
		data := bytesFromValue(value)
		c.values[qualified] = memoryValue{data: data}
		if ttl > 0 {
			c.expiries[qualified] = now.Add(ttl)
		} else {
			delete(c.expiries, qualified)
		}
		out[key] = append([]byte(nil), data...)
	}
	return out, nil
}

func (c *memoryClient) Del(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	for _, key := range keys {
		qualified := c.qualify(key)
		c.deleteKeyLocked(qualified)
	}
	return nil
}

func (c *memoryClient) PFAdd(_ context.Context, key string, els ...interface{}) (int64, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, fmt.Errorf("memory redis client is closed")
	}
	if c.hyperLogLogs[qualified] == nil {
		c.hyperLogLogs[qualified] = map[string]struct{}{}
	}
	before := len(c.hyperLogLogs[qualified])
	for _, el := range els {
		c.hyperLogLogs[qualified][fmt.Sprint(el)] = struct{}{}
	}
	if len(c.hyperLogLogs[qualified]) == before {
		return 0, nil
	}
	return 1, nil
}

func (c *memoryClient) PFCount(_ context.Context, keys ...string) (int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return 0, fmt.Errorf("memory redis client is closed")
	}
	if len(keys) == 0 {
		return 0, nil
	}
	seen := map[string]struct{}{}
	for _, key := range keys {
		for value := range c.hyperLogLogs[c.qualify(key)] {
			seen[value] = struct{}{}
		}
	}
	return int64(len(seen)), nil
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
	for pattern, subs := range c.patternSubscribers {
		for _, sub := range subs {
			close(sub)
		}
		c.patternSubscribers[pattern] = nil
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

func (c *memoryClient) memoryKeyExistsLocked(qualified string, now time.Time) bool {
	c.expireIfNeededLocked(qualified, now)
	if _, ok := c.values[qualified]; ok {
		return true
	}
	if _, ok := c.counters[qualified]; ok {
		return true
	}
	if _, ok := c.locks[qualified]; ok {
		return true
	}
	if _, ok := c.streams[qualified]; ok {
		return true
	}
	if _, ok := c.hyperLogLogs[qualified]; ok {
		return true
	}
	return false
}

func (c *memoryClient) expireIfNeededLocked(qualified string, now time.Time) {
	expiry, ok := c.expiries[qualified]
	if !ok || now.Before(expiry) {
		return
	}
	c.deleteKeyLocked(qualified)
}

func (c *memoryClient) deleteKeyLocked(qualified string) {
	delete(c.values, qualified)
	delete(c.counters, qualified)
	delete(c.expiries, qualified)
	delete(c.locks, qualified)
	delete(c.streams, qualified)
	delete(c.streamSequences, qualified)
	delete(c.streamGroups, qualified)
	delete(c.hyperLogLogs, qualified)
}

func (c *memoryClient) memoryStreamGroupLocked(stream, group string) *memoryStreamGroup {
	if strings.TrimSpace(group) == "" {
		group = "default"
	}
	groups := c.streamGroups[stream]
	if groups == nil {
		groups = map[string]*memoryStreamGroup{}
		c.streamGroups[stream] = groups
	}
	g := groups[group]
	if g == nil {
		g = &memoryStreamGroup{pending: map[string]StreamMessage{}}
		groups[group] = g
	}
	return g
}

func removeMemorySubscriber(subscribers map[string][]chan []byte, key string, ch chan []byte) bool {
	subs := subscribers[key]
	filtered := make([]chan []byte, 0, len(subs))
	wasRegistered := false
	for _, sub := range subs {
		if sub == ch {
			wasRegistered = true
			continue
		}
		filtered = append(filtered, sub)
	}
	if len(filtered) == 0 {
		delete(subscribers, key)
	} else {
		subscribers[key] = filtered
	}
	return wasRegistered
}

func publishMemoryPayload(sub chan []byte, payload []byte) {
	select {
	case sub <- payload:
	default:
		observability.Default().RecordConcurrency("redis_memory", "channel", "send_rejected_full")
	}
}

func bytesFromValue(value interface{}) []byte {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), v...)
	case string:
		return []byte(v)
	default:
		return []byte(fmt.Sprint(v))
	}
}

func cloneStreamMessage(message StreamMessage) StreamMessage {
	return StreamMessage{
		ID:     message.ID,
		Values: copyInterfaceMap(message.Values),
	}
}

func copyInterfaceMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func redisPatternMatches(pattern, channel string) bool {
	if pattern == "*" || pattern == channel {
		return true
	}
	pIndex := 0
	cIndex := 0
	for pIndex < len(pattern) && cIndex < len(channel) {
		if pattern[pIndex] == '*' {
			if pIndex == len(pattern)-1 {
				return true
			}
			next := pattern[pIndex+1]
			for cIndex < len(channel) && channel[cIndex] != next {
				cIndex++
			}
			pIndex++
			continue
		}
		if pattern[pIndex] != channel[cIndex] {
			return false
		}
		pIndex++
		cIndex++
	}
	return pIndex == len(pattern) && cIndex == len(channel)
}

type redisClient struct {
	client *goredis.Client
	prefix string
}

func newRedisClient(opts Options) (*redisClient, error) {
	if len(opts.URLs) == 0 || strings.TrimSpace(opts.URLs[0]) == "" {
		return nil, fmt.Errorf("redis url is required when redis driver is enabled")
	}

	redisOpts, err := goredis.ParseURL(opts.URLs[0])
	if err != nil {
		return nil, err
	}
	redisOpts.PoolSize = opts.PoolSize
	redisOpts.MinIdleConns = opts.MinIdle
	redisOpts.MaxRetries = opts.MaxRetries
	redisOpts.DialTimeout = opts.DialTimeout
	redisOpts.ReadTimeout = opts.ReadTimeout
	redisOpts.WriteTimeout = opts.WriteTimeout
	client := goredis.NewClient(redisOpts)
	start := time.Now()
	if err := client.Ping(context.Background()).Err(); err != nil {
		recordRedisOperation("ping", start, err)
		_ = client.Close()
		return nil, err
	}
	recordRedisOperation("ping", start, nil)
	return &redisClient{
		client: client,
		prefix: opts.Prefix,
	}, nil
}

type shardedClient struct {
	shards []*redisClient
}

func newShardedClient(opts Options) (*shardedClient, error) {
	shards := make([]*redisClient, 0, len(opts.URLs))
	for _, url := range opts.URLs {
		if strings.TrimSpace(url) == "" {
			continue
		}
		shardOpts := opts
		shardOpts.URLs = []string{url}
		shard, err := newRedisClient(shardOpts)
		if err != nil {
			for _, existing := range shards {
				_ = existing.Close()
			}
			return nil, err
		}
		shards = append(shards, shard)
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("at least one redis shard url is required")
	}
	return &shardedClient{shards: shards}, nil
}

func (c *shardedClient) shard(key string) *redisClient {
	if len(c.shards) == 1 {
		return c.shards[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return c.shards[int(h.Sum32())%len(c.shards)]
}

func (c *redisClient) Publish(ctx context.Context, channel string, payload []byte) error {
	start := time.Now()
	err := c.client.Publish(ctx, c.qualify(channel), payload).Err()
	recordRedisOperation("publish", start, err)
	return err
}

func relayRedisMessages(ctx context.Context, src <-chan *goredis.Message, stopPubSub func()) (<-chan []byte, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make(chan []byte, 256)
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	cancel := func() {
		stopOnce.Do(func() {
			close(stopCh)
			if stopPubSub != nil {
				stopPubSub()
			}
		})
	}

	go func() {
		observability.Default().RecordConcurrency("redis_pubsub", "goroutine", "started")
		defer observability.Default().RecordConcurrency("redis_pubsub", "goroutine", "stopped")
		defer close(out)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				observability.Default().RecordConcurrency("redis_pubsub", "select", "cancel_won")
				return
			case <-stopCh:
				return
			case msg, ok := <-src:
				if !ok {
					return
				}
				payload := []byte(msg.Payload)
				select {
				case out <- payload:
				case <-ctx.Done():
					observability.Default().RecordConcurrency("redis_pubsub", "channel", "send_canceled")
					return
				case <-stopCh:
					return
				default:
					observability.Default().RecordConcurrency("redis_pubsub", "channel", "send_rejected_full")
				}
			}
		}
	}()

	return out, cancel
}

func (c *redisClient) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	start := time.Now()
	pubsub := c.client.Subscribe(ctx, c.qualify(channel))
	if _, err := pubsub.Receive(ctx); err != nil {
		recordRedisOperation("subscribe", start, err)
		_ = pubsub.Close()
		return nil, nil, err
	}
	recordRedisOperation("subscribe", start, nil)
	src := pubsub.Channel(goredis.WithChannelSize(256))
	out, cancel := relayRedisMessages(ctx, src, func() { _ = pubsub.Close() })
	return out, cancel, nil
}

func (c *redisClient) PSubscribe(ctx context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	if len(patterns) == 0 {
		return nil, nil, fmt.Errorf("at least one pattern is required")
	}
	start := time.Now()
	qualified := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		qualified = append(qualified, c.qualify(pattern))
	}
	pubsub := c.client.PSubscribe(ctx, qualified...)
	if _, err := pubsub.Receive(ctx); err != nil {
		recordRedisOperation("psubscribe", start, err)
		_ = pubsub.Close()
		return nil, nil, err
	}
	recordRedisOperation("psubscribe", start, nil)
	src := pubsub.Channel(goredis.WithChannelSize(256))
	out, cancel := relayRedisMessages(ctx, src, func() { _ = pubsub.Close() })

	channels := make([]<-chan []byte, 0, len(patterns))
	for range patterns {
		channels = append(channels, out)
	}
	return channels, cancel, nil
}

func (c *redisClient) Incr(ctx context.Context, key string) (int64, error) {
	start := time.Now()
	result, err := c.client.Incr(ctx, c.qualify(key)).Result()
	recordRedisOperation("incr", start, err)
	return result, err
}

func (c *redisClient) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	start := time.Now()
	result, err := c.client.Expire(ctx, c.qualify(key), ttl).Result()
	recordRedisOperation("expire", start, err)
	return result, err
}

func (c *redisClient) XAdd(ctx context.Context, stream string, values map[string]interface{}) (string, error) {
	start := time.Now()
	result, err := c.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: c.qualify(stream),
		Values: values,
	}).Result()
	recordRedisOperation("xadd", start, err)
	return result, err
}

func (c *redisClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	qualified := c.qualify(stream)
	start := time.Now()
	res, err := c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{qualified, ">"},
		Count:    count,
		Block:    0,
	}).Result()
	if isRedisNoGroup(err) {
		if createErr := c.client.XGroupCreateMkStream(ctx, qualified, group, "0").Err(); createErr != nil && !isRedisBusyGroup(createErr) {
			recordRedisOperation("xgroup_create", start, createErr)
			return nil, createErr
		}
		res, err = c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{qualified, ">"},
			Count:    count,
			Block:    0,
		}).Result()
	}
	if errors.Is(err, goredis.Nil) {
		recordRedisOperation("xreadgroup", start, nil)
		return nil, nil
	}
	if err != nil {
		recordRedisOperation("xreadgroup", start, err)
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
	recordRedisOperation("xreadgroup", start, nil)
	return messages, nil
}

func (c *redisClient) XAck(ctx context.Context, stream, group string, ids ...string) error {
	start := time.Now()
	err := c.client.XAck(ctx, c.qualify(stream), group, ids...).Err()
	recordRedisOperation("xack", start, err)
	return err
}

func (c *redisClient) Lock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	qualified := c.qualify(key)
	start := time.Now()
	ok, err := c.client.SetNX(ctx, qualified, token, ttl).Result()
	if err != nil {
		recordRedisOperation("lock", start, err)
		return "", err
	}
	if !ok {
		observability.Default().RecordRedisOperation("lock", "contention", time.Since(start))
		return "", fmt.Errorf("lock already held for key: %s", key)
	}
	recordRedisOperation("lock", start, nil)
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
	start := time.Now()
	res, err := c.client.Eval(ctx, script, []string{c.qualify(key)}, token).Int64()
	recordRedisOperation("unlock", start, err)
	return res == 1, err
}

func (c *redisClient) PFAdd(ctx context.Context, key string, els ...interface{}) (int64, error) {
	start := time.Now()
	result, err := c.client.PFAdd(ctx, c.qualify(key), els...).Result()
	recordRedisOperation("pfadd", start, err)
	return result, err
}

func (c *redisClient) PFCount(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	qualified := make([]string, len(keys))
	for i, k := range keys {
		qualified[i] = c.qualify(k)
	}
	start := time.Now()
	result, err := c.client.PFCount(ctx, qualified...).Result()
	recordRedisOperation("pfcount", start, err)
	return result, err
}

func (c *redisClient) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	start := time.Now()
	err := c.client.Set(ctx, c.qualify(key), value, ttl).Err()
	recordRedisOperation("set", start, err)
	return err
}

func (c *redisClient) SetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	start := time.Now()
	pipe := c.client.Pipeline()
	for key, value := range values {
		pipe.Set(ctx, c.qualify(key), value, ttl)
	}
	_, err := pipe.Exec(ctx)
	recordRedisOperation("set_many", start, err)
	return err
}

func (c *redisClient) Get(ctx context.Context, key string) ([]byte, error) {
	start := time.Now()
	result, err := c.client.Get(ctx, c.qualify(key)).Bytes()
	if errors.Is(err, goredis.Nil) {
		recordRedisOperation("get", start, nil)
		return nil, nil
	}
	recordRedisOperation("get", start, err)
	return result, err
}

func (c *redisClient) GetMany(ctx context.Context, keys ...string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	qualified := make([]string, len(keys))
	for i, key := range keys {
		qualified[i] = c.qualify(key)
	}
	start := time.Now()
	values, err := c.client.MGet(ctx, qualified...).Result()
	if err != nil {
		recordRedisOperation("get_many", start, err)
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for i, value := range values {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			out[keys[i]] = []byte(typed)
		case []byte:
			out[keys[i]] = append([]byte(nil), typed...)
		default:
			out[keys[i]] = []byte(fmt.Sprint(typed))
		}
	}
	recordRedisOperation("get_many", start, nil)
	return out, nil
}

func (c *redisClient) SetGetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) (map[string][]byte, error) {
	if len(values) == 0 {
		return map[string][]byte{}, nil
	}
	start := time.Now()
	pipe := c.client.Pipeline()
	gets := make(map[string]*goredis.StringCmd, len(values))
	for key, value := range values {
		qualified := c.qualify(key)
		pipe.Set(ctx, qualified, value, ttl)
		gets[key] = pipe.Get(ctx, qualified)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, goredis.Nil) {
		recordRedisOperation("set_get_many", start, err)
		return nil, err
	}
	out := make(map[string][]byte, len(values))
	for key, cmd := range gets {
		value, err := cmd.Bytes()
		if errors.Is(err, goredis.Nil) {
			continue
		}
		if err != nil {
			recordRedisOperation("set_get_many", start, err)
			return nil, err
		}
		out[key] = value
	}
	recordRedisOperation("set_get_many", start, nil)
	return out, nil
}

func (c *redisClient) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	qualified := make([]string, len(keys))
	for i, k := range keys {
		qualified[i] = c.qualify(k)
	}
	start := time.Now()
	err := c.client.Del(ctx, qualified...).Err()
	recordRedisOperation("del", start, err)
	return err
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

func recordRedisOperation(operation string, start time.Time, err error) {
	state := "success"
	if err != nil {
		state = "error"
	}
	observability.Default().RecordRedisOperation(operation, state, time.Since(start))
}

func isRedisNoGroup(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "NOGROUP")
}

func isRedisBusyGroup(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "BUSYGROUP")
}

func (c *shardedClient) Publish(ctx context.Context, channel string, payload []byte) error {
	var firstErr error
	for _, shard := range c.shards {
		if err := shard.Publish(ctx, channel, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *shardedClient) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	return c.shards[0].Subscribe(ctx, channel)
}

func (c *shardedClient) PSubscribe(ctx context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	return c.shards[0].PSubscribe(ctx, patterns...)
}

func (c *shardedClient) XAdd(ctx context.Context, stream string, values map[string]interface{}) (string, error) {
	return c.shard(stream).XAdd(ctx, stream, values)
}

func (c *shardedClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	return c.shard(stream).XReadGroup(ctx, stream, group, consumer, count)
}

func (c *shardedClient) XAck(ctx context.Context, stream, group string, ids ...string) error {
	return c.shard(stream).XAck(ctx, stream, group, ids...)
}

func (c *shardedClient) Incr(ctx context.Context, key string) (int64, error) {
	return c.shard(key).Incr(ctx, key)
}

func (c *shardedClient) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return c.shard(key).Expire(ctx, key, ttl)
}

func (c *shardedClient) Lock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return c.shard(key).Lock(ctx, key, ttl)
}

func (c *shardedClient) Unlock(ctx context.Context, key, token string) (bool, error) {
	return c.shard(key).Unlock(ctx, key, token)
}

func (c *shardedClient) PFAdd(ctx context.Context, key string, els ...interface{}) (int64, error) {
	return c.shard(key).PFAdd(ctx, key, els...)
}

func (c *shardedClient) PFCount(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	return c.shard(keys[0]).PFCount(ctx, keys...)
}

func (c *shardedClient) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.shard(key).Set(ctx, key, value, ttl)
}

func (c *shardedClient) SetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	grouped := make(map[*redisClient]map[string]interface{}, len(c.shards))
	for key, value := range values {
		shard := c.shard(key)
		if grouped[shard] == nil {
			grouped[shard] = map[string]interface{}{}
		}
		grouped[shard][key] = value
	}
	var firstErr error
	for shard, shardValues := range grouped {
		if err := shard.SetMany(ctx, shardValues, ttl); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *shardedClient) Get(ctx context.Context, key string) ([]byte, error) {
	return c.shard(key).Get(ctx, key)
}

func (c *shardedClient) GetMany(ctx context.Context, keys ...string) (map[string][]byte, error) {
	grouped := make(map[*redisClient][]string, len(c.shards))
	for _, key := range keys {
		shard := c.shard(key)
		grouped[shard] = append(grouped[shard], key)
	}
	out := make(map[string][]byte, len(keys))
	var firstErr error
	for shard, shardKeys := range grouped {
		values, err := shard.GetMany(ctx, shardKeys...)
		if err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		for key, value := range values {
			out[key] = value
		}
	}
	return out, firstErr
}

func (c *shardedClient) SetGetMany(ctx context.Context, values map[string]interface{}, ttl time.Duration) (map[string][]byte, error) {
	if len(values) == 0 {
		return map[string][]byte{}, nil
	}
	grouped := make(map[*redisClient]map[string]interface{}, len(c.shards))
	for key, value := range values {
		shard := c.shard(key)
		if grouped[shard] == nil {
			grouped[shard] = map[string]interface{}{}
		}
		grouped[shard][key] = value
	}
	out := make(map[string][]byte, len(values))
	var firstErr error
	for shard, shardValues := range grouped {
		values, err := shard.SetGetMany(ctx, shardValues, ttl)
		if err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		for key, value := range values {
			out[key] = value
		}
	}
	return out, firstErr
}

func (c *shardedClient) Del(ctx context.Context, keys ...string) error {
	var firstErr error
	for _, key := range keys {
		if err := c.shard(key).Del(ctx, key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *shardedClient) Close() error {
	var firstErr error
	for _, shard := range c.shards {
		if err := shard.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
