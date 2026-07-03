package graceful

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/riverqueue/river"
)

var (
	ErrRiverClientRequired = errors.New("graceful: river client is required")
	// ErrEmitTxUnsupported is returned when a caller passes a non-nil pgx.Tx to
	// an emitter that cannot honor transactional publish semantics (i.e. the
	// direct Redis fallback). Surfacing this loudly prevents silent dual-writes
	// where a producer commits its business row and then loses the event on a
	// mid-flight crash. Wire a River-backed emitter for producers that need the
	// transactional outbox.
	ErrEmitTxUnsupported = errors.New("graceful: emitter does not support transactional publish; use the river-backed emitter")
)

// EventEmitter emits lifecycle events for command outcomes.
//
// Contract: callers that hold an open pgx.Tx MUST call EmitEventTx with that
// tx so the enqueue and business write commit atomically. EmitEvent is only
// valid for genuinely tx-less call sites. Passing a non-nil tx to an emitter
// that cannot honor it returns ErrEmitTxUnsupported rather than silently
// falling back to a dual write.
//
// Payload must be an extension.Object. Callers build it explicitly at the
// emit site; there is no reflection-based struct fallback. This makes payload
// shape reviewable and avoids silent JSON-marshal degradation.
//
// Ownership: the emitter takes ownership of payload on entry — no clone at
// the boundary. Callers MUST NOT mutate or reuse the object after calling.
// Build a fresh extension.Object per emit (SuccessContext.ToObject and
// ErrorContext.ToObject already do this).
type EventEmitter interface {
	EmitEvent(ctx context.Context, eventType string, payload extension.Object, metadata extension.Object) error
	EmitEventTx(ctx context.Context, tx pgx.Tx, eventType string, payload extension.Object, metadata extension.Object) error
}

// Scheduler abstracts async job scheduling and transactional variants.
type Scheduler interface {
	Schedule(ctx context.Context, job river.JobArgs, runAt time.Time) error
	ScheduleTx(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time) error
	ScheduleTxWithOpts(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time, opts *river.InsertOpts) error
}

// Cache defines optional result caching support.
type Cache interface {
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
}

// CacheInfo defines a cache write intent for Success responses.
type CacheInfo struct {
	Key string
	TTL time.Duration
}

// SuccessContext is the canonical success payload shape.
//
// Result is an extension.Object so the outgoing envelope Payload is a typed
// object end-to-end. Scalar results should be wrapped, e.g.
// extension.Object{"value": extension.String("ok")}.
type SuccessContext struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	Result    extension.Object `json:"result"`
	Timestamp time.Time        `json:"timestamp"`
}

// ToObject renders the success context as an extension.Object payload.
func (s *SuccessContext) ToObject() extension.Object {
	if s == nil {
		return extension.Object{}
	}
	return extension.Object{
		"code":      extension.String(s.Code),
		"message":   extension.String(s.Message),
		"result":    extension.ObjectValue(s.Result),
		"timestamp": extension.String(s.Timestamp.UTC().Format(time.RFC3339Nano)),
	}
}

