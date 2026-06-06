package graceful

import (
	"context"
	"errors"
	"testing"
	"time"

	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/riverqueue/river"
)

type testCache struct {
	key   string
	value any
	ttl   time.Duration
	err   error
}

func (c *testCache) Set(_ context.Context, key string, value any, ttl time.Duration) error {
	c.key = key
	c.value = value
	c.ttl = ttl
	return c.err
}

func TestHandlerSuccessEmitsEvent(t *testing.T) {
	bus := eventcontract.NewInMemoryBus(20)
	emitter := NewInMemoryEventEmitter(bus)
	handler := NewHandler(
		WithEventEmitter(emitter),
		WithService("operations"),
		WithEventEnabled(true),
	)

	ctx := metautil.IntoContext(context.Background(), metautil.FromMap(map[string]any{
		"correlation_id": "corr_123",
	}))
	handler.Success(ctx, "operations:create_work_order:v1:requested", "done", extension.Object{"id": extension.String("wo_1")}, nil, "wo_1", nil)

	recent := bus.Recent(10)
	if len(recent) == 0 {
		t.Fatalf("expected emitted events")
	}
	last := recent[len(recent)-1]
	if last.EventType != "operations:create_work_order:v1:success" {
		t.Fatalf("unexpected event_type: %s", last.EventType)
	}
	if last.CorrelationID != "corr_123" {
		t.Fatalf("expected correlation_id propagation")
	}
}

func TestHandlerOptionsCacheAndDisabledEvents(t *testing.T) {
	cache := &testCache{}
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	handler := NewHandler(
		WithLogger(log),
		WithCache(cache),
		WithScheduler(NewInMemoryScheduler()),
		WithVersion("v2"),
		WithEventEnabled(false),
	)
	if handler.Service != "server_kit" || handler.Version != "v2" || handler.Scheduler == nil {
		t.Fatalf("handler options not applied: %+v", handler)
	}
	success := handler.Success(context.Background(), "orders:create:v1:requested", "ok", "result", nil, "order_1", &CacheInfo{Key: "orders:1"})
	if success.Code != "ok" || success.Result != "result" {
		t.Fatalf("unexpected success context: %+v", success)
	}
	if cache.key != "orders:1" || cache.ttl != 5*time.Minute {
		t.Fatalf("cache write mismatch: %+v", cache)
	}

	cache.err = errors.New("cache down")
	success = handler.Success(context.Background(), "orders:create:v1:requested", "ok", "result", nil, "order_1", &CacheInfo{Key: "orders:2", TTL: time.Second})
	if success.Code != "ok" || cache.key != "orders:2" || cache.ttl != time.Second {
		t.Fatalf("cache error path mismatch: success=%+v cache=%+v", success, cache)
	}
}

func TestHandlerErrorEmitsFailure(t *testing.T) {
	bus := eventcontract.NewInMemoryBus(20)
	handler := NewHandler(
		WithEventEmitter(NewInMemoryEventEmitter(bus)),
		WithService("sensor"),
		WithEventEnabled(true),
	)

	handler.Error(
		context.Background(),
		"sensor:verify_proof:v1:requested",
		"verification failed",
		errors.New("signature mismatch"),
		extension.Object{"correlation_id": extension.String("corr_fail")},
		"proof_1",
	)

	recent := bus.Recent(10)
	if len(recent) == 0 {
		t.Fatalf("expected emitted failure event")
	}
	last := recent[len(recent)-1]
	if last.EventType != "sensor:verify_proof:v1:failed" {
		t.Fatalf("unexpected event_type: %s", last.EventType)
	}
}

func TestHandlerErrorHandlesNilCauseAndDisabledEvents(t *testing.T) {
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	handler := NewHandler(WithLogger(log), WithEventEnabled(false))
	handler.Error(context.Background(), "orders:create:v1:requested", "failed", nil, nil, "order_1")
	if errorString(nil) != "" || errorString(errors.New("x")) != "x" {
		t.Fatalf("errorString mismatch")
	}
	if ensureTerminalState("orders:create:v1:requested", "success") != "orders:create:v1:success" {
		t.Fatalf("ensureTerminalState mismatch")
	}
}

