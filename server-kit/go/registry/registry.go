package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/intelligence"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"google.golang.org/protobuf/proto"
)

// ConcurrencyOptions defines event registration throttling.
type ConcurrencyOptions = bootstrap.ConcurrencyOptions

type registeredMethod struct {
	handler      bootstrap.HandlerFunc
	typedHandler bootstrap.TypedHandlerFunc
	binding      *protoapi.Binding
	requestPool  *sync.Pool
}

type DispatchInput struct {
	Payload          extension.Object
	PayloadBytes     []byte
	PayloadEncoding  string
	ResponseEncoding string
	Metadata         extension.Object
}

type DispatchResult struct {
	Payload         extension.Object
	PayloadBytes    []byte
	PayloadEncoding string
	Stream          any
}

type MetricsSnapshot struct {
	RegisteredHandlers uint64
	DispatchWorkers    int
	UnknownEvents      uint64
}

// ServiceRegistry routes request events to registered domain handlers.
type ServiceRegistry struct {
	mu              sync.RWMutex
	methods         map[string]registeredMethod
	redis           redis.Client
	handler         *graceful.Handler
	log             logger.Logger
	dispatchWorkers int
	intelligence    *intelligence.Injector
	unknownEvents   atomic.Uint64
}

type Options struct {
	DispatchWorkers int
	Intelligence    *intelligence.Injector
}

// New creates a new ServiceRegistry.
func New(redisClient redis.Client, gh *graceful.Handler, l logger.Logger) *ServiceRegistry {
	return NewWithOptions(redisClient, gh, l, Options{})
}

