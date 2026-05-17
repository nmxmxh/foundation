// Package cache provides standardized cache-aside patterns with TTL policies and invalidation.
// It abstracts cache operations and supports multiple backends (memory, Redis).
//
// Usage:
//
//	c := cache.New(cache.Config{
//	    Backend: cache.NewRedisBackend(redisClient),
//	    DefaultTTL: 5 * time.Minute,
//	    Prefix: "myapp:",
//	})
//
//	// Get or compute
//	user, err := cache.GetOrSet(ctx, c, "user:123", func() (*User, error) {
//	    return db.GetUser(ctx, 123)
//	})
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Backend defines the interface for cache storage.
type Backend interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	DeletePattern(ctx context.Context, pattern string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// Config holds cache configuration.
type Config struct {
	// Backend is the storage backend.
	Backend Backend

	// DefaultTTL is the default time-to-live for cached items.
	DefaultTTL time.Duration

	// Prefix is prepended to all keys.
	Prefix string

	// Serializer is used to encode/decode values. Default: JSON.
	Serializer Serializer

	// OnHit is called on cache hits.
	OnHit func(key string)

	// OnMiss is called on cache misses.
	OnMiss func(key string)

	// OnError is called on cache errors.
	OnError func(key string, err error)
}

// Serializer handles value encoding/decoding.
type Serializer interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONSerializer is the default JSON serializer.
type JSONSerializer struct{}

func (JSONSerializer) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONSerializer) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Cache provides cache operations.
type Cache struct {
	config Config
	sf     singleflight.Group
}

// New creates a new cache instance.
func New(cfg Config) *Cache {
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 5 * time.Minute
	}
	if cfg.Serializer == nil {
		cfg.Serializer = JSONSerializer{}
	}
	return &Cache{config: cfg}
}

func (c *Cache) key(k string) string {
	return c.config.Prefix + k
}

// Get retrieves a value from cache.
func (c *Cache) Get(ctx context.Context, key string, dest any) error {
	data, err := c.config.Backend.Get(ctx, c.key(key))
	if err != nil {
		if c.config.OnError != nil {
			c.config.OnError(key, err)
		}
		return err
	}

	if data == nil {
		if c.config.OnMiss != nil {
			c.config.OnMiss(key)
		}
		return ErrNotFound
	}

	if c.config.OnHit != nil {
		c.config.OnHit(key)
	}

	return c.config.Serializer.Unmarshal(data, dest)
}

// Set stores a value in cache with optional TTL.
func (c *Cache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	data, err := c.config.Serializer.Marshal(value)
	if err != nil {
		return err
	}

	t := c.config.DefaultTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		t = ttl[0]
	}

	return c.config.Backend.Set(ctx, c.key(key), data, t)
}

// Delete removes a value from cache.
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.config.Backend.Delete(ctx, c.key(key))
}

// DeletePattern removes all keys matching a pattern.
func (c *Cache) DeletePattern(ctx context.Context, pattern string) error {
	return c.config.Backend.DeletePattern(ctx, c.key(pattern))
}

// Exists checks if a key exists in cache.
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	return c.config.Backend.Exists(ctx, c.key(key))
}

// GetOrSet retrieves from cache or computes and stores the value.
// It uses singleflight to ensure only one concurrent computation happens per key.
func GetOrSet[T any](ctx context.Context, c *Cache, key string, compute func() (T, error), ttl ...time.Duration) (T, error) {
	var result T

	// Try to get from cache first
	err := c.Get(ctx, key, &result)
	if err == nil {
		return result, nil
	}

	// Cache miss or error - compute value with singleflight protection
	val, err, _ := c.sf.Do(key, func() (any, error) {
		// Double check cache inside singleflight to handle race conditions
		var innerResult T
		if err := c.Get(ctx, key, &innerResult); err == nil {
			return innerResult, nil
		}

		res, err := compute()
		if err != nil {
			return nil, err
		}

		// Store in cache
		_ = c.Set(ctx, key, res, ttl...)
		return res, nil
	})

	if err != nil {
		return result, err
	}

	return val.(T), nil
}

