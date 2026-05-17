package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJobNextBackoffCapsLargeAttempts(t *testing.T) {
	job := Job{Attempt: 10_000}
	backoff := job.NextBackoff()
	if backoff < 0 {
		t.Fatalf("backoff must not be negative: %s", backoff)
	}
	if backoff > 45*time.Second {
		t.Fatalf("backoff exceeded jittered cap: %s", backoff)
	}
}

func TestJobNormalizeValidateTimeoutAndHealthKey(t *testing.T) {
	var nilJob *Job
	nilJob.Normalize()
	job := Job{JobKind: "kind", Metadata: map[string]any{"timeout_ms": float64(1500)}, Attempt: -1}
	job.Normalize()
	if job.Queue != "default" || job.MaxAttempts != 3 || job.Attempt != 0 || job.ExecutionPolicy.TimeoutMS != 1500 || job.CreatedAt.IsZero() {
		t.Fatalf("normalized job = %+v", job)
	}
	if job.Kind() != "kind" || job.Timeout() != 1500*time.Millisecond {
		t.Fatalf("kind/timeout mismatch: %s %s", job.Kind(), job.Timeout())
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, invalid := range []Job{
		{},
		{JobKind: "kind"},
		{JobKind: "kind", Queue: "q"},
		{JobKind: "kind", Queue: "q", MaxAttempts: 1, Attempt: 2},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("expected invalid job to fail: %+v", invalid)
		}
	}
	if (Job{ID: "id"}).HealthKey() != "id" || (Job{CorrelationID: "corr"}).HealthKey() != "corr:corr" || (Job{IdempotencyKey: "idem"}).HealthKey() != "idem:idem" {
		t.Fatal("HealthKey priority failed")
	}
	if positiveIntFromMetadata(map[string]any{"a": int32(3)}, "a") != 3 ||
		positiveIntFromMetadata(map[string]any{"a": int64(4)}, "a") != 4 ||
		positiveIntFromMetadata(map[string]any{"a": -1}, "a") != 0 {
		t.Fatal("positiveIntFromMetadata failed")
	}
}

func TestJobJSONOmitsZeroStructFields(t *testing.T) {
	jobJSON, err := json.Marshal(Job{})
	if err != nil {
		t.Fatalf("Marshal(Job{}) error = %v", err)
	}
	encodedJob := string(jobJSON)
	for _, field := range []string{`"scheduled_at"`, `"created_at"`, `"execution_policy"`} {
		if strings.Contains(encodedJob, field) {
			t.Fatalf("zero job field %s should be omitted: %s", field, jobJSON)
		}
	}

	healthJSON, err := json.Marshal(JobHealthSnapshot{})
	if err != nil {
		t.Fatalf("Marshal(JobHealthSnapshot{}) error = %v", err)
	}
	encodedHealth := string(healthJSON)
	for _, field := range []string{`"scheduled_at"`, `"started_at"`, `"finished_at"`, `"updated_at"`} {
		if strings.Contains(encodedHealth, field) {
			t.Fatalf("zero health field %s should be omitted: %s", field, healthJSON)
		}
	}
}

func TestInMemoryMetadataStore(t *testing.T) {
	store := NewInMemoryMetadataStore()
	if err := store.Save(context.Background(), JobMetadata{}); err == nil {
		t.Fatal("expected missing metadata fields to fail")
	}
	meta := JobMetadata{JobID: 1, WorkflowName: "wf", EntityType: "asset", EntityID: "a1", TrackingData: map[string]any{"state": "queued"}}
	if err := store.Save(context.Background(), meta); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Get(context.Background(), 1)
	if err != nil || got.WorkflowName != "wf" {
		t.Fatalf("Get() = %+v err=%v", got, err)
	}
	if err := store.UpdateTrackingData(context.Background(), 1, map[string]any{"state": "done"}); err != nil {
		t.Fatalf("UpdateTrackingData() error = %v", err)
	}
	got, _ = store.Get(context.Background(), 1)
	if got.TrackingData["state"] != "done" {
		t.Fatalf("tracking data = %+v", got.TrackingData)
	}
	if _, err := store.Get(context.Background(), 2); err == nil {
		t.Fatal("expected missing metadata get to fail")
	}
	if err := store.UpdateTrackingData(context.Background(), 2, nil); err == nil {
		t.Fatal("expected missing metadata update to fail")
	}
	encoded, err := got.ToJSON()
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("ToJSON() = %s err=%v", string(encoded), err)
	}
	if NewPostgresMetadataStore(nil) == nil {
		t.Fatal("expected postgres store wrapper")
	}
	pg := NewPostgresMetadataStore(nil)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected nil postgres pool save panic")
			}
		}()
		_ = pg.Save(context.Background(), meta)
	}()
}

func TestTrendPredictorSuggestsWorkersOnlyForRisingDepth(t *testing.T) {
	predictor := NewTrendPredictor()
	for _, depth := range []int{10, 20} {
		if got := predictor.Predict(context.Background(), "operations_core", depth, 4); got != 0 {
			t.Fatalf("expected insufficient history to return 0, got %d", got)
		}
	}
	if got := predictor.Predict(context.Background(), "operations_core", 180, 4); got != 1 {
		t.Fatalf("expected rising queue to add one worker, got %d", got)
	}
	if got := predictor.Predict(context.Background(), "operations_core", 400, 64); got != 0 {
		t.Fatalf("expected max worker cap to suppress scaling, got %d", got)
	}
	for i := range 12 {
		predictor.Predict(context.Background(), "operations_core", i, 1)
	}
	if len(predictor.history["operations_core"]) != 10 {
		t.Fatalf("history length = %d, want 10", len(predictor.history["operations_core"]))
	}
}
