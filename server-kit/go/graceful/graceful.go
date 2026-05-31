package graceful

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/riverqueue/river"
)

// EventEmitter emits lifecycle events for command outcomes.
type EventEmitter interface {
	EmitEvent(ctx context.Context, eventType string, payload any, metadata map[string]any) error
	EmitEventTx(ctx context.Context, tx pgx.Tx, eventType string, payload any, metadata map[string]any) error
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
type SuccessContext struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Result    any       `json:"result"`
	Timestamp time.Time `json:"timestamp"`
}

// ErrorContext is the canonical error payload shape.
type ErrorContext struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Cause     string    `json:"cause"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
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
func (h *Handler) Success(ctx context.Context, action string, msg string, result any, metadata map[string]any, entityID string, cacheInfo *CacheInfo) *SuccessContext {
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

	if h.EventEnabled && h.EventEmitter != nil {
		eventType := ensureTerminalState(action, "success")
		meta := withCorrelationIDFromContext(ctx, metadata)
		if err := h.EventEmitter.EmitEvent(ctx, eventType, success, meta); err != nil && h.Log != nil {
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
func (h *Handler) Error(ctx context.Context, action string, msg string, cause error, metadata map[string]any, entityID string) {
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

	if h.EventEnabled && h.EventEmitter != nil {
		eventType := ensureTerminalState(action, "failed")
		meta := withCorrelationIDFromContext(ctx, metadata)
		if err := h.EventEmitter.EmitEvent(ctx, eventType, errContext, meta); err != nil && h.Log != nil {
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

func withCorrelationIDFromContext(ctx context.Context, metadata map[string]any) map[string]any {
	return metautil.PrepareForEmit(ctx, metadata)
}

// PublishEventArgs captures event publication job arguments for River.
type PublishEventArgs struct {
	EventType     string         `json:"event_type"`
	Payload       any            `json:"payload"`
	Metadata      map[string]any `json:"metadata"`
	SchemaVersion string         `json:"schema_version,omitempty"`
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

func (e *InMemoryEventEmitter) EmitEvent(ctx context.Context, eventType string, payload any, metadata map[string]any) error {
	if e == nil || e.Bus == nil {
		return nil
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}

	meta := withCorrelationIDFromContext(ctx, metadata)
	envelope := eventcontract.Envelope{
		EventType:     eventType,
		Payload:       asMap(payload),
		Metadata:      meta,
		CorrelationID: pickCorrelation(meta),
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	}
	envelope.Normalize()
	return e.Bus.Publish(ctx, envelope)
}

func (e *InMemoryEventEmitter) EmitEventTx(ctx context.Context, _ pgx.Tx, eventType string, payload any, metadata map[string]any) error {
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

func (s *InMemoryScheduler) Schedule(_ context.Context, job river.JobArgs, runAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, ScheduledJob{Job: job, RunAt: runAt.UTC()})
	return nil
}

func (s *InMemoryScheduler) ScheduleTx(ctx context.Context, _ pgx.Tx, job river.JobArgs, runAt time.Time) error {
	return s.Schedule(ctx, job, runAt)
}

func (s *InMemoryScheduler) ScheduleTxWithOpts(_ context.Context, _ pgx.Tx, job river.JobArgs, runAt time.Time, opts *river.InsertOpts) error {
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

func (e *RedisEventEmitter) EmitEvent(ctx context.Context, eventType string, payload any, metadata map[string]any) error {
	if e == nil || e.Bus == nil {
		return nil
	}
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}

	meta := withCorrelationIDFromContext(ctx, metadata)
	envelope := eventcontract.Envelope{
		EventType:     eventType,
		Payload:       asMap(payload),
		Metadata:      meta,
		CorrelationID: pickCorrelation(meta),
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	}
	envelope.Normalize()
	return e.Bus.Publish(ctx, envelope)
}

func (e *RedisEventEmitter) EmitEventTx(ctx context.Context, _ pgx.Tx, eventType string, payload any, metadata map[string]any) error {
	return e.EmitEvent(ctx, eventType, payload, metadata)
}

// RiverEventEmitter implements EventEmitter using River for Transactional Outbox.
type RiverEventEmitter struct {
	riverClient *river.Client[pgx.Tx]
}

func NewRiverEventEmitter(client *river.Client[pgx.Tx]) *RiverEventEmitter {
	return &RiverEventEmitter{riverClient: client}
}

func (e *RiverEventEmitter) EmitEvent(ctx context.Context, eventType string, payload any, metadata map[string]any) error {
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	meta := withCorrelationIDFromContext(ctx, metadata)
	_, err := e.riverClient.Insert(ctx, PublishEventArgs{
		EventType:     eventType,
		Payload:       payload,
		Metadata:      meta,
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
	}, nil)
	return err
}

func (e *RiverEventEmitter) EmitEventTx(ctx context.Context, tx pgx.Tx, eventType string, payload any, metadata map[string]any) error {
	if err := eventcontract.ValidateEventType(eventType); err != nil {
		return err
	}
	meta := withCorrelationIDFromContext(ctx, metadata)
	_, err := e.riverClient.InsertTx(ctx, tx, PublishEventArgs{
		EventType:     eventType,
		Payload:       payload,
		Metadata:      meta,
		SchemaVersion: eventcontract.EnvelopeSchemaVersion,
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
	_, err := s.riverClient.Insert(ctx, job, &river.InsertOpts{ScheduledAt: runAt})
	return err
}

func (s *RiverScheduler) ScheduleTx(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time) error {
	_, err := s.riverClient.InsertTx(ctx, tx, job, &river.InsertOpts{ScheduledAt: runAt})
	return err
}

func (s *RiverScheduler) ScheduleTxWithOpts(ctx context.Context, tx pgx.Tx, job river.JobArgs, runAt time.Time, opts *river.InsertOpts) error {
	if opts == nil {
		opts = &river.InsertOpts{ScheduledAt: runAt}
	} else if opts.ScheduledAt.IsZero() {
		opts.ScheduledAt = runAt
	}
	_, err := s.riverClient.InsertTx(ctx, tx, job, opts)
	return err
}

func asMap(payload any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if value, ok := payload.(map[string]any); ok {
		return value
	}
	return map[string]any{"result": payload}
}

func pickCorrelation(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if corr, ok := metadata["correlation_id"].(string); ok {
		return corr
	}
	return ""
}