// Invalidator provides cache invalidation helpers.
type Invalidator struct {
	cache  *Cache
	tags   map[string][]string
	tagsMu sync.RWMutex
}

// NewInvalidator creates a new cache invalidator.
func NewInvalidator(c *Cache) *Invalidator {
	return &Invalidator{
		cache: c,
		tags:  make(map[string][]string),
	}
}

// Tag associates a key with one or more tags.
func (i *Invalidator) Tag(key string, tags ...string) {
	i.tagsMu.Lock()
	defer i.tagsMu.Unlock()
	for _, tag := range tags {
		i.tags[tag] = append(i.tags[tag], key)
	}
}

// InvalidateTag invalidates all keys with the given tag.
func (i *Invalidator) InvalidateTag(ctx context.Context, tag string) error {
	i.tagsMu.Lock()
	keys := i.tags[tag]
	delete(i.tags, tag)
	i.tagsMu.Unlock()

	for _, key := range keys {
		if err := i.cache.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// ErrNotFound is returned when a key is not found in cache.
var ErrNotFound = fmt.Errorf("cache: key not found")

// MemoryBackend is an in-memory cache backend.
type MemoryBackend struct {
	mu     sync.RWMutex
	items  map[string]memoryItem
	stopCh chan struct{}
	once   sync.Once
}

type memoryItem struct {
	data      []byte
	expiresAt time.Time
}

// NewMemoryBackend creates a new in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	m := &MemoryBackend{
		items:  make(map[string]memoryItem),
		stopCh: make(chan struct{}),
	}
	go m.cleanup()
	return m
}

func (m *MemoryBackend) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for k, v := range m.items {
				if !v.expiresAt.IsZero() && now.After(v.expiresAt) {
					delete(m.items, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *MemoryBackend) Close() error {
	if m == nil {
		return nil
	}
	m.once.Do(func() {
		close(m.stopCh)
	})
	return nil
}

func (m *MemoryBackend) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.items[key]
	if !ok {
		return nil, nil
	}

	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return nil, nil
	}

	return item.data, nil
}

func (m *MemoryBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	item := memoryItem{data: value}
	if ttl > 0 {
		item.expiresAt = time.Now().Add(ttl)
	}

	m.items[key] = item
	return nil
}

func (m *MemoryBackend) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

func (m *MemoryBackend) DeletePattern(ctx context.Context, pattern string) error {
	// Simple prefix matching for memory backend
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := pattern
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix = pattern[:len(pattern)-1]
	}

	for k := range m.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.items, k)
		}
	}
	return nil
}

func (m *MemoryBackend) Exists(ctx context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.items[key]
	if !ok {
		return false, nil
	}

	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return false, nil
	}

	return true, nil
}

// TTL policies for common use cases

// TTLPolicy defines standard TTL durations.
type TTLPolicy struct {
	// Short is for frequently changing data (1 minute).
	Short time.Duration
	// Medium is for moderately changing data (5 minutes).
	Medium time.Duration
	// Long is for rarely changing data (1 hour).
	Long time.Duration
	// Extended is for very stable data (24 hours).
	Extended time.Duration
}

// DefaultTTLPolicy returns sensible TTL defaults.
func DefaultTTLPolicy() TTLPolicy {
	return TTLPolicy{
		Short:    1 * time.Minute,
		Medium:   5 * time.Minute,
		Long:     1 * time.Hour,
		Extended: 24 * time.Hour,
	}
}

// CacheKey generates a cache key from parts.
func CacheKey(parts ...any) string {
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = fmt.Sprintf("%v", p)
	}
	return strings.Join(strs, ":")
}

func stringJoin(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(strs[0])
	for i := 1; i < len(strs); i++ {
		result.WriteString(sep + strs[i])
	}
	return result.String()
}
