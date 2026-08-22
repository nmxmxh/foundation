package redis

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
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

const (
	// scanCount is the SCAN COUNT hint per iteration for pattern deletion.
	scanCount = int64(512)
	// deleteChunkSize bounds DEL fan-in per round trip during pattern deletes.
	deleteChunkSize = 500
	// defaultDeleteLimit bounds one DeletePattern sweep when callers pass no limit.
	defaultDeleteLimit = int64(10000)
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
	XAdd(ctx context.Context, stream string, values Values) (string, error)
	XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error)
	XReadGroupPending(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error)
	XAck(ctx context.Context, stream, group string, ids ...string) error

	// Coordination & Locks
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Lock(ctx context.Context, key string, ttl time.Duration) (string, error)
	Unlock(ctx context.Context, key, token string) (bool, error)

	// Analytics & Cardinality (HyperLogLog)
	PFAdd(ctx context.Context, key string, els ...any) (int64, error)
	PFCount(ctx context.Context, keys ...string) (int64, error)

	// Primitives
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	Close() error
}

// BatchClient is the optional round-trip amortization surface for cache
// hydration and write-through paths. Callers should use it when several
// independent Redis keys cross the network boundary together.
type BatchClient interface {
	SetMany(ctx context.Context, values Values, ttl time.Duration) error
	GetMany(ctx context.Context, keys ...string) (map[string][]byte, error)
	SetGetMany(ctx context.Context, values Values, ttl time.Duration) (map[string][]byte, error)
}

// StreamBatchClient is the optional round-trip amortization surface for Redis
// Stream append paths. It keeps durable event relay lanes from paying one
// socket round trip per pending event.
type StreamBatchClient interface {
	XAddMany(ctx context.Context, stream string, entries []Values) ([]string, []error)
	XAddManyField(ctx context.Context, stream string, field string, payloads [][]byte) ([]string, []error)
}

// KeyAdminClient is the optional cache-administration surface for existence
// probes and bounded pattern-scoped deletion. Cache backends use it to
// support cross-process invalidation without unbounded KEYS scans. Deleted
// keys are returned in the caller's namespace so they can be passed back to
// Get or Del symmetrically.
type KeyAdminClient interface {
	Exists(ctx context.Context, key string) (bool, error)
	// DeletePattern deletes keys matching pattern and returns them. At most
	// limit keys are deleted; limit <= 0 selects a driver default bound.
	DeletePattern(ctx context.Context, pattern string, limit int64) ([]string, error)
}

// SortedSetClient is the optional ranked-lane surface for bounded recent-item
// buffers, feeds, and leaderboards.
//
// It is an optional capability, asserted at startup like the other extension
// surfaces: consumers should degrade to their in-process fallback when the
// connected driver does not implement it. Scores are float64 per Redis
// semantics. Ranks ascend by score with lexicographically-ascending tie
// breaks; ZRevRange is the exact mirror of that order, so tied members come
// back reverse-lexicographic — identical on every driver.
type SortedSetClient interface {
	// ZAdd inserts or updates one member and reports whether the member was
	// newly added (1) versus rescored (0).
	ZAdd(ctx context.Context, key string, score float64, member string) (int64, error)
	// ZRevRange returns members in descending score order within the
	// inclusive [start, stop] rank window of the reversed set. Negative
	// indices count from the end (-1 = last), matching Redis. Newest-N reads
	// use (0, N-1).
	ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	// ZRemRangeByRank removes members in the inclusive ascending-rank window
	// and returns how many were removed. Bounded buffers trim with
	// (0, -(keep+1)).
	ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error)
	// ZCard returns the current member count.
	ZCard(ctx context.Context, key string) (int64, error)
}

// CoordinationBatchClient is the optional round-trip amortization surface for
// coordination keys that need counter+TTL updates and notification fanout.
// WebSocket route registration uses this during reconnect storms so each
// connection does not pay several sequential Redis round trips.
type CoordinationBatchClient interface {
	IncrExpireMany(ctx context.Context, keys []string, ttl time.Duration) []error
	PublishMany(ctx context.Context, channel string, payloads [][]byte) []error
}