// ErrorContext is the canonical error payload shape.
type ErrorContext struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Cause     string    `json:"cause"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
}

// ToObject renders the error context as an extension.Object payload.
func (e *ErrorContext) ToObject() extension.Object {
	if e == nil {
		return extension.Object{}
	}
	return extension.Object{
		"code":      extension.String(e.Code),
		"message":   extension.String(e.Message),
		"cause":     extension.String(e.Cause),
		"timestamp": extension.String(e.Timestamp.UTC().Format(time.RFC3339Nano)),
		"service":   extension.String(e.Service),
	}
}

// Handler centralizes success/error signaling, event publication, and scheduling.
type Handler struct {
	Log          logger.Logger
	EventEmitter EventEmitter
	Cache        Cache
	Scheduler    Scheduler
	Service      string
	Version      string
	EventEnabled bool
}

// Option configures handler behavior.
type Option func(*Handler)

func WithLogger(l logger.Logger) Option {
	return func(h *Handler) {
		h.Log = l
	}
}

func WithEventEmitter(emitter EventEmitter) Option {
	return func(h *Handler) {
		h.EventEmitter = emitter
	}
}

func WithScheduler(scheduler Scheduler) Option {
	return func(h *Handler) {
		h.Scheduler = scheduler
	}
}

func WithCache(cache Cache) Option {
	return func(h *Handler) {
		h.Cache = cache
	}
}

func WithService(service string) Option {
	return func(h *Handler) {
		h.Service = service
	}
}

func WithVersion(version string) Option {
	return func(h *Handler) {
		h.Version = version
	}
}

func WithEventEnabled(enabled bool) Option {
	return func(h *Handler) {
		h.EventEnabled = enabled
	}
}

// NewHandler creates a graceful handler with safe defaults.
func NewHandler(opts ...Option) *Handler {
	h := &Handler{
		Service:      "server_kit",
		Version:      "v1",
		EventEnabled: true,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Success records a successful operation and emits <action>:success when configured.
func (h *Handler) Success(ctx context.Context, action string, msg string, result extension.Object, metadata extension.Object, entityID string, cacheInfo *CacheInfo) *SuccessContext {
	success := &SuccessContext{
		Code:      "ok",
		Message:   msg,
		Result:    result,
		Timestamp: time.Now().UTC(),
	}

	if h.Log != nil {
		h.Log.InfoContext(ctx, "operation success",
			"action", action,
			"entity_id", entityID,
			"service", h.Service,
		)
	}

	if cacheInfo != nil && h.Cache != nil && cacheInfo.Key != "" {
		ttl := cacheInfo.TTL
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		if err := h.Cache.Set(ctx, cacheInfo.Key, result, ttl); err != nil && h.Log != nil {
			h.Log.WarnContext(ctx, "cache write failed",
				"cache_key", cacheInfo.Key,
				"action", action,
				"error", err,
			)
		}
	}

	if h.EventEnabled && h.EventEmitter != nil && ctx.Err() == nil {
		eventType := ensureTerminalState(action, "success")
		meta := withCorrelationIDFromContext(ctx, metadata)
		if err := h.EventEmitter.EmitEvent(ctx, eventType, success.ToObject(), meta); err != nil && h.Log != nil {
			h.Log.WarnContext(ctx, "success event emit failed",
				"event_type", eventType,
				"error", err,
			)
		}
	}

	return success
}

// Error records a failed operation and emits <action>:failed when configured.
// The metadata argument is augmented, not trusted as authoritative: PrepareForEmit
// preserves caller-provided extra fields but always overwrites correlation_id from ctx.
func (h *Handler) Error(ctx context.Context, action string, msg string, cause error, metadata extension.Object, entityID string) {
	errContext := &ErrorContext{
		Code:      "error",
		Message:   msg,
		Cause:     errorString(cause),
		Timestamp: time.Now().UTC(),
		Service:   h.Service,
	}

	if h.Log != nil {
		h.Log.ErrorContext(ctx, "operation failed",
			"action", action,
			"entity_id", entityID,
			"service", h.Service,
			"error", cause,
		)
	}

	if h.EventEnabled && h.EventEmitter != nil && ctx.Err() == nil {
		eventType := ensureTerminalState(action, "failed")
		meta := withCorrelationIDFromContext(ctx, metadata)
		if err := h.EventEmitter.EmitEvent(ctx, eventType, errContext.ToObject(), meta); err != nil && h.Log != nil {
			h.Log.WarnContext(ctx, "failure event emit failed",
				"event_type", eventType,
				"error", err,
			)
		}
	}
}

func ensureTerminalState(action, terminal string) string {
	return eventcontract.EnsureTerminalState(action, terminal)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func withCorrelationIDFromContext(ctx context.Context, metadata extension.Object) extension.Object {
	return metautil.PrepareObjectForEmit(ctx, metadata)
}

// PublishEventArgs captures event publication job arguments for River.
type PublishEventArgs struct {
	EventType      string           `json:"event_type"`
	Payload        extension.Object `json:"payload"`
	Metadata       extension.Object `json:"metadata"`
	SchemaVersion  string           `json:"schema_version,omitempty"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
}

func (PublishEventArgs) Kind() string { return "publish_event" }

// InsertOptions captures queueing and timing hints for scheduled jobs.
type InsertOptions struct {
	Queue       string
	MaxAttempts int
	ScheduledAt time.Time
}

// InMemoryEventEmitter emits directly to an in-memory bus (Local/Dev).
type InMemoryEventEmitter struct {
	Bus eventcontract.Bus
}

func NewInMemoryEventEmitter(bus eventcontract.Bus) *InMemoryEventEmitter {
	return &InMemoryEventEmitter{Bus: bus}
}

func (e *InMemoryEventEmitter) EmitEvent(ctx context.Context, eventType string, payload extension.Object, metadata extension.Object) error {
	if e == nil || e.Bus == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}

	meta := withCorrelationIDFromContext(ctx, metadata)
	// correlation_id is always set by PrepareObjectForEmit; read it directly
	// instead of scanning the map again via pickCorrelation.
	corr, _ := meta.GetString("correlation_id")
	envelope := eventcontract.Envelope{
		EventType:     eventType,
		Payload:       payload,
		Metadata:      meta,
		CorrelationID: corr,
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	}
	envelope.Normalize()
	return e.Bus.Publish(ctx, envelope)
}

func (e *InMemoryEventEmitter) EmitEventTx(ctx context.Context, _ pgx.Tx, eventType string, payload extension.Object, metadata extension.Object) error {
	return e.EmitEvent(ctx, eventType, payload, metadata)
}

// InMemoryScheduler is a simple scheduler implementation for local development.
type InMemoryScheduler struct {
	mu   sync.Mutex
	jobs []ScheduledJob
}

// ScheduledJob captures an in-memory schedule record.
type ScheduledJob struct {
	Job   river.JobArgs
	RunAt time.Time
	Opts  *InsertOptions
}

func NewInMemoryScheduler() *InMemoryScheduler {
	return &InMemoryScheduler{jobs: make([]ScheduledJob, 0, 32)}
}

