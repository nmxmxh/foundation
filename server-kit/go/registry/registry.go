package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"go.uber.org/zap"
)

// ConcurrencyOptions defines event registration throttling.
type ConcurrencyOptions = bootstrap.ConcurrencyOptions

type registeredMethod struct {
	handler      bootstrap.HandlerFunc
	typedHandler bootstrap.TypedHandlerFunc
	binding      *protoapi.Binding
}

type DispatchInput struct {
	Payload          map[string]any
	PayloadBytes     []byte
	PayloadEncoding  string
	ResponseEncoding string
	Metadata         map[string]any
}

type DispatchResult struct {
	Payload         map[string]any
	PayloadBytes    []byte
	PayloadEncoding string
	Stream          any
}

// ServiceRegistry routes request events to registered domain handlers.
type ServiceRegistry struct {
	mu              sync.RWMutex
	methods         map[string]registeredMethod
	redis           redis.Client
	handler         *graceful.Handler
	log             logger.Logger
	dispatchWorkers int
}

type Options struct {
	DispatchWorkers int
}

// New creates a new ServiceRegistry.
func New(redisClient redis.Client, gh *graceful.Handler, l logger.Logger) *ServiceRegistry {
	return NewWithOptions(redisClient, gh, l, Options{})
}

// NewWithOptions creates a new ServiceRegistry with configurable dispatch worker sizing.
func NewWithOptions(redisClient redis.Client, gh *graceful.Handler, l logger.Logger, opts Options) *ServiceRegistry {
	if l == nil {
		l, _ = logger.NewDefault()
	}
	dispatchWorkers := opts.DispatchWorkers
	if dispatchWorkers <= 0 {
		dispatchWorkers = 1
	}
	return &ServiceRegistry{
		methods:         map[string]registeredMethod{},
		redis:           redisClient,
		handler:         gh,
		log:             l.With(zap.String("component", "service_registry")),
		dispatchWorkers: dispatchWorkers,
	}
}

func (r *ServiceRegistry) Register(eventType string, handler bootstrap.HandlerFunc) error {
	return r.RegisterWithOptions(eventType, handler, ConcurrencyOptions{
		MaxConcurrent:  64,
		RequestsPerSec: 0,
		AcquireTimeout: 250 * time.Millisecond,
	})
}

func (r *ServiceRegistry) RegisterWithOptions(eventType string, handler bootstrap.HandlerFunc, opts ConcurrencyOptions) error {
	if strings.TrimSpace(eventType) == "" {
		return errors.New("event_type is required")
	}
	if handler == nil {
		return fmt.Errorf("handler is required for %s", eventType)
	}
	if !strings.HasSuffix(eventType, ":requested") {
		return fmt.Errorf("event_type %q must end with :requested", eventType)
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 64
	}

	controller := bootstrap.NewHandlerExecutionController(opts)
	wrapped := controller.Wrap(handler)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[eventType] = registeredMethod{handler: wrapped}
	r.log.Info("registered handler", zap.String("event_type", eventType))
	return nil
}

func (r *ServiceRegistry) RegisterTypedWithOptions(eventType string, binding protoapi.Binding, handler bootstrap.TypedHandlerFunc, opts ConcurrencyOptions) error {
	if strings.TrimSpace(eventType) == "" {
		return errors.New("event_type is required")
	}
	if handler == nil {
		return fmt.Errorf("typed handler is required for %s", eventType)
	}
	if !strings.HasSuffix(eventType, ":requested") {
		return fmt.Errorf("event_type %q must end with :requested", eventType)
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 64
	}

	currentBinding := binding
	controller := bootstrap.NewHandlerExecutionController(opts)
	wrapped := controller.WrapTyped(handler)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[eventType] = registeredMethod{
		typedHandler: wrapped,
		binding:      &currentBinding,
	}
	r.log.Info("registered typed handler", zap.String("event_type", eventType))
	return nil
}

// Listen starts the Redis subscription and dispatches events to registered handlers.
func (r *ServiceRegistry) Listen(ctx context.Context, patterns ...string) error {
	if r.redis == nil {
		return errors.New("redis client is required for Listen")
	}
	if len(patterns) == 0 {
		return errors.New("at least one pattern is required")
	}

	channels, cancel, err := r.redis.PSubscribe(ctx, patterns...)
	if err != nil {
		return err
	}

	r.log.Info("listening for events", zap.Strings("patterns", patterns), zap.Int("dispatch_workers", r.dispatchWorkers))

	payloadCh := make(chan []byte, 256)
	uniqueChannels := make(map[<-chan []byte]struct{}, len(channels))
	for _, ch := range channels {
		uniqueChannels[ch] = struct{}{}
	}

	for ch := range uniqueChannels {
		go func(ch <-chan []byte) {
			for {
				select {
				case <-ctx.Done():
					return
				case payload, ok := <-ch:
					if !ok {
						return
					}
					select {
					case payloadCh <- payload:
					case <-ctx.Done():
						return
					}
				}
			}
		}(ch)
	}

	for workerIndex := 0; workerIndex < r.dispatchWorkers; workerIndex++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case payload := <-payloadCh:
					r.dispatchEnvelope(ctx, payload)
				}
			}
		}()
	}

	go func() {
		<-ctx.Done()
		cancel()
	}()

	return nil
}