type Value struct {
	Field string
	Value any
}

type Values []Value

func Field(field string, value any) Value {
	return Value{Field: field, Value: value}
}

func (values Values) Get(field string) (any, bool) {
	for i := range values {
		if values[i].Field == field {
			return values[i].Value, true
		}
	}
	return nil, false
}

func (values Values) Clone() Values {
	out := make(Values, 0, len(values))
	for i := range values {
		if values[i].Field == "" {
			continue
		}
		out = append(out, Value{Field: values[i].Field, Value: copyInterfaceValue(values[i].Value)})
	}
	return out
}

func (values Values) InterfaceMap() map[string]any {
	out := make(map[string]any, len(values))
	for i := range values {
		if values[i].Field == "" {
			continue
		}
		out[values[i].Field] = values[i].Value
	}
	return out
}

func (values Values) InterfaceSlice() []any {
	out := make([]any, 0, len(values)*2)
	for i := range values {
		if values[i].Field == "" {
			continue
		}
		out = append(out, values[i].Field, values[i].Value)
	}
	return out
}

func valuesFromMap(input map[string]any) Values {
	out := make(Values, 0, len(input))
	for field, value := range input {
		out = append(out, Value{Field: field, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Field < out[j].Field
	})
	return out
}

// StreamMessage represents a message read from a Redis stream.
type StreamMessage struct {
	ID     string
	Values Values
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
	zsets              map[string]map[string]float64
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
		zsets:              map[string]map[string]float64{},
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

func (c *memoryClient) PublishMany(_ context.Context, channel string, payloads [][]byte) []error {
	errs := make([]error, len(payloads))
	if len(payloads) == 0 {
		return errs
	}
	qualified := c.qualify(channel)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		err := fmt.Errorf("memory redis client is closed")
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	for i, payload := range payloads {
		if payload == nil {
			continue
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
		errs[i] = nil
	}
	return errs
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

func (c *memoryClient) IncrExpireMany(_ context.Context, keys []string, ttl time.Duration) []error {
	errs := make([]error, len(keys))
	if len(keys) == 0 {
		return errs
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		err := fmt.Errorf("memory redis client is closed")
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	now := time.Now()
	for i, key := range keys {
		if key == "" {
			continue
		}
		qualified := c.qualify(key)
		c.expireIfNeededLocked(qualified, now)
		if value, ok := c.values[qualified]; ok {
			current, err := strconv.ParseInt(string(value.data), 10, 64)
			if err != nil {
				errs[i] = fmt.Errorf("value is not an integer or out of range")
				continue
			}
			current++
			c.values[qualified] = memoryValue{data: []byte(strconv.FormatInt(current, 10))}
			c.counters[qualified] = current
		} else {
			c.counters[qualified]++
			c.values[qualified] = memoryValue{data: []byte(strconv.FormatInt(c.counters[qualified], 10))}
		}
		if ttl > 0 {
			c.expiries[qualified] = now.Add(ttl)
		}
	}
	return errs
}

func (c *memoryClient) XAdd(_ context.Context, stream string, values Values) (string, error) {
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
		Values: values.Clone(),
	})
	return id, nil
}

func (c *memoryClient) XAddMany(_ context.Context, stream string, entries []Values) ([]string, []error) {
	ids := make([]string, len(entries))
	errs := make([]error, len(entries))
	if len(entries) == 0 {
		return ids, errs
	}
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		err := fmt.Errorf("memory redis client is closed")
		for i := range errs {
			errs[i] = err
		}
		return ids, errs
	}
	now := time.Now().UnixMilli()
	for i, values := range entries {
		c.streamSequences[qualified]++
		id := fmt.Sprintf("%d-%d", now, c.streamSequences[qualified])
		c.streams[qualified] = append(c.streams[qualified], StreamMessage{
			ID:     id,
			Values: values.Clone(),
		})
		ids[i] = id
	}
	return ids, errs
}

func (c *memoryClient) XAddManyField(_ context.Context, stream string, field string, payloads [][]byte) ([]string, []error) {
	ids := make([]string, len(payloads))
	errs := make([]error, len(payloads))
	if len(payloads) == 0 {
		return ids, errs
	}
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		err := fmt.Errorf("memory redis client is closed")
		for i := range errs {
			errs[i] = err
		}
		return ids, errs
	}
	now := time.Now().UnixMilli()
	for i, payload := range payloads {
		c.streamSequences[qualified]++
		id := fmt.Sprintf("%d-%d", now, c.streamSequences[qualified])
		c.streams[qualified] = append(c.streams[qualified], StreamMessage{
			ID: id,
			Values: Values{
				Field(field, append([]byte(nil), payload...)),
			},
		})
		ids[i] = id
	}
	return ids, errs
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

func (c *memoryClient) XReadGroupPending(_ context.Context, stream, group, _ string, count int64) ([]StreamMessage, error) {
	qualified := c.qualify(stream)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	g := c.memoryStreamGroupLocked(qualified, group)
	if len(g.pending) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(g.pending))
	for id := range g.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	limit := len(ids)
	if count > 0 && int(count) < limit {
		limit = int(count)
	}
	out := make([]StreamMessage, 0, limit)
	for _, id := range ids[:limit] {
		out = append(out, cloneStreamMessage(g.pending[id]))
	}
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

func (c *memoryClient) Set(_ context.Context, key string, value any, ttl time.Duration) error {
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

func (c *memoryClient) SetMany(_ context.Context, values Values, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("memory redis client is closed")
	}
	now := time.Now()
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		qualified := c.qualify(field.Field)
		c.values[qualified] = memoryValue{data: bytesFromValue(field.Value)}
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

func (c *memoryClient) SetGetMany(_ context.Context, values Values, ttl time.Duration) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("memory redis client is closed")
	}
	now := time.Now()
	out := make(map[string][]byte, len(values))
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		qualified := c.qualify(field.Field)
		data := bytesFromValue(field.Value)
		c.values[qualified] = memoryValue{data: data}
		if ttl > 0 {
			c.expiries[qualified] = now.Add(ttl)
		} else {
			delete(c.expiries, qualified)
		}
		out[field.Field] = append([]byte(nil), data...)
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

func (c *memoryClient) Exists(_ context.Context, key string) (bool, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.memoryKeyExistsLocked(qualified, time.Now()), nil
}

func (c *memoryClient) DeletePattern(_ context.Context, pattern string, limit int64) ([]string, error) {
	if limit <= 0 {
		limit = defaultDeleteLimit
	}
	qualifiedPattern := c.qualify(pattern)

	c.mu.Lock()
	candidates := make([]string, 0, len(c.values))
	for key := range c.values {
		if redisPatternMatches(qualifiedPattern, key) {
			candidates = append(candidates, key)
		}
	}
	for _, key := range candidates {
		c.deleteKeyLocked(key)
	}
	c.mu.Unlock()

	deleted := make([]string, 0, len(candidates))
	for _, key := range candidates {
		if int64(len(deleted)) >= limit {
			break
		}
		deleted = append(deleted, strings.TrimPrefix(key, c.prefix+":"))
	}
	return deleted, nil
}

func (c *memoryClient) ZAdd(_ context.Context, key string, score float64, member string) (int64, error) {
	qualified := c.qualify(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, fmt.Errorf("memory redis client is closed")
	}
	c.expireIfNeededLocked(qualified, time.Now())
	set := c.zsets[qualified]
	if set == nil {
		set = map[string]float64{}
		c.zsets[qualified] = set
	}
	added := int64(1)
	if _, exists := set[member]; exists {
		added = 0
	}
	set[member] = score
	return added, nil
}

// zsetAscending orders members the way Redis ranks them: score ascending,
// ties lexicographically ascending. Ranks and rank-window trims are defined
// against this order.
func zsetAscending(set map[string]float64) []string {
	members := make([]string, 0, len(set))
	for member := range set {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		if set[members[i]] != set[members[j]] {
			return set[members[i]] < set[members[j]]
		}
		return members[i] < members[j]
	})
	return members
}

// normalizeRankWindow clamps an inclusive Redis [start, stop] window (with
// negative indices counting from the end) onto [0, n-1]. An empty window
// returns ok=false.
func normalizeRankWindow(start, stop, n int64) (int64, int64, bool) {
	length := int64(n)
	if start < 0 {
		start += length
	}
	if stop < 0 {
		stop += length
	}
	if start < 0 {
		start = 0
	}
	if stop >= length {
		stop = length - 1
	}
	if length == 0 || start > stop {
		return 0, 0, false
	}
	return start, stop, true
}

func (c *memoryClient) zrevRange(qualified string, start, stop int64) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.expireIfNeededLocked(qualified, time.Now())
	set := c.zsets[qualified]
	if len(set) == 0 {
		return []string{}, nil
	}
	ascending := zsetAscending(set)
	length := int64(len(ascending))
	begin, end, ok := normalizeRankWindow(start, stop, length)
	if !ok {
		return []string{}, nil
	}
	// The window indexes the DESCENDING set; descending[i] mirrors to
	// ascending[length-1-i]. Walking the ascending window backwards would
	// shift every negative stop by one rank.
	out := make([]string, 0, end-begin+1)
	for index := begin; index <= end; index++ {
		out = append(out, ascending[length-1-index])
	}
	return out, nil
}

