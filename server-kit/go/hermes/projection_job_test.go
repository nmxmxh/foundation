package hermes

import (
	"context"
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

func TestNewRecordProjectionJobContract(t *testing.T) {
	if _, err := NewRecordProjectionJob("", "dishes", "org_1", "dish_1", "v1"); err == nil {
		t.Fatal("empty domain must be rejected")
	}

	job, err := NewRecordProjectionJob("menu", "dishes", "org_1", "dish_1", "v7")
	if err != nil {
		t.Fatalf("NewRecordProjectionJob() error = %v", err)
	}
	if job.JobKind != RecordProjectionJobKind || job.Queue != RecordProjectionQueue {
		t.Fatalf("job kind/queue = %s/%s", job.JobKind, job.Queue)
	}
	if job.IdempotencyKey != "proj:menu:dishes:org_1:dish_1:v7" {
		t.Fatalf("IdempotencyKey = %q, want mutation tag included so successive updates never dedupe against each other", job.IdempotencyKey)
	}
	for key, want := range map[string]string{
		"operation": "upsert", "domain": "menu", "collection": "dishes",
		"organization_id": "org_1", "record_id": "dish_1",
	} {
		if got, _ := job.Payload.GetString(key); got != want {
			t.Fatalf("payload[%s] = %q, want %q", key, got, want)
		}
	}

	// Empty mutation tag: keys must still differ across enqueues (no dedup is
	// safer than a lost update).
	a, _ := NewRecordProjectionJob("menu", "dishes", "org_1", "dish_1", "")
	b, _ := NewRecordProjectionJob("menu", "dishes", "org_1", "dish_1", "")
	if a.IdempotencyKey == "" || a.IdempotencyKey == b.IdempotencyKey {
		// Equal keys are only possible if two calls land on the same
		// nanosecond timestamp; treat that as a failure signal for the fallback.
		t.Fatalf("empty-tag keys must be unique: %q vs %q", a.IdempotencyKey, b.IdempotencyKey)
	}

	del, err := NewRecordProjectionDeleteJob("menu", "dishes", "org_1", "dish_1", "v8")
	if err != nil {
		t.Fatalf("NewRecordProjectionDeleteJob() error = %v", err)
	}
	if op, _ := del.Payload.GetString("operation"); op != "delete" {
		t.Fatalf("delete job operation = %q", op)
	}
}

// TestRecordProjectionProcessorReadBackAndDelete pins the generic processor's
// full contract: read-back upsert lands in both the durable record store and
// the hot partition; a fetch miss converges by deleting; an explicit delete
// job removes the record; a malformed identity is dropped without error
// (deterministic poison must not burn retries to dead-letter).
func TestRecordProjectionProcessorReadBackAndDelete(t *testing.T) {
	base := database.NewMemoryDB()
	projected, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	// The app's normalized table, simulated: one row the fetcher can read back.
	rows := map[string]database.RecordData{
		"dish_1": testRecordData(map[string]any{"name": "Jollof", "price_minor": 4500}),
	}
	fetch := func(_ context.Context, _, _, _, recordID string) (database.RecordData, bool, error) {
		data, ok := rows[recordID]
		return data, ok, nil
	}
	processor, err := NewRecordProjectionProcessor(projected, fetch)
	if err != nil {
		t.Fatalf("NewRecordProjectionProcessor() error = %v", err)
	}
	if processor.Kind() != RecordProjectionJobKind || processor.Queue() != RecordProjectionQueue {
		t.Fatalf("processor identity = %s/%s", processor.Kind(), processor.Queue())
	}

	// Read-back upsert.
	job, _ := NewRecordProjectionJob("menu", "dishes", "org_1", "dish_1", "v1")
	if err := processor.Handle(ctx, job); err != nil {
		t.Fatalf("Handle(upsert) error = %v", err)
	}
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_1"); err != nil || !found {
		t.Fatalf("durable record: found=%v err=%v", found, err)
	}
	projection := projected.ProjectionName("menu", "dishes", "org_1")
	if count, _ := projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{}); count != 1 {
		t.Fatalf("hot count = %d, want 1", count)
	}

	// Fetch miss (row deleted between commit and projection) converges by delete.
	delete(rows, "dish_1")
	job, _ = NewRecordProjectionJob("menu", "dishes", "org_1", "dish_1", "v2")
	if err := processor.Handle(ctx, job); err != nil {
		t.Fatalf("Handle(miss) error = %v", err)
	}
	if count, _ := projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{}); count != 0 {
		t.Fatalf("hot count after miss-converge = %d, want 0", count)
	}

	// Inline payload path: no fetcher round-trip.
	job, _ = NewRecordProjectionJob("menu", "dishes", "org_1", "dish_2", "v1")
	job.RawPayload = []byte(`{"name":"Suya","price_minor":3000}`)
	if err := processor.Handle(ctx, job); err != nil {
		t.Fatalf("Handle(inline) error = %v", err)
	}
	rec, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_2")
	if err != nil || !found || !recordDataStringEquals(rec.Data, "name", "Suya") {
		t.Fatalf("inline record = %+v found=%v err=%v", rec, found, err)
	}

	// Explicit delete job.
	del, _ := NewRecordProjectionDeleteJob("menu", "dishes", "org_1", "dish_2", "v2")
	if err := processor.Handle(ctx, del); err != nil {
		t.Fatalf("Handle(delete) error = %v", err)
	}
	if count, _ := projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{}); count != 0 {
		t.Fatalf("hot count after delete = %d, want 0", count)
	}

	// Malformed identity: dropped, not retried.
	if err := processor.Handle(ctx, worker.Job{JobKind: RecordProjectionJobKind}); err != nil {
		t.Fatalf("Handle(malformed) error = %v, want nil (deterministic poison must not retry)", err)
	}

	// Fetcher system errors do retry.
	failing, _ := NewRecordProjectionProcessor(projected, func(context.Context, string, string, string, string) (database.RecordData, bool, error) {
		return nil, false, errors.New("db down")
	})
	job, _ = NewRecordProjectionJob("menu", "dishes", "org_1", "dish_3", "v1")
	if err := failing.Handle(ctx, job); err == nil {
		t.Fatal("fetcher system error must propagate for retry")
	}
}
