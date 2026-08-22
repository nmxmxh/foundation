package cache

import (
	"context"
	"fmt"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

const redisDeleteLimit = int64(10000)

// RedisBackend stores cache entries in a shared Redis deployment through the
// rediskit transport. All processes pointing at the same Redis and prefix
// share entries and invalidation markers, so tag-based invalidation crosses
// process boundaries.
type RedisBackend struct {
	client rediskit.Client
	admin  rediskit.KeyAdminClient
}

// NewRedisBackend wraps a connected rediskit.Client as a cache Backend.
func NewRedisBackend(client rediskit.Client) *RedisBackend {
	if client == nil {
		panic("cache: NewRedisBackend requires a non-nil client")
	}
	admin, _ := client.(rediskit.KeyAdminClient)
	return &RedisBackend{client: client, admin: admin}
}

func (b *RedisBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := b.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("cache: redis get: %w", err)
	}
	return data, nil
}

func (b *RedisBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := b.client.Set(ctx, key, value, ttl); err != nil {
		return fmt.Errorf("cache: redis set: %w", err)
	}
	return nil
}

func (b *RedisBackend) Delete(ctx context.Context, key string) error {
	if err := b.client.Del(ctx, key); err != nil {
		return fmt.Errorf("cache: redis delete: %w", err)
	}
	return nil
}

func (b *RedisBackend) Exists(ctx context.Context, key string) (bool, error) {
	if b.admin != nil {
		exists, err := b.admin.Exists(ctx, key)
		if err != nil {
			return false, fmt.Errorf("cache: redis exists: %w", err)
		}
		return exists, nil
	}
	data, err := b.client.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("cache: redis exists probe: %w", err)
	}
	return data != nil, nil
}

func (b *RedisBackend) DeletePattern(ctx context.Context, pattern string) ([]string, error) {
	if b.admin == nil {
		return nil, fmt.Errorf("cache: redis client does not implement KeyAdminClient; pattern deletion unsupported")
	}
	keys, err := b.admin.DeletePattern(ctx, pattern, redisDeleteLimit)
	if err != nil {
		return keys, fmt.Errorf("cache: redis delete pattern: %w", err)
	}
	return keys, nil
}