func TestEventEmittersNilInvalidAndRedisPaths(t *testing.T) {
	if err := (*InMemoryEventEmitter)(nil).EmitEvent(context.Background(), "orders:create:v1:success", nil, nil); err != nil {
		t.Fatalf("nil in-memory emitter should no-op: %v", err)
	}
	if err := NewInMemoryEventEmitter(nil).EmitEventTx(context.Background(), nil, "orders:create:v1:success", nil, nil); err != nil {
		t.Fatalf("nil in-memory bus should no-op: %v", err)
	}
	inMemoryBus := eventcontract.NewInMemoryBus(10)
	inMemoryEmitter := NewInMemoryEventEmitter(inMemoryBus)
	if err := inMemoryEmitter.EmitEvent(context.Background(), "bad event", nil, nil); err == nil {
		t.Fatalf("expected invalid in-memory event error")
	}
	if err := inMemoryEmitter.EmitEventTx(context.Background(), nil, "orders:create:v1:success", extension.Object{"id": extension.String("o1")}, extension.Object{"correlation_id": extension.String("corr_memory")}); err != nil {
		t.Fatalf("in-memory EmitEventTx() error = %v", err)
	}
	if recent := inMemoryBus.Recent(1); len(recent) != 1 || recent[0].CorrelationID != "corr_memory" {
		t.Fatalf("unexpected in-memory event: %+v", recent)
	}
	bus := eventcontract.NewInMemoryBus(10)
	redisEmitter := NewRedisEventEmitter(bus)
	if err := NewRedisEventEmitter(nil).EmitEventTx(context.Background(), nil, "orders:create:v1:success", nil, nil); err != nil {
		t.Fatalf("nil redis bus should no-op: %v", err)
	}
	if err := redisEmitter.EmitEvent(context.Background(), "bad event", nil, nil); err == nil {
		t.Fatalf("expected invalid event error")
	}
	ctx := metautil.IntoContext(context.Background(), metautil.FromMap(map[string]any{"correlation_id": "corr_redis"}))
	if err := redisEmitter.EmitEventTx(ctx, nil, "orders:create:v1:success", "ok", nil); err != nil {
		t.Fatalf("EmitEventTx() error = %v", err)
	}
	recent := bus.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("unexpected redis emitter event: %+v", recent)
	}
	result, _ := recent[0].Payload.GetString("result")
	if result != "ok" || recent[0].CorrelationID != "corr_redis" {
		t.Fatalf("unexpected redis emitter event: %+v", recent)
	}
	if err := (*RedisEventEmitter)(nil).EmitEvent(context.Background(), "orders:create:v1:success", nil, nil); err != nil {
		t.Fatalf("nil redis emitter should no-op: %v", err)
	}
}

func TestInMemorySchedulerStoresJobs(t *testing.T) {
	scheduler := NewInMemoryScheduler()
	err := scheduler.Schedule(context.Background(), PublishEventArgs{EventType: "governance:record_audit:v1:requested"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("schedule failed: %v", err)
	}

	err = scheduler.ScheduleTxWithOpts(
		context.Background(),
		nil,
		PublishEventArgs{EventType: "governance:record_audit:v1:success"},
		time.Now().UTC(),
		&river.InsertOpts{Queue: "governance_audit", MaxAttempts: 5},
	)
	if err != nil {
		t.Fatalf("schedule tx with opts failed: %v", err)
	}

	if len(scheduler.Jobs()) != 2 {
		t.Fatalf("expected 2 scheduled jobs")
	}
}

func TestPublishEventArgsAndHelpers(t *testing.T) {
	if got := (PublishEventArgs{}).Kind(); got != "publish_event" {
		t.Fatalf("Kind() = %q", got)
	}
	if got := objectFromPayload(nil); len(got) != 0 {
		t.Fatalf("objectFromPayload(nil) = %+v", got)
	}
	payload := extension.Object{"ok": extension.Bool(true)}
	if got := objectFromPayload(payload); got["ok"].Interface() != true {
		t.Fatalf("objectFromPayload(object) = %+v", got)
	}
	if got := objectFromPayload("value"); got["result"].Interface() != "value" {
		t.Fatalf("objectFromPayload(value) = %+v", got)
	}
	if got := pickCorrelation(extension.Object{"correlation_id": extension.String("corr")}); got != "corr" {
		t.Fatalf("pickCorrelation = %q", got)
	}
	if got := pickCorrelation(nil); got != "" {
		t.Fatalf("pickCorrelation(nil) = %q", got)
	}

	scheduler := NewInMemoryScheduler()
	runAt := time.Now().Add(time.Minute)
	if err := scheduler.ScheduleTx(context.Background(), nil, PublishEventArgs{}, runAt); err != nil {
		t.Fatalf("ScheduleTx() error = %v", err)
	}
	if got := scheduler.Jobs()[0].RunAt; !got.Equal(runAt.UTC()) {
		t.Fatalf("RunAt = %v, want %v", got, runAt.UTC())
	}
	if err := scheduler.ScheduleTxWithOpts(context.Background(), nil, PublishEventArgs{}, runAt, &river.InsertOpts{}); err != nil {
		t.Fatalf("ScheduleTxWithOpts empty opts error = %v", err)
	}
	if err := scheduler.ScheduleTxWithOpts(context.Background(), nil, PublishEventArgs{}, runAt, nil); err != nil {
		t.Fatalf("ScheduleTxWithOpts nil opts error = %v", err)
	}
}

func TestRiverConstructorsAndValidationBranches(t *testing.T) {
	emitter := NewRiverEventEmitter(nil)
	if emitter == nil {
		t.Fatalf("expected river emitter")
	}
	if err := emitter.EmitEvent(context.Background(), "bad event", nil, nil); err == nil {
		t.Fatalf("expected invalid river event error before client use")
	}
	if err := emitter.EmitEventTx(context.Background(), nil, "bad event", nil, nil); err == nil {
		t.Fatalf("expected invalid river tx event error before client use")
	}
	scheduler := NewRiverScheduler(nil)
	if scheduler == nil {
		t.Fatalf("expected river scheduler")
	}
}