func (r *ServiceRegistry) dispatchEnvelope(ctx context.Context, payload []byte) {
	var env struct {
		EventType     string          `json:"event_type"`
		Payload       json.RawMessage `json:"payload"`
		Metadata      json.RawMessage `json:"metadata"`
		CorrelationID string          `json:"correlation_id"`
	}

	if err := json.Unmarshal(payload, &env); err != nil {
		r.log.Error("failed to unmarshal event envelope", zap.Error(err))
		return
	}

	r.mu.RLock()
	method, ok := r.methods[env.EventType]
	r.mu.RUnlock()

	if !ok {
		// Silent ignore or debug/warn?
		return
	}

	// Prepare metadata and inject correlation
	var metaMap map[string]any
	if len(env.Metadata) > 0 {
		_ = json.Unmarshal(env.Metadata, &metaMap)
	}
	if metaMap == nil {
		metaMap = make(map[string]any)
	}
	if env.CorrelationID != "" {
		metaMap["correlation_id"] = env.CorrelationID
	}

	// Enrich context
	ctx = metadata.NewContext(ctx, metaMap)

	if method.typedHandler != nil && method.binding != nil {
		req, err := protoapi.DecodeByEncoding(*method.binding, protoapi.PayloadEncodingJSON, nil, []byte(env.Payload), metaMap)
		if err != nil {
			r.log.Error("failed to decode typed payload", zap.String("event_type", env.EventType), zap.Error(err))
			return
		}
		_, err = method.typedHandler(ctx, req)
		if err != nil && r.handler != nil {
			r.handler.Error(ctx, strings.TrimSuffix(env.EventType, ":requested"), "event processing failed", err, metaMap, "")
		}
		return
	}

	// Legacy map-based handler
	var payloadMap map[string]any
	_ = json.Unmarshal(env.Payload, &payloadMap)
	_, err := method.handler(ctx, payloadMap)
	if err != nil && r.handler != nil {
		r.handler.Error(ctx, strings.TrimSuffix(env.EventType, ":requested"), "event processing failed", err, metaMap, "")
	}
}

func (r *ServiceRegistry) Dispatch(ctx context.Context, eventType string, payload map[string]any) (map[string]any, bool, error) {
	result, ok, err := r.DispatchInput(ctx, eventType, DispatchInput{
		Payload:          payload,
		PayloadEncoding:  protoapi.PayloadEncodingJSON,
		ResponseEncoding: protoapi.PayloadEncodingJSON,
	})
	if err != nil {
		return nil, ok, err
	}
	return result.Payload, ok, nil
}

func (r *ServiceRegistry) DispatchInput(ctx context.Context, eventType string, input DispatchInput) (DispatchResult, bool, error) {
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return DispatchResult{}, false, err
	}

	r.mu.RLock()
	method, ok := r.methods[eventType]
	r.mu.RUnlock()
	if !ok {
		return DispatchResult{}, false, nil
	}

	input.PayloadEncoding = normalizeEncoding(input.PayloadEncoding)
	input.ResponseEncoding = normalizeEncoding(input.ResponseEncoding)
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}

	if method.typedHandler != nil && method.binding != nil {
		request, err := protoapi.DecodeByEncoding(*method.binding, input.PayloadEncoding, input.Payload, input.PayloadBytes, input.Metadata)
		if err != nil {
			return DispatchResult{}, true, err
		}
		response, err := method.typedHandler(ctx, request)
		if err != nil {
			return DispatchResult{}, true, err
		}
		payloadMap, err := method.binding.EncodeResponseMap(response)
		if err != nil {
			return DispatchResult{}, true, err
		}
		result := DispatchResult{
			Payload:         payloadMap,
			PayloadEncoding: protoapi.PayloadEncodingJSON,
		}
		if input.ResponseEncoding == protoapi.PayloadEncodingProtobuf {
			payloadBytes, err := method.binding.EncodeResponseBytes(response)
			if err != nil {
				return DispatchResult{}, true, err
			}
			result.PayloadBytes = payloadBytes
			result.PayloadEncoding = protoapi.PayloadEncodingProtobuf
		}
		return result, true, nil
	}

	if input.PayloadEncoding == protoapi.PayloadEncodingProtobuf {
		return DispatchResult{}, true, fmt.Errorf("handler %q does not support protobuf payload dispatch", eventType)
	}
	response, err := method.handler(ctx, input.Payload)
	if err != nil {
		return DispatchResult{}, true, err
	}

	result := DispatchResult{
		PayloadEncoding: protoapi.PayloadEncodingJSON,
	}
	if m, ok := response.(map[string]any); ok {
		result.Payload = m
	} else {
		result.Stream = response
	}
	return result, true, nil
}

func (r *ServiceRegistry) RegisteredEventTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.methods))
	for eventType := range r.methods {
		out = append(out, eventType)
	}
	return out
}

func normalizeEncoding(value string) string {
	switch value {
	case "", protoapi.PayloadEncodingJSON:
		return protoapi.PayloadEncodingJSON
	case protoapi.PayloadEncodingProtobuf:
		return protoapi.PayloadEncodingProtobuf
	default:
		return value
	}
}