func (c *memoryClient) zremRangeByRank(qualified string, start, stop int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireIfNeededLocked(qualified, time.Now())
	set := c.zsets[qualified]
	if len(set) == 0 {
		return 0, nil
	}
	ascending := zsetAscending(set)
	begin, end, ok := normalizeRankWindow(start, stop, int64(len(ascending)))
	if !ok {
		return 0, nil
	}
	for _, member := range ascending[begin : end+1] {
		delete(set, member)
	}
	if len(set) == 0 {
		delete(c.zsets, qualified)
	}
	return end - begin + 1, nil
}

func (c *memoryClient) ZRevRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	return c.zrevRange(c.qualify(key), start, stop)
}

func (c *memoryClient) ZRemRangeByRank(_ context.Context, key string, start, stop int64) (int64, error) {
	return c.zremRangeByRank(c.qualify(key), start, stop)
}

func (c *memoryClient) ZCard(_ context.Context, key string) (int64, error) {
	qualified := c.qualify(key)
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.expireIfNeededLocked(qualified, time.Now())
	return int64(len(c.zsets[qualified])), nil
}

func (c *memoryClient) PFAdd(_ context.Context, key string, els ...any) (int64, error) {
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
	if len(c.zsets[qualified]) > 0 {
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
	delete(c.zsets, qualified)
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

func bytesFromValue(value any) []byte {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), v...)
	case string:
		return []byte(v)
	default:
		return fmt.Append(nil, v)
	}
}

