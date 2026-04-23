package graceful

import (
	"context"
	"errors"
	"testing"
	"time"

	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/riverqueue/river"
)

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
	handler.Success(ctx, "operations:create_work_order:v1:requested", "done", map[string]any{"id": "wo_1"}, nil, "wo_1", nil)

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
		map[string]any{"correlation_id": "corr_fail"},
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