// NewWithOptions creates a new ServiceRegistry with configurable dispatch worker sizing.
func NewWithOptions(redisClient redis.Client, gh *graceful.Handler, l logger.Logger, opts Options) *ServiceRegistry {
	if l == nil {
		l = logger.Default()
	}
	dispatchWorkers := opts.DispatchWorkers
	if dispatchWorkers <= 0 {
		dispatchWorkers = 1
	}
	return &ServiceRegistry{
		methods:         map[string]registeredMethod{},
		redis:           redisClient,
		handler:         gh,
		log:             l.With("component", "service_registry"),
		dispatchWorkers: dispatchWorkers,
		intelligence:    opts.Intelligence,
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
	r.log.Info("registered handler", "event_type", eventType)
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
	var requestPool *sync.Pool
	if currentBinding.AllowsProtobufDecodeReuse() {
		requestPool = &sync.Pool{
			New: func() any {
				msg, err := currentBinding.NewRequest()
				if err != nil {
					return nil
				}
				return msg
			},
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods[eventType] = registeredMethod{
		typedHandler: wrapped,
		binding:      &currentBinding,
		requestPool:  requestPool,
	}
	r.log.Info("registered typed handler", "event_type", eventType)
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

	r.log.InfoContext(ctx, "listening for events", "patterns", patterns, "dispatch_workers", r.dispatchWorkers)

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
					if ctx.Err() != nil {
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
		go func(workerIndex int) {
			for {
				select {
				case <-ctx.Done():
					return
				case payload := <-payloadCh:
					if ctx.Err() != nil {
						return
					}
					r.dispatchEnvelope(ctx, payload)
				}
			}
		}(workerIndex)
	}

	go func() {
		<-ctx.Done()
		cancel()
	}()

	return nil
}

func (r *ServiceRegistry) dispatchEnvelope(ctx context.Context, payload []byte) {
	env, err := eventcontract.Decode(payload)
	if err != nil {
		r.log.ErrorContext(ctx, "failed to decode event envelope", "error", err)
		return
	}

	r.mu.RLock()
	method, ok := r.methods[env.EventType]
	r.mu.RUnlock()

	if !ok {
		r.unknownEvents.Add(1)
		r.log.DebugContext(ctx, "unknown event type ignored", "event_type", env.EventType)
		return
	}
	if env.PayloadEncoding != protoapi.PayloadEncodingProtobuf {
		if err := env.MaterializePayload(); err != nil {
			r.log.ErrorContext(ctx, "failed to materialize event payload", "event_type", env.EventType, "error", err)
			return
		}
	}

	// Prepare metadata and inject correlation
	metaObject := env.Metadata.Clone()
	if env.CorrelationID != "" {
		metaObject["correlation_id"] = extension.String(env.CorrelationID)
	}
	payloadObject := env.Payload.Clone()

	ctx = metadata.NewContextObject(ctx, metaObject)
	if r.intelligence != nil {
		ctx, metaObject, _ = r.intelligence.Inject(ctx, intelligence.Input{
			EventType:    env.EventType,
			Payload:      payloadObject,
			PayloadBytes: env.PayloadBytes,
			Metadata:     metaObject,
		})
	}

	if method.typedHandler != nil && method.binding != nil {
		req, pooled, err := method.decodeRequest(env.PayloadEncoding, payloadObject, env.PayloadBytes, metaObject)
		if err != nil {
			r.log.ErrorContext(ctx, "failed to decode typed payload", "event_type", env.EventType, "error", err)
			return
		}
		_, err = method.typedHandler(ctx, req)
		method.releaseRequest(req, pooled)
		if err != nil && r.handler != nil {
			r.handler.Error(ctx, strings.TrimSuffix(env.EventType, ":requested"), "event processing failed", err, metaObject, "")
		}
		return
	}

	// Legacy map-based handler
	if env.PayloadEncoding == protoapi.PayloadEncodingProtobuf {
		r.log.ErrorContext(ctx, "handler does not support protobuf payload dispatch", "event_type", env.EventType)
		return
	}
	_, err = method.handler(ctx, payloadObject)
	if err != nil && r.handler != nil {
		r.handler.Error(ctx, strings.TrimSuffix(env.EventType, ":requested"), "event processing failed", err, metaObject, "")
	}
}

func (r *ServiceRegistry) MetricsSnapshot() MetricsSnapshot {
	r.mu.RLock()
	registered := len(r.methods)
	r.mu.RUnlock()
	return MetricsSnapshot{
		RegisteredHandlers: uint64(registered),
		DispatchWorkers:    r.dispatchWorkers,
		UnknownEvents:      r.unknownEvents.Load(),
	}
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
	if input.Payload == nil {
		input.Payload = extension.Object{}
	}
	if input.Metadata == nil {
		input.Metadata = extension.Object{}
	}
	ctx = metadata.NewContextObject(ctx, input.Metadata)
	if r.intelligence != nil {
		ctx, input.Metadata, _ = r.intelligence.Inject(ctx, intelligence.Input{
			EventType:    eventType,
			Payload:      input.Payload,
			PayloadBytes: input.PayloadBytes,
			Metadata:     input.Metadata,
		})
	}

	if method.typedHandler != nil && method.binding != nil {
		input.ResponseEncoding = normalizeResponseEncoding(input.ResponseEncoding, input.PayloadEncoding)
		request, pooled, err := method.decodeRequest(input.PayloadEncoding, input.Payload, input.PayloadBytes, input.Metadata)
		if err != nil {
			return DispatchResult{}, true, err
		}
		response, err := method.typedHandler(ctx, request)
		if err != nil {
			method.releaseRequest(request, pooled)
			return DispatchResult{}, true, err
		}
		result := DispatchResult{
			PayloadEncoding: input.ResponseEncoding,
		}
		if input.ResponseEncoding == protoapi.PayloadEncodingProtobuf {
			payloadBytes, err := method.binding.EncodeResponseBytes(response)
			if err != nil {
				method.releaseRequest(request, pooled)
				return DispatchResult{}, true, err
			}
			method.releaseRequest(request, pooled)
			result.PayloadBytes = payloadBytes
			result.PayloadEncoding = protoapi.PayloadEncodingProtobuf
			return result, true, nil
		}
		responseObject, err := method.binding.EncodeResponseObject(response)
		if err != nil {
			method.releaseRequest(request, pooled)
			return DispatchResult{}, true, err
		}
		method.releaseRequest(request, pooled)
		result.Payload = responseObject
		return result, true, nil
	}

	input.ResponseEncoding = normalizeEncoding(input.ResponseEncoding)
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
	if m, ok := response.(extension.Object); ok {
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

func normalizeResponseEncoding(value, requestEncoding string) string {
	if value == "" {
		return normalizeEncoding(requestEncoding)
	}
	return normalizeEncoding(value)
}

func (m registeredMethod) decodeRequest(encoding string, payload extension.Object, payloadBytes []byte, metadata extension.Object) (proto.Message, bool, error) {
	if normalizeEncoding(encoding) != protoapi.PayloadEncodingProtobuf || m.requestPool == nil || m.binding == nil {
		msg, err := protoapi.DecodeObjectByEncoding(*m.binding, encoding, payload, payloadBytes, metadata)
		return msg, false, err
	}
	request, ok := m.requestPool.Get().(proto.Message)
	if !ok || request == nil {
		var err error
		request, err = m.binding.NewRequest()
		if err != nil {
			return nil, false, err
		}
	}
	msg, err := m.binding.DecodeRequestBytesIntoObject(request, payloadBytes, metadata, protoapi.DecodeRequestBytesIntoOptions{
		CompleteMessage: true,
	})
	if err != nil {
		proto.Reset(request)
		m.requestPool.Put(request)
		return nil, false, err
	}
	return msg, true, nil
}

func (m registeredMethod) releaseRequest(request proto.Message, pooled bool) {
	if !pooled || request == nil || m.requestPool == nil {
		return
	}
	m.requestPool.Put(request)
}