func cloneStreamMessage(message StreamMessage) StreamMessage {
	return StreamMessage{
		ID:     message.ID,
		Values: message.Values.Clone(),
	}
}

func copyInterfaceMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = copyInterfaceValue(value)
	}
	return out
}

func copyInterfaceValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...)
	case map[string]any:
		return copyInterfaceMap(typed)
	default:
		return typed
	}
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

// shardIndex is the sharded router's key-routing function — the "vindex" in
// Vitess terms: it maps a routing key to a shard index. FNV-1a is computed
// inline over the string's bytes. The previous fnv.New32a()+[]byte(key) form is
// also zero-allocation today because escape analysis inlines the hasher, but
// that is a compiler-dependent property of a hot path on every sharded op;
// computing the hash inline makes the zero-allocation guarantee structural
// (pinned by TestShardIndexDoesNotAllocate) and shaves the hasher indirection.
// The result is bit-identical to int(fnv.New32a().Sum32()) % n on 64-bit hosts
// (proven by TestShardIndexMatchesStdlibFNVOracle), so this changes cost, not
// placement — existing sharded data stays on the same shard.
func shardIndex(key string, n int) int {
	if n <= 1 {
		return 0
	}
	const (
		fnvOffset32 = 2166136261
		fnvPrime32  = 16777619
	)
	h := uint32(fnvOffset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= fnvPrime32
	}
	// int(uint32) is a non-negative widening on 64-bit hosts, so int(h) % n is
	// in [0, n) — the exact reduction the previous hasher path used. (Kept as
	// int(h) % n rather than uint32(n) modulo to avoid a narrowing int->uint32
	// conversion; n is only reached here when > 1 per the guard above.)
	return int(h) % n
}

