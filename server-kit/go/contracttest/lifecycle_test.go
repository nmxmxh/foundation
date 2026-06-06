package contracttest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

func TestVerifyCommandLifecycle(t *testing.T) {
	obs := LifecycleObservation{
		Requested: lifecycleEnvelope("orders:create:v1:requested", "corr-1", "idem-1", "org-1"),
		Terminal:  lifecycleEnvelope("orders:create:v1:success", "corr-1", "idem-1", "org-1"),
		Jobs: []worker.Job{{
			ID:             "job-1",
			JobKind:        "orders.create",
			Queue:          "orders",
			CorrelationID:  "corr-1",
			IdempotencyKey: "idem-1",
			MaxAttempts:    2,
			Metadata: extension.Object{
				"correlation_id":  extension.String("corr-1"),
				"idempotency_key": extension.String("idem-1"),
				"organization_id": extension.String("org-1"),
			},
		}},
	}

	if err := VerifyCommandLifecycle(obs, LifecycleOptions{RequireIdempotency: true, RequireTenant: true}); err != nil {
		t.Fatalf("VerifyCommandLifecycle() error = %v", err)
	}
}

func TestLifecycleRecorderVerifiesObservedBusAndJobs(t *testing.T) {
	recorder := NewLifecycleRecorder()
	bus := recorder.WrapBus(events.NewInMemoryBus(10))
	recorder.RecordJob(worker.Job{
		ID:             "job-1",
		JobKind:        "orders.create",
		Queue:          "orders",
		CorrelationID:  "corr-1",
		IdempotencyKey: "idem-1",
		MaxAttempts:    2,
		Metadata: extension.Object{
			"correlation_id":  extension.String("corr-1"),
			"idempotency_key": extension.String("idem-1"),
			"organization_id": extension.String("org-1"),
		},
	})
	if err := bus.Publish(context.Background(), lifecycleEnvelope("orders:create:v1:requested", "corr-1", "idem-1", "org-1")); err != nil {
		t.Fatalf("Publish(requested) error = %v", err)
	}
	if err := bus.Publish(context.Background(), lifecycleEnvelope("orders:create:v1:success", "corr-1", "idem-1", "org-1")); err != nil {
		t.Fatalf("Publish(success) error = %v", err)
	}

	if err := recorder.Verify("orders:create:v1:requested", "orders:create:v1:success", LifecycleOptions{
		RequireIdempotency: true,
		RequireTenant:      true,
	}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	events := recorder.Events()
	jobs := recorder.Jobs()
	if len(events) != 2 || len(jobs) != 1 {
		t.Fatalf("unexpected recorder snapshot events=%d jobs=%d", len(events), len(jobs))
	}
	events[0].EventType = "mutated"
	jobs[0].JobKind = "mutated"
	if got := recorder.Events()[0].EventType; got != "orders:create:v1:requested" {
		t.Fatalf("Events should return copies, got %q", got)
	}
	if got := recorder.Jobs()[0].JobKind; got != "orders.create" {
		t.Fatalf("Jobs should return copies, got %q", got)
	}
}

func TestVerifyCommandLifecycleRejectsTerminalDrift(t *testing.T) {
	obs := LifecycleObservation{
		Requested: lifecycleEnvelope("orders:create:v1:requested", "corr-1", "idem-1", "org-1"),
		Terminal:  lifecycleEnvelope("orders:delete:v1:success", "corr-1", "idem-1", "org-1"),
	}

	err := VerifyCommandLifecycle(obs, LifecycleOptions{RequireIdempotency: true, RequireTenant: true})
	if err == nil || !strings.Contains(err.Error(), "does not refine requested") {
		t.Fatalf("expected terminal refinement error, got %v", err)
	}
}

func TestVerifyCommandLifecycleRejectsMetadataDrift(t *testing.T) {
	obs := LifecycleObservation{
		Requested: lifecycleEnvelope("orders:create:v1:requested", "corr-1", "idem-1", "org-1"),
		Terminal:  lifecycleEnvelope("orders:create:v1:success", "corr-2", "idem-1", "org-1"),
	}

	err := VerifyCommandLifecycle(obs, LifecycleOptions{RequireIdempotency: true, RequireTenant: true})
	if err == nil || !strings.Contains(err.Error(), "correlation_id") {
		t.Fatalf("expected correlation drift error, got %v", err)
	}
}

func TestVerifyCommandLifecycleRejectsJobTenantDrift(t *testing.T) {
	obs := LifecycleObservation{
		Requested: lifecycleEnvelope("orders:create:v1:requested", "corr-1", "idem-1", "org-1"),
		Terminal:  lifecycleEnvelope("orders:create:v1:failed", "corr-1", "idem-1", "org-1"),
		Jobs: []worker.Job{{
			ID:             "job-1",
			JobKind:        "orders.create",
			Queue:          "orders",
			CorrelationID:  "corr-1",
			IdempotencyKey: "idem-1",
			MaxAttempts:    2,
			Metadata:       extension.Object{"organization_id": extension.String("org-2")},
		}},
	}

	err := VerifyCommandLifecycle(obs, LifecycleOptions{RequireIdempotency: true, RequireTenant: true})
	if err == nil || !strings.Contains(err.Error(), "organization_id") {
		t.Fatalf("expected tenant drift error, got %v", err)
	}
}

func lifecycleEnvelope(eventType, correlationID, idempotencyKey, orgID string) events.Envelope {
	env := events.Envelope{
		EventType: eventType,
		Payload:   contractObject(map[string]any{"id": "order-1"}),
		Metadata: contractObject(map[string]any{
			"correlation_id":  correlationID,
			"idempotency_key": idempotencyKey,
			"organization_id": orgID,
		}),
		CorrelationID: correlationID,
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}
	env.Normalize()
	return env
}
