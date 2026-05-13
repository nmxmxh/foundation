package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"google.golang.org/protobuf/proto"
)

// HandlerFunc is the normalized command handler signature.
type HandlerFunc func(context.Context, map[string]any) (any, error)

// TypedHandlerFunc is the normalized protobuf command handler signature.
type TypedHandlerFunc = protoapi.TypedHandlerFunc

// ServiceHandlers maps event types to handler functions.
type ServiceHandlers map[string]HandlerFunc

// TypedHandlerRegistration binds an event type to request/response protobuf contracts.
type TypedHandlerRegistration struct {
	Binding protoapi.Binding
	Handler TypedHandlerFunc
}

// TypedServiceHandlers maps event types to typed handler registrations.
type TypedServiceHandlers map[string]TypedHandlerRegistration

// ErrConcurrencyLimitReached indicates the handler could not acquire a worker slot in time.
var ErrConcurrencyLimitReached = errors.New("concurrency limit reached")

// ConcurrencyOptions controls registration-level throttling.
type ConcurrencyOptions struct {
	MaxConcurrent int
	// AcquireTimeout bounds how long a handler waits for a concurrency slot.
	// Zero means wait until context cancellation.
	AcquireTimeout time.Duration
	// RequestsPerSec is deprecated. Use RateLimitRate/RateLimitPeriod/RateLimitBurst instead.
	RequestsPerSec int
	// Optional token-bucket style rate limiter. Disabled when RateLimitRate <= 0.
	RateLimitRate   int
	RateLimitPeriod time.Duration
	RateLimitBurst  int
}

// RegistryAdapter is the minimal registration interface.
type RegistryAdapter interface {
	Register(eventType string, handler HandlerFunc) error
}

type advancedRegistryAdapter interface {
	RegisterWithOptions(eventType string, handler HandlerFunc, opts ConcurrencyOptions) error
}

type typedRegistryAdapter interface {
	RegisterTypedWithOptions(eventType string, binding protoapi.Binding, handler TypedHandlerFunc, opts ConcurrencyOptions) error
}

// HandlerExecutionController centralizes bounded concurrency and token-bucket pacing.
type HandlerExecutionController struct {
	workerSem      chan struct{}
	acquireTimeout time.Duration
	limiter        *tokenBucketLimiter
}

// RegisterHandlers registers all handlers from one domain.
func RegisterHandlers(adapter RegistryAdapter, handlers ServiceHandlers, opts ...ConcurrencyOptions) error {
	regOpts := defaultConcurrencyOptions()
	if len(opts) > 0 {
		regOpts = normalizeConcurrencyOptions(opts[0])
	}

	advanced, ok := adapter.(advancedRegistryAdapter)
	controller := NewHandlerExecutionController(regOpts)
	for eventType, handler := range handlers {
		if handler == nil {
			return fmt.Errorf("nil handler for event_type %s", eventType)
		}
		if err := validateRequestEventType(eventType); err != nil {
			return err
		}

		if ok {
			if err := advanced.RegisterWithOptions(eventType, handler, regOpts); err != nil {
				return err
			}
			continue
		}

		if err := adapter.Register(eventType, controller.Wrap(handler)); err != nil {
			return err
		}
	}
	return nil
}

// RegisterTypedHandlers registers protobuf-backed handlers from one domain.
func RegisterTypedHandlers(adapter RegistryAdapter, handlers TypedServiceHandlers, opts ...ConcurrencyOptions) error {
	regOpts := defaultConcurrencyOptions()
	if len(opts) > 0 {
		regOpts = normalizeConcurrencyOptions(opts[0])
	}

	typed, ok := adapter.(typedRegistryAdapter)
	if !ok {
		return errors.New("registry adapter does not support typed protobuf handlers")
	}

	for eventType, registration := range handlers {
		if registration.Handler == nil {
			return fmt.Errorf("nil typed handler for event_type %s", eventType)
		}
		if err := validateRequestEventType(eventType); err != nil {
			return err
		}
		if err := registration.Binding.Validate(); err != nil {
			return fmt.Errorf("invalid protobuf binding for %s: %w", eventType, err)
		}
		if err := typed.RegisterTypedWithOptions(eventType, registration.Binding, registration.Handler, regOpts); err != nil {
			return err
		}
	}
	return nil
}

// NewHandlerExecutionController builds the canonical bounded handler wrapper used by server-kit.
func NewHandlerExecutionController(opts ConcurrencyOptions) *HandlerExecutionController {
	normalized := normalizeConcurrencyOptions(opts)
	return &HandlerExecutionController{
		workerSem:      make(chan struct{}, normalized.MaxConcurrent),
		acquireTimeout: normalized.AcquireTimeout,
		limiter:        newTokenBucketLimiter(normalized),
	}
}