func (c *shardedClient) shard(key string) *redisClient {
	return c.shards[shardIndex(key, len(c.shards))]
}

func (c *redisClient) Publish(ctx context.Context, channel string, payload []byte) error {
	start := time.Now()
	err := c.client.Publish(ctx, c.qualify(channel), payload).Err()
	recordRedisOperation("publish", start, err)
	return err
}

func (c *redisClient) PublishMany(ctx context.Context, channel string, payloads [][]byte) []error {
	errs := make([]error, len(payloads))
	if len(payloads) == 0 {
		return errs
	}
	start := time.Now()
	qualified := c.qualify(channel)
	pipe := c.client.Pipeline()
	cmds := make([]*goredis.IntCmd, len(payloads))
	for i, payload := range payloads {
		cmds[i] = pipe.Publish(ctx, qualified, payload)
	}
	_, execErr := pipe.Exec(ctx)
	var firstErr error
	for i, cmd := range cmds {
		if err := cmd.Err(); err != nil {
			errs[i] = err
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if execErr != nil && firstErr == nil {
		firstErr = execErr
		errs[0] = execErr
	}
	recordRedisOperation("publish_many", start, firstErr)
	return errs
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

func (c *redisClient) IncrExpireMany(ctx context.Context, keys []string, ttl time.Duration) []error {
	errs := make([]error, len(keys))
	if len(keys) == 0 {
		return errs
	}
	start := time.Now()
	pipe := c.client.Pipeline()
	incrs := make([]*goredis.IntCmd, len(keys))
	expires := make([]*goredis.BoolCmd, len(keys))
	for i, key := range keys {
		if key == "" {
			continue
		}
		qualified := c.qualify(key)
		incrs[i] = pipe.Incr(ctx, qualified)
		if ttl > 0 {
			expires[i] = pipe.Expire(ctx, qualified, ttl)
		}
	}
	_, execErr := pipe.Exec(ctx)
	var firstErr error
	for i := range keys {
		if incrs[i] != nil {
			if err := incrs[i].Err(); err != nil {
				errs[i] = err
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}
		if expires[i] != nil {
			if err := expires[i].Err(); err != nil {
				errs[i] = err
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	if execErr != nil && firstErr == nil {
		firstErr = execErr
		errs[0] = execErr
	}
	recordRedisOperation("incr_expire_many", start, firstErr)
	return errs
}

func (c *redisClient) XAdd(ctx context.Context, stream string, values Values) (string, error) {
	start := time.Now()
	result, err := c.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: c.qualify(stream),
		Values: values.InterfaceSlice(),
	}).Result()
	recordRedisOperation("xadd", start, err)
	return result, err
}

func (c *redisClient) XAddMany(ctx context.Context, stream string, entries []Values) ([]string, []error) {
	ids := make([]string, len(entries))
	errs := make([]error, len(entries))
	if len(entries) == 0 {
		return ids, errs
	}
	start := time.Now()
	qualified := c.qualify(stream)
	pipe := c.client.Pipeline()
	cmds := make([]*goredis.StringCmd, len(entries))
	for i, values := range entries {
		cmds[i] = pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: qualified,
			Values: values.InterfaceSlice(),
		})
	}
	_, execErr := pipe.Exec(ctx)
	var firstErr error
	for i, cmd := range cmds {
		id, err := cmd.Result()
		ids[i] = id
		if err != nil {
			errs[i] = err
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if execErr != nil && firstErr == nil {
		firstErr = execErr
		errs[0] = execErr
	}
	recordRedisOperation("xadd_many", start, firstErr)
	return ids, errs
}

func (c *redisClient) XAddManyField(ctx context.Context, stream string, field string, payloads [][]byte) ([]string, []error) {
	ids := make([]string, len(payloads))
	errs := make([]error, len(payloads))
	if len(payloads) == 0 {
		return ids, errs
	}
	start := time.Now()
	qualified := c.qualify(stream)
	pipe := c.client.Pipeline()
	cmds := make([]*goredis.StringCmd, len(payloads))
	for i, payload := range payloads {
		cmds[i] = pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: qualified,
			Values: []any{field, payload},
		})
	}
	_, execErr := pipe.Exec(ctx)
	var firstErr error
	for i, cmd := range cmds {
		id, err := cmd.Result()
		ids[i] = id
		if err != nil {
			errs[i] = err
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if execErr != nil && firstErr == nil {
		firstErr = execErr
		errs[0] = execErr
	}
	recordRedisOperation("xadd_many", start, firstErr)
	return ids, errs
}

func (c *redisClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	return c.xReadGroup(ctx, stream, group, consumer, count, ">")
}

func (c *redisClient) XReadGroupPending(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	return c.xReadGroup(ctx, stream, group, consumer, count, "0")
}

func (c *redisClient) xReadGroup(ctx context.Context, stream, group, consumer string, count int64, id string) ([]StreamMessage, error) {
	qualified := c.qualify(stream)
	start := time.Now()
	res, err := c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{qualified, id},
		Count:    count,
		Block:    -1,
	}).Result()
	if isRedisNoGroup(err) {
		if createErr := c.client.XGroupCreateMkStream(ctx, qualified, group, "0").Err(); createErr != nil && !isRedisBusyGroup(createErr) {
			recordRedisOperation("xgroup_create", start, createErr)
			return nil, createErr
		}
		res, err = c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{qualified, id},
			Count:    count,
			Block:    -1,
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
				Values: valuesFromMap(xmsg.Values),
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

func (c *redisClient) PFAdd(ctx context.Context, key string, els ...any) (int64, error) {
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

func (c *redisClient) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	start := time.Now()
	err := c.client.Set(ctx, c.qualify(key), value, ttl).Err()
	recordRedisOperation("set", start, err)
	return err
}

func (c *redisClient) SetMany(ctx context.Context, values Values, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	start := time.Now()
	pipe := c.client.Pipeline()
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		pipe.Set(ctx, c.qualify(field.Field), field.Value, ttl)
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
			out[keys[i]] = fmt.Append(nil, typed)
		}
	}
	recordRedisOperation("get_many", start, nil)
	return out, nil
}

func (c *redisClient) SetGetMany(ctx context.Context, values Values, ttl time.Duration) (map[string][]byte, error) {
	if len(values) == 0 {
		return map[string][]byte{}, nil
	}
	start := time.Now()
	pipe := c.client.Pipeline()
	gets := make(map[string]*goredis.StringCmd, len(values))
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		qualified := c.qualify(field.Field)
		pipe.Set(ctx, qualified, field.Value, ttl)
		gets[field.Field] = pipe.Get(ctx, qualified)
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

func (c *redisClient) Exists(ctx context.Context, key string) (bool, error) {
	start := time.Now()
	count, err := c.client.Exists(ctx, c.qualify(key)).Result()
	recordRedisOperation("exists", start, err)
	return count > 0, err
}

func (c *redisClient) DeletePattern(ctx context.Context, pattern string, limit int64) ([]string, error) {
	if limit <= 0 {
		limit = defaultDeleteLimit
	}
	qualified := c.qualify(pattern)
	start := time.Now()
	var (
		collected []string
		batch     []string
		firstErr  error
		cursor    uint64
	)
	for int64(len(collected)) < limit && firstErr == nil {
		keys, next, err := c.client.Scan(ctx, cursor, qualified, scanCount).Result()
		if err != nil {
			firstErr = err
			break
		}
		cursor = next
		for _, key := range keys {
			collected = append(collected, strings.TrimPrefix(key, c.prefix+":"))
			batch = append(batch, key)
			if len(batch) >= deleteChunkSize {
				if err := c.client.Del(ctx, batch...).Err(); err != nil && firstErr == nil {
					firstErr = err
				}
				batch = batch[:0]
				if firstErr != nil {
					break
				}
			}
			if int64(len(collected)) >= limit {
				break
			}
		}
		if cursor == 0 || firstErr != nil {
			break
		}
	}
	if len(batch) > 0 && firstErr == nil {
		if err := c.client.Del(ctx, batch...).Err(); err != nil {
			firstErr = err
		}
	}
	recordRedisOperation("delete_pattern", start, firstErr)
	return collected, firstErr
}

func (c *redisClient) ZAdd(ctx context.Context, key string, score float64, member string) (int64, error) {
	start := time.Now()
	added, err := c.client.ZAdd(ctx, c.qualify(key), goredis.Z{Score: score, Member: member}).Result()
	recordRedisOperation("zadd", start, err)
	return added, err
}

func (c *redisClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	startedAt := time.Now()
	members, err := c.client.ZRevRange(ctx, c.qualify(key), start, stop).Result()
	recordRedisOperation("zrevrange", startedAt, err)
	return members, err
}

func (c *redisClient) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error) {
	startedAt := time.Now()
	removed, err := c.client.ZRemRangeByRank(ctx, c.qualify(key), start, stop).Result()
	recordRedisOperation("zremrangebyrank", startedAt, err)
	return removed, err
}

func (c *redisClient) ZCard(ctx context.Context, key string) (int64, error) {
	start := time.Now()
	cardinality, err := c.client.ZCard(ctx, c.qualify(key)).Result()
	recordRedisOperation("zcard", start, err)
	return cardinality, err
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

func (c *shardedClient) PublishMany(ctx context.Context, channel string, payloads [][]byte) []error {
	errs := make([]error, len(payloads))
	if len(payloads) == 0 {
		return errs
	}
	for _, shard := range c.shards {
		shardErrs := shard.PublishMany(ctx, channel, payloads)
		for i, err := range shardErrs {
			if err != nil && errs[i] == nil {
				errs[i] = err
			}
		}
	}
	return errs
}

func (c *shardedClient) Subscribe(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	return c.shards[0].Subscribe(ctx, channel)
}

func (c *shardedClient) PSubscribe(ctx context.Context, patterns ...string) ([]<-chan []byte, func(), error) {
	return c.shards[0].PSubscribe(ctx, patterns...)
}

func (c *shardedClient) XAdd(ctx context.Context, stream string, values Values) (string, error) {
	return c.shard(stream).XAdd(ctx, stream, values)
}

func (c *shardedClient) XAddMany(ctx context.Context, stream string, entries []Values) ([]string, []error) {
	return c.shard(stream).XAddMany(ctx, stream, entries)
}

func (c *shardedClient) XAddManyField(ctx context.Context, stream string, field string, payloads [][]byte) ([]string, []error) {
	return c.shard(stream).XAddManyField(ctx, stream, field, payloads)
}

func (c *shardedClient) XReadGroup(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	return c.shard(stream).XReadGroup(ctx, stream, group, consumer, count)
}

func (c *shardedClient) XReadGroupPending(ctx context.Context, stream, group, consumer string, count int64) ([]StreamMessage, error) {
	return c.shard(stream).XReadGroupPending(ctx, stream, group, consumer, count)
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

func (c *shardedClient) IncrExpireMany(ctx context.Context, keys []string, ttl time.Duration) []error {
	errs := make([]error, len(keys))
	if len(keys) == 0 {
		return errs
	}
	type indexedKey struct {
		index int
		key   string
	}
	grouped := make(map[*redisClient][]indexedKey, len(c.shards))
	for i, key := range keys {
		if key == "" {
			continue
		}
		shard := c.shard(key)
		grouped[shard] = append(grouped[shard], indexedKey{index: i, key: key})
	}
	for shard, shardKeys := range grouped {
		batchKeys := make([]string, len(shardKeys))
		for i, item := range shardKeys {
			batchKeys[i] = item.key
		}
		shardErrs := shard.IncrExpireMany(ctx, batchKeys, ttl)
		for i, err := range shardErrs {
			if err != nil {
				errs[shardKeys[i].index] = err
			}
		}
	}
	return errs
}

func (c *shardedClient) Lock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return c.shard(key).Lock(ctx, key, ttl)
}

func (c *shardedClient) Unlock(ctx context.Context, key, token string) (bool, error) {
	return c.shard(key).Unlock(ctx, key, token)
}

func (c *shardedClient) PFAdd(ctx context.Context, key string, els ...any) (int64, error) {
	return c.shard(key).PFAdd(ctx, key, els...)
}

func (c *shardedClient) PFCount(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	return c.shard(keys[0]).PFCount(ctx, keys...)
}

func (c *shardedClient) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return c.shard(key).Set(ctx, key, value, ttl)
}

func (c *shardedClient) SetMany(ctx context.Context, values Values, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	grouped := make(map[*redisClient]Values, len(c.shards))
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		shard := c.shard(field.Field)
		if grouped[shard] == nil {
			grouped[shard] = Values{}
		}
		grouped[shard] = append(grouped[shard], field)
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
		maps.Copy(out, values)
	}
	return out, firstErr
}

func (c *shardedClient) SetGetMany(ctx context.Context, values Values, ttl time.Duration) (map[string][]byte, error) {
	if len(values) == 0 {
		return map[string][]byte{}, nil
	}
	grouped := make(map[*redisClient]Values, len(c.shards))
	for _, field := range values {
		if field.Field == "" {
			continue
		}
		shard := c.shard(field.Field)
		if grouped[shard] == nil {
			grouped[shard] = Values{}
		}
		grouped[shard] = append(grouped[shard], field)
	}
	out := make(map[string][]byte, len(values))
	var firstErr error
	for shard, shardValues := range grouped {
		values, err := shard.SetGetMany(ctx, shardValues, ttl)
		if err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		maps.Copy(out, values)
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

func (c *shardedClient) Exists(ctx context.Context, key string) (bool, error) {
	return c.shard(key).Exists(ctx, key)
}

func (c *shardedClient) DeletePattern(ctx context.Context, pattern string, limit int64) ([]string, error) {
	if limit <= 0 {
		limit = defaultDeleteLimit
	}
	var (
		collected []string
		firstErr  error
	)
	for _, shard := range c.shards {
		remaining := limit - int64(len(collected))
		if remaining <= 0 {
			break
		}
		keys, err := shard.DeletePattern(ctx, pattern, remaining)
		collected = append(collected, keys...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return collected, firstErr
}

func (c *shardedClient) ZAdd(ctx context.Context, key string, score float64, member string) (int64, error) {
	return c.shard(key).ZAdd(ctx, key, score, member)
}

func (c *shardedClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.shard(key).ZRevRange(ctx, key, start, stop)
}

func (c *shardedClient) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error) {
	return c.shard(key).ZRemRangeByRank(ctx, key, start, stop)
}

func (c *shardedClient) ZCard(ctx context.Context, key string) (int64, error) {
	return c.shard(key).ZCard(ctx, key)
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