func (s *InMemoryScheduler) Schedule(ctx context.Context, job river.JobArgs, runAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, ScheduledJob{Job: job, RunAt: runAt.UTC()})
	return nil
}

func (s *InMemoryScheduler) ScheduleTx(ctx context.Context, _ pgx.Tx, job river.JobArgs, runAt time.Time) error {
	return s.Schedule(ctx, job, runAt)
}

func (s *InMemoryScheduler) ScheduleTxWithOpts(ctx context.Context, _ pgx.Tx, job river.JobArgs, runAt time.Time, opts *river.InsertOpts) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, ScheduledJob{
		Job:   job,
		RunAt: runAt.UTC(),
	})
	return nil
}

func (s *InMemoryScheduler) Jobs() []ScheduledJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ScheduledJob, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// RedisEventEmitter emits directly to a Redis bus (fallback/simple).
type RedisEventEmitter struct {
	Bus eventcontract.Bus
}

func NewRedisEventEmitter(bus eventcontract.Bus) *RedisEventEmitter {
	return &RedisEventEmitter{Bus: bus}
}

func (e *RedisEventEmitter) EmitEvent(ctx context.Context, eventType string, payload extension.Object, metadata extension.Object) error {
	if e == nil || e.Bus == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}

	meta := withCorrelationIDFromContext(ctx, metadata)
	corr, _ := meta.GetString("correlation_id")
	envelope := eventcontract.Envelope{
		EventType:     eventType,
		Payload:       payload,
		Metadata:      meta,
		CorrelationID: corr,
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	}
	envelope.Normalize()
	return e.Bus.Publish(ctx, envelope)
}

func (e *RedisEventEmitter) EmitEventTx(ctx context.Context, tx pgx.Tx, eventType string, payload extension.Object, metadata extension.Object) error {
	if tx != nil {
		return ErrEmitTxUnsupported
	}
	return e.EmitEvent(ctx, eventType, payload, metadata)
}

// RiverEventEmitter implements EventEmitter using River for Transactional Outbox.
type RiverEventEmitter struct {
	riverClient *river.Client[pgx.Tx]
}

func NewRiverEventEmitter(client *river.Client[pgx.Tx]) *RiverEventEmitter {
	return &RiverEventEmitter{riverClient: client}
}

func (e *RiverEventEmitter) EmitEvent(ctx context.Context, eventType string, payload extension.Object, metadata extension.Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	if e == nil || e.riverClient == nil {
		return ErrRiverClientRequired
	}
	meta := withCorrelationIDFromContext(ctx, metadata)
	_, err := e.riverClient.Insert(ctx, PublishEventArgs{
		EventType:      eventType,
		Payload:        payload,
		Metadata:       meta,
		SchemaVersion:  eventcontract.EnvelopeSchemaVersion,
		IdempotencyKey: pickIdempotency(meta),
	}, nil)
	return err
}

func (e *RiverEventEmitter) EmitEventTx(ctx context.Context, tx pgx.Tx, eventType string, payload extension.Object, metadata extension.Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	if e == nil || e.riverClient == nil {
		return ErrRiverClientRequired
	}
	meta := withCorrelationIDFromContext(ctx, metadata)
	_, err := e.riverClient.InsertTx(ctx, tx, PublishEventArgs{
		EventType:      eventType,
		Payload:        payload,
		Metadata:       meta,
		SchemaVersion:  eventcontract.EnvelopeSchemaVersion,
		IdempotencyKey: pickIdempotency(meta),
	}, nil)
	return err
}

// RiverScheduler implements Scheduler using River.
type RiverScheduler struct {
	riverClient *river.Client[pgx.Tx]
}

func NewRiverScheduler(client *river.Client[pgx.Tx]) *RiverScheduler {
	return &RiverScheduler{riverClient: client}
}

func (s *RiverScheduler) Schedule(ctx context.Context, job river.JobArgs, runAt time.Time) error {
	if s == nil || s.riverClient == nil {
		return ErrRiverClientRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.riverClient.Insert(ctx, job, &river.InsertOpts{ScheduledAt: runAt})
	return err
}

func (s *RiverScheduler) ScheduleTx(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time) error {
	if s == nil || s.riverClient == nil {
		return ErrRiverClientRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.riverClient.InsertTx(ctx, tx, job, &river.InsertOpts{ScheduledAt: runAt})
	return err
}

func (s *RiverScheduler) ScheduleTxWithOpts(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time, opts *river.InsertOpts) error {
	if s == nil || s.riverClient == nil {
		return ErrRiverClientRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts == nil {
		opts = &river.InsertOpts{ScheduledAt: runAt}
	} else if opts.ScheduledAt.IsZero() {
		opts.ScheduledAt = runAt
	}
	_, err := s.riverClient.InsertTx(ctx, tx, job, opts)
	return err
}

func pickCorrelation(metadata extension.Object) string {
	if metadata == nil {
		return ""
	}
	corr, _ := metadata.GetString("correlation_id")
	return corr
}

func pickIdempotency(metadata extension.Object) string {
	if metadata == nil {
		return ""
	}
	key, _ := metadata.GetString("idempotency_key")
	return key
}