// Wrap applies the controller to map-based handlers.
func (c *HandlerExecutionController) Wrap(handler HandlerFunc) HandlerFunc {
	if c == nil {
		return NewHandlerExecutionController(defaultConcurrencyOptions()).Wrap(handler)
	}

	return func(ctx context.Context, payload map[string]any) (any, error) {
		release, err := acquireSlot(ctx, c.workerSem, c.acquireTimeout)
		if err != nil {
			return nil, err
		}
		defer release()

		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		return handler(ctx, payload)
	}
}

// WrapTyped applies the controller to protobuf-backed handlers.
func (c *HandlerExecutionController) WrapTyped(handler TypedHandlerFunc) TypedHandlerFunc {
	if c == nil {
		return NewHandlerExecutionController(defaultConcurrencyOptions()).WrapTyped(handler)
	}

	return func(ctx context.Context, payload proto.Message) (proto.Message, error) {
		release, err := acquireSlot(ctx, c.workerSem, c.acquireTimeout)
		if err != nil {
			return nil, err
		}
		defer release()

		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		return handler(ctx, payload)
	}
}

func defaultConcurrencyOptions() ConcurrencyOptions {
	return ConcurrencyOptions{
		MaxConcurrent:  64,
		RequestsPerSec: 0,
		AcquireTimeout: 250 * time.Millisecond,
	}
}

func normalizeConcurrencyOptions(opts ConcurrencyOptions) ConcurrencyOptions {
	normalized := defaultConcurrencyOptions()
	if opts.MaxConcurrent > 0 {
		normalized.MaxConcurrent = opts.MaxConcurrent
	}
	if opts.AcquireTimeout >= 0 {
		normalized.AcquireTimeout = opts.AcquireTimeout
	}
	if opts.RequestsPerSec >= 0 {
		normalized.RequestsPerSec = opts.RequestsPerSec
	}
	if opts.RateLimitRate > 0 {
		normalized.RateLimitRate = opts.RateLimitRate
	}
	if opts.RateLimitPeriod > 0 {
		normalized.RateLimitPeriod = opts.RateLimitPeriod
	}
	if opts.RateLimitBurst > 0 {
		normalized.RateLimitBurst = opts.RateLimitBurst
	}
	return normalized
}

func acquireSlot(ctx context.Context, sem chan struct{}, timeout time.Duration) (func(), error) {
	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}

	if timeout <= 0 {
		select {
		case sem <- struct{}{}:
			return func() { <-sem }, nil
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-timer.C:
		return nil, ErrConcurrencyLimitReached
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	}
}

type tokenBucketLimiter struct {
	mu          sync.Mutex
	tokens      float64
	burst       float64
	refillEvery time.Duration
	lastRefill  time.Time
}

func (l *tokenBucketLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	for {
		delay := l.reserveDelay(time.Now())
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return waitCtx.Err()
		case <-timer.C:
		}
	}
}

func (l *tokenBucketLimiter) reserveDelay(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elapsed := now.Sub(l.lastRefill); elapsed > 0 {
		l.tokens += float64(elapsed) / float64(l.refillEvery)
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.lastRefill = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return 0
	}
	missing := 1 - l.tokens
	return time.Duration(missing * float64(l.refillEvery))
}

func newTokenBucketLimiter(opts ConcurrencyOptions) *tokenBucketLimiter {
	rate := opts.RateLimitRate
	if rate <= 0 && opts.RequestsPerSec > 0 {
		rate = opts.RequestsPerSec
	}
	if rate <= 0 {
		return nil
	}

	period := opts.RateLimitPeriod
	if period <= 0 {
		period = time.Second
	}
	burst := opts.RateLimitBurst
	if burst <= 0 {
		burst = rate
	}

	refillEvery := period / time.Duration(rate)
	if refillEvery <= 0 {
		refillEvery = time.Millisecond
	}

	return &tokenBucketLimiter{
		tokens:      float64(burst),
		burst:       float64(burst),
		refillEvery: refillEvery,
		lastRefill:  time.Now(),
	}
}

// InMemoryRegistry is a simple registry used by server and tests.
type InMemoryRegistry struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{handlers: map[string]HandlerFunc{}}
}

func (r *InMemoryRegistry) Register(eventType string, handler HandlerFunc) error {
	if handler == nil {
		return errors.New("handler is required")
	}
	if err := validateRequestEventType(eventType); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[eventType] = handler
	return nil
}

func (r *InMemoryRegistry) Resolve(eventType string) (HandlerFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[eventType]
	return handler, ok
}

func validateRequestEventType(eventType string) error {
	if strings.TrimSpace(eventType) == "" {
		return errors.New("event_type is required")
	}
	if !strings.HasSuffix(strings.TrimSpace(eventType), ":requested") {
		return fmt.Errorf("event_type %q must end with :requested", eventType)
	}
	return eventcontract.ValidateEventType(eventType)
}
