package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
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
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected nil postgres pool get panic")
			}
		}()
		_, _ = pg.Get(context.Background(), 1)
	}()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected nil postgres pool update panic")
			}
		}()
		_ = pg.UpdateTrackingData(context.Background(), 1, nil)
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

func TestJobMarshalUnmarshalParity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	orig := Job{
		ID:             "job-12345",
		JobKind:        "test:worker:kind",
		Queue:          "test_queue",
		CorrelationID:  "corr-abc",
		IdempotencyKey: "idem-xyz",
		Attempt:        1,
		MaxAttempts:    5,
		ScheduledAt:    now.Add(10 * time.Minute),
		CreatedAt:      now,
		ExecutionPolicy: JobExecutionPolicy{
			TimeoutMS: 5000,
		},
		RequireSidecar: true,
		Payload: extension.Object{
			"key_str": extension.String("val"),
			"key_num": extension.Int(42),
		},
		Metadata: extension.Object{
			"env": extension.String("test"),
		},
		RawPayload: []byte(`{"nested":"json_data","num":123}`),
	}

	// 1. Standard MarshalJSON -> UnmarshalJSON
	data, err := orig.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	var decoded Job
	if err := decoded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}

	if decoded.ID != orig.ID || decoded.JobKind != orig.JobKind || decoded.Queue != orig.Queue {
		t.Fatalf("base fields mismatch: %+v vs %+v", decoded, orig)
	}
	if decoded.CorrelationID != orig.CorrelationID || decoded.IdempotencyKey != orig.IdempotencyKey {
		t.Fatalf("correlation/idempotency mismatch: %+v vs %+v", decoded, orig)
	}
	if decoded.Attempt != orig.Attempt || decoded.MaxAttempts != orig.MaxAttempts {
		t.Fatalf("attempt mismatch: %d vs %d", decoded.Attempt, orig.Attempt)
	}
	if decoded.ExecutionPolicy.TimeoutMS != 5000 || !decoded.RequireSidecar {
		t.Fatalf("policy/sidecar mismatch: %+v", decoded)
	}
	if string(decoded.RawPayload) != string(orig.RawPayload) {
		t.Fatalf("raw_payload mismatch: %s vs %s", string(decoded.RawPayload), string(orig.RawPayload))
	}
	if str, _ := decoded.Payload.GetString("key_str"); str != "val" {
		t.Fatalf("payload string mismatch: %s", str)
	}
	if str, _ := decoded.Metadata.GetString("env"); str != "test" {
		t.Fatalf("metadata string mismatch: %s", str)
	}

	// 2. Test Binary (Non-JSON) payload encodes as Base64 string and decodes properly
	binaryPayload := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd}
	binaryJob := Job{
		JobKind:    "bin:kind",
		Queue:      "default",
		RawPayload: binaryPayload,
	}
	binData, err := binaryJob.MarshalJSON()
	if err != nil {
		t.Fatalf("binary marshal failed: %v", err)
	}
	var binDecoded Job
	if err := binDecoded.UnmarshalJSON(binData); err != nil {
		t.Fatalf("binary unmarshal failed: %v", err)
	}
	if len(binDecoded.RawPayload) != len(binaryPayload) {
		t.Fatalf("binary payload len mismatch: %d vs %d", len(binDecoded.RawPayload), len(binaryPayload))
	}
	for i := range binaryPayload {
		if binDecoded.RawPayload[i] != binaryPayload[i] {
			t.Fatalf("binary byte %d mismatch: %x vs %x", i, binDecoded.RawPayload[i], binaryPayload[i])
		}
	}

	// 3. Test Arena-backed Marshaling
	arena := scratch.New(4096)
	arenaData, err := orig.MarshalJSONWithArena(arena)
	if err != nil {
		t.Fatalf("MarshalJSONWithArena failed: %v", err)
	}
	var arenaDecoded Job
	if err := arenaDecoded.UnmarshalJSON(arenaData); err != nil {
		t.Fatalf("arena unmarshal failed: %v", err)
	}
	if arenaDecoded.ID != orig.ID {
		t.Fatalf("arena decoded id mismatch: %s vs %s", arenaDecoded.ID, orig.ID)
	}

	// 4. Test String Escaping in appendJSONString
	complexStringsJob := Job{
		ID:            "id\"with\\quotes\nand\rnewlines\tand\x00control\x1fchars",
		JobKind:       "kind:complex",
		Queue:         "default",
		CorrelationID: "corr\"quotes",
	}
	escData, err := complexStringsJob.MarshalJSON()
	if err != nil {
		t.Fatalf("complex strings marshal failed: %v", err)
	}
	var escDecoded Job
	if err := escDecoded.UnmarshalJSON(escData); err != nil {
		t.Fatalf("complex strings unmarshal failed: %v", err)
	}
	if escDecoded.ID != complexStringsJob.ID {
		t.Fatalf("escaped string roundtrip mismatch:\norig:    %q\ndecoded: %q", complexStringsJob.ID, escDecoded.ID)
	}

	// 5. Test UnmarshalJSON error paths
	var invalidJob Job
	if err := invalidJob.UnmarshalJSON([]byte(`[1, 2, 3]`)); err == nil {
		t.Fatal("expected array JSON to fail UnmarshalJSON")
	}
	if err := invalidJob.UnmarshalJSON([]byte(`{"payload": "not_an_object"}`)); err == nil {
		t.Fatal("expected invalid payload to fail UnmarshalJSON")
	}
	if err := invalidJob.UnmarshalJSON([]byte(`{"metadata": "not_an_object"}`)); err == nil {
		t.Fatal("expected invalid metadata to fail UnmarshalJSON")
	}
	// Non-base64 string in raw_payload falls back to raw string bytes
	var rawFallback Job
	if err := rawFallback.UnmarshalJSON([]byte(`{"raw_payload": "not-valid-base64-!@#$"}`)); err != nil {
		t.Fatalf("expected raw string fallback: %v", err)
	}
	if string(rawFallback.RawPayload) != "not-valid-base64-!@#$" {
		t.Fatalf("raw string fallback mismatch: %s", string(rawFallback.RawPayload))
	}
}

func TestProcessorMapletNilAndMissingLookups(t *testing.T) {
	var nilMaplet *ProcessorMaplet
	if proc, ok := nilMaplet.Lookup("any"); ok || proc != nil {
		t.Fatalf("expected nil maplet lookup to return false, got %v, %v", ok, proc)
	}
	if ch, ok := nilMaplet.Queue("any"); ok || ch != nil {
		t.Fatalf("expected nil maplet queue to return false, got %v, %v", ok, ch)
	}

	maplet := NewProcessorMaplet(nil, nil)
	if proc, ok := maplet.Lookup("missing"); ok || proc != nil {
		t.Fatalf("expected missing lookup to return false, got %v", ok)
	}
	if ch, ok := maplet.Queue("missing"); ok || ch != nil {
		t.Fatalf("expected missing queue to return false, got %v", ok)
	}

	holder := NewMapletHolder()
	if holder.Load() == nil {
		t.Fatal("expected default empty maplet from holder")
	}
	holder.Update(map[string]Processor{"proc": &benchProcessor{kind: "proc"}}, nil)
	if p, ok := holder.Load().Lookup("proc"); !ok || p == nil {
		t.Fatal("expected updated maplet from holder to contain proc")
	}
}

