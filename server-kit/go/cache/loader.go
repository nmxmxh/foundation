package cache

import (
	"context"
	"errors"
	"time"
)

// LoadPolicy bounds shared cache construction. Zero fields use bounded defaults.
type LoadPolicy struct {
	Timeout     time.Duration
	MaxInflight int
	MaxWaiters  int
}

// NewWithLoadPolicy preserves Config and configures bounded context-aware construction.
func NewWithLoadPolicy(cfg Config, policy LoadPolicy) *Cache {
	c := New(cfg)
	c.loadConfig = policy
	return c
}

var (
	ErrLoadBusy      = errors.New("cache: shared load capacity reached")
	ErrLoadType      = errors.New("cache: shared load result type mismatch")
	ErrLoadExecution = errors.New("cache: loader panic")
)

type loadCall struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	value   any
	err     error
}

type loadValue[T any] struct {
	value T
}

func (c *Cache) loadPolicy() LoadPolicy {
	policy := c.loadConfig
	if policy.Timeout <= 0 {
		policy.Timeout = 5 * time.Second
	}
	if policy.MaxInflight <= 0 {
		policy.MaxInflight = 64
	}
	if policy.MaxWaiters <= 0 {
		policy.MaxWaiters = 64
	}
	return policy
}

// GetOrSetContext shares bounded source work while each caller retains its cancellation.
// Keys must include the authorization scope. Loaders must honor their supplied context.
// Cache failures retain the source fallback. This function adds no value cache.
func GetOrSetContext[T any](ctx context.Context, c *Cache, key string, load func(context.Context) (T, error), ttl ...time.Duration) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	var cached T
	if err := c.Get(ctx, key, &cached); err == nil {
		return cached, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	call, err := c.acquireLoad(ctx, key, func(loadCtx context.Context) (any, error) {
		var value T
		if err := c.Get(loadCtx, key, &value); err == nil {
			return loadValue[T]{value}, loadCtx.Err()
		}
		if err := loadCtx.Err(); err != nil {
			return nil, err
		}
		value, err := load(loadCtx)
		if err != nil {
			return nil, err
		}
		if err := loadCtx.Err(); err != nil {
			return nil, err
		}
		if err := c.Set(loadCtx, key, value, ttl...); err != nil && c.config.OnError != nil {
			c.config.OnError(key, err)
		}
		return loadValue[T]{value}, loadCtx.Err()
	})
	if err != nil {
		return zero, err
	}
	defer c.releaseLoad(call)
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-call.ctx.Done():
		select {
		case <-call.done:
		default:
			return zero, call.ctx.Err()
		}
	case <-call.done:
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if call.err != nil {
		return zero, call.err
	}
	value, ok := call.value.(loadValue[T])
	if !ok {
		return zero, ErrLoadType
	}
	return value.value, nil
}

func (c *Cache) acquireLoad(ctx context.Context, key string, run func(context.Context) (any, error)) (*loadCall, error) {
	policy := c.loadPolicy()
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	if call := c.loads[key]; call != nil {
		if call.ctx.Err() != nil || call.waiters >= policy.MaxWaiters {
			return nil, ErrLoadBusy
		}
		call.waiters++
		return call, nil
	}
	if len(c.loads) >= policy.MaxInflight {
		return nil, ErrLoadBusy
	}
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policy.Timeout)
	call := &loadCall{ctx: loadCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
	if c.loads == nil {
		c.loads = make(map[string]*loadCall)
	}
	c.loads[key] = call
	go c.runLoad(key, call, run)
	return call, nil
}

func (c *Cache) runLoad(key string, call *loadCall, run func(context.Context) (any, error)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			call.err = ErrLoadExecution
		}
		c.loadMu.Lock()
		delete(c.loads, key)
		close(call.done)
		call.cancel()
		c.loadMu.Unlock()
	}()
	call.value, call.err = run(call.ctx)
}

func (c *Cache) releaseLoad(call *loadCall) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	call.waiters--
	if call.waiters == 0 {
		call.cancel()
	}
}
