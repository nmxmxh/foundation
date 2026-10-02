package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
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
	job := Job{JobKind: "kind", Metadata: extension.Object{"timeout_ms": extension.Float(1500)}, Attempt: -1}
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
	if positiveIntFromMetadata(extension.Object{"a": extension.Int(3)}, "a") != 3 ||
		positiveIntFromMetadata(extension.Object{"a": extension.Int(4)}, "a") != 4 ||
		positiveIntFromMetadata(extension.Object{"a": extension.Int(-1)}, "a") != 0 {
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
	meta := JobMetadata{JobID: 1, WorkflowName: "wf", EntityType: "asset", EntityID: "a1", TrackingData: extension.Object{"state": extension.String("queued")}}
	if err := store.Save(context.Background(), meta); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Get(context.Background(), 1)
	if err != nil || got.WorkflowName != "wf" {
		t.Fatalf("Get() = %+v err=%v", got, err)
	}
	if err := store.UpdateTrackingData(context.Background(), 1, extension.Object{"state": extension.String("done")}); err != nil {
		t.Fatalf("UpdateTrackingData() error = %v", err)
	}
	got, _ = store.Get(context.Background(), 1)
	if state, _ := got.TrackingData.GetString("state"); state != "done" {
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

// Conformance test linking the WorkerRetryQueue TLA spec
// (docs/specs/tla/WorkerRetryQueue.tla) to the real worker implementation.
//
// The spec's RetryBudgetBounded invariant -- attempts[j] <= MaxAttempts -- is
// enforced in production code by Job.Validate (job.go: rejects
// "attempt N exceeds max_attempts M") and by the engine's exhaustion decision
// (engine.go: `if job.Attempt >= job.MaxAttempts`). TLC proves no reachable
// model state violates the invariant; this test proves the real code refuses to
// enter the violating state on real inputs.

// TestConformanceWorkerRetryBudgetBounded drives the real Job.Validate across
// the budget boundary. A job within budget must validate; a job past budget
// must be rejected -- that rejection is the concrete enforcement the TLA
// RetryBudgetBounded invariant abstracts.

func TestConformanceWorkerRetryBudgetBounded(t *testing.T) {
	for maxAttempts := 1; maxAttempts <= 5; maxAttempts++ {
		for attempt := 0; attempt <= maxAttempts+2; attempt++ {
			job := Job{
				JobKind:     "conformance",
				Queue:       "default",
				MaxAttempts: maxAttempts,
				Attempt:     attempt,
			}
			err := job.Validate()
			withinBudget := attempt <= maxAttempts
			switch {
			case withinBudget && err != nil:
				t.Fatalf("within-budget job (attempt=%d max=%d) rejected: %v",
					attempt, maxAttempts, err)
			case !withinBudget && err == nil:
				t.Fatalf("over-budget job (attempt=%d max=%d) accepted; "+
					"RetryBudgetBounded is not enforced by Job.Validate",
					attempt, maxAttempts)
			}
		}
	}
}

// Trace validation for the WorkerRetryQueue TLA spec
// (docs/specs/tla/WorkerRetryQueue.tla) against the real worker.
//
// Idea (option 3, "does a real execution refine the spec?"): drive the real
// retry lifecycle using the real Job type and the real predicates, record the
// trace of (action, state) steps, then confirm every recorded state and every
// transition is a behavior the TLA spec permits. TLC explores the model
// exhaustively; this checks that an actual run is one of the model's behaviors.

// wrqAction mirrors the WorkerRetryQueue spec's actions.

type wrqAction string

const (
	actRetry   wrqAction = "Retry"
	actExhaust wrqAction = "Exhaust"
)

type wrqStep struct {
	action  wrqAction
	attempt int
}

// TestConformanceTraceWorkerRetryQueue records a real retry lifecycle and
// validates it against the spec invariants and allowed transitions.

func TestConformanceTraceWorkerRetryQueue(t *testing.T) {
	const maxAttempts = 3

	job := Job{JobKind: "conformance", Queue: "default", MaxAttempts: maxAttempts, Attempt: 0}
	job.Normalize()

	var trace []wrqStep
	terminal := false

	// Mirror the engine's failure/retry loop (engine.go): each failed run does
	// `job.Attempt++`, and the job is exhausted when
	// `job.Attempt >= job.MaxAttempts`. RetryBudgetBounded is enforced at every
	// step by the real Job.Validate -- if the real code ever let a state exceed
	// the budget, Validate would return an error here and fail the test.
	for !terminal {
		job.Attempt++
		if err := job.Validate(); err != nil {
			t.Fatalf("recorded state violates RetryBudgetBounded (real Job.Validate): %v", err)
		}
		if job.Attempt >= job.MaxAttempts {
			trace = append(trace, wrqStep{actExhaust, job.Attempt})
			terminal = true
		} else {
			trace = append(trace, wrqStep{actRetry, job.Attempt})
		}
	}

	// Trace-level invariants -- the spec's THEOREMs, checked on the real run.

	// RetryBudgetBounded: no recorded state exceeds the attempt budget.
	for _, s := range trace {
		if s.attempt > maxAttempts {
			t.Fatalf("trace state attempt=%d exceeds MaxAttempts=%d (RetryBudgetBounded)",
				s.attempt, maxAttempts)
		}
	}

	// AtLeastTerminal + ExactlyOneTerminal: the run ends in Exhaust, and Exhaust
	// appears nowhere else.
	if len(trace) == 0 || trace[len(trace)-1].action != actExhaust {
		t.Fatalf("trace does not terminate in Exhaust: %+v", trace)
	}
	for _, s := range trace[:len(trace)-1] {
		if s.action == actExhaust {
			t.Fatalf("Exhaust observed before the terminal step: %+v", trace)
		}
	}

	// Every transition is an allowed spec action: attempts advance by exactly 1
	// (the Lease/Retry step), never jump or regress.
	for i := 1; i < len(trace); i++ {
		if trace[i].attempt != trace[i-1].attempt+1 {
			t.Fatalf("transition is not a valid Lease step (attempt jumped): %+v", trace)
		}
	}
}
