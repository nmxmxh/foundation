package worker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/tracing"
	"github.com/riverqueue/river"
)

type fakeProcessor struct {
	kind        string
	queue       string
	maxAttempts int
	failUntil   int32
	calls       atomic.Int32
}

func (p *fakeProcessor) Kind() string     { return p.kind }
func (p *fakeProcessor) Queue() string    { return p.queue }
func (p *fakeProcessor) MaxAttempts() int { return p.maxAttempts }
func (p *fakeProcessor) Handle(_ context.Context, _ Job) error {
	call := p.calls.Add(1)
	if call <= p.failUntil {
		return context.DeadlineExceeded
	}
	return nil
}

type correlationProcessor struct {
	fakeProcessor
	seen chan string
}

func (p *correlationProcessor) Handle(ctx context.Context, _ Job) error {
	p.seen <- tracing.CorrelationIDFromContext(ctx)
	return nil
}

func TestEngineRetriesAndSucceeds(t *testing.T) {
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	processor := &fakeProcessor{
		kind:        "operations_lifecycle",
		queue:       "operations_core",
		maxAttempts: 4,
		failUntil:   1,
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx := t.Context()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	err := engine.Enqueue(ctx, Job{
		JobKind:        "operations_lifecycle",
		Queue:          "operations_core",
		MaxAttempts:    4,
		IdempotencyKey: "idem_1",
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	waitForWorkerCondition(t, 2*time.Second, func() bool {
		return processor.calls.Load() >= 2
	}, "retry execution")
}

func TestEngineDedupesByIdempotencyKey(t *testing.T) {
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	processor := &fakeProcessor{
		kind:        "operations_lifecycle",
		queue:       "operations_core",
		maxAttempts: 2,
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx := t.Context()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	for range 2 {
		err := engine.Enqueue(ctx, Job{
			JobKind:        "operations_lifecycle",
			Queue:          "operations_core",
			MaxAttempts:    2,
			IdempotencyKey: "idem_same",
		})
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	waitForWorkerCondition(t, 2*time.Second, func() bool {
		snapshot := engine.HealthSnapshot()["idem:idem_same"]
		return snapshot.State == JobHealthDeduped && processor.calls.Load() == 1
	}, "dedupe terminal state")
}

func TestEngineInjectsJobCorrelationIntoProcessorContext(t *testing.T) {
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	processor := &correlationProcessor{
		fakeProcessor: fakeProcessor{
			kind:        "operations_lifecycle",
			queue:       "operations_core",
			maxAttempts: 1,
		},
		seen: make(chan string, 1),
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx := t.Context()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if err := engine.Enqueue(ctx, Job{
		JobKind:       "operations_lifecycle",
		Queue:         "operations_core",
		MaxAttempts:   1,
		CorrelationID: "corr_worker_context",
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	select {
	case got := <-processor.seen:
		if got != "corr_worker_context" {
			t.Fatalf("correlation ID = %q, want %q", got, "corr_worker_context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("processor did not receive job")
	}
}

func TestDetachedContextWithTimeoutKeepsLiveParentValues(t *testing.T) {
	type contextKey string
	parent := context.WithValue(context.Background(), contextKey("request_id"), "req_live")
	ctx, cancel := DetachedContextWithTimeout(parent, time.Second)
	defer cancel()

	if got := ctx.Value(contextKey("request_id")); got != "req_live" {
		t.Fatalf("request value = %v, want req_live", got)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context should be live: %v", err)
	}
}

func TestDetachedContextWithTimeoutSurvivesCancelledParent(t *testing.T) {
	type contextKey string
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), contextKey("request_id"), "req_cancelled"))
	cancelParent()

	ctx, cancel := DetachedContextWithTimeout(parent, time.Second)
	defer cancel()

	if got := ctx.Value(contextKey("request_id")); got != "req_cancelled" {
		t.Fatalf("request value = %v, want req_cancelled", got)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context should not inherit cancellation: %v", err)
	}
}

func TestEngineRecordsObservabilityStates(t *testing.T) {
	observability.Default().Reset()
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	processor := &fakeProcessor{
		kind:        "operations_lifecycle",
		queue:       "operations_core",
		maxAttempts: 2,
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx := t.Context()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if err := engine.Enqueue(ctx, Job{
		JobKind:        "operations_lifecycle",
		Queue:          "operations_core",
		MaxAttempts:    2,
		CorrelationID:  "corr_worker_obs",
		IdempotencyKey: "idem_obs_1",
		Metadata:       extension.Object{"organization_id": extension.String("org_obs")},
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := observability.Default().Snapshot()
		count := snapshot.Worker.Count
		if count["operations_lifecycle|operations_core|succeeded"] >= 1 {
			trace := observability.Default().Trace("corr_worker_obs", 0)
			if len(trace) == 0 {
				t.Fatalf("expected worker trace events")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshot := observability.Default().Snapshot()
	count := snapshot.Worker.Count
	t.Fatalf("expected succeeded worker metric, got keys: %s", fmt.Sprint(count))
}

func TestEngineRecordsNoProcessorTerminalState(t *testing.T) {
	observability.Default().Reset()
	engine := NewEngine(map[string]int{}, nil)
	err := engine.Enqueue(context.Background(), Job{
		JobKind:       "missing_processor",
		Queue:         "operations_core",
		MaxAttempts:   1,
		CorrelationID: "corr_missing_processor",
	})
	if err == nil {
		t.Fatal("expected missing processor error")
	}
	snapshot := engine.HealthSnapshot()["corr:corr_missing_processor"]
	if snapshot.State != JobHealthDroppedNoProcessor {
		t.Fatalf("state = %s, want %s", snapshot.State, JobHealthDroppedNoProcessor)
	}
}

func TestEngineRejectsFullInMemoryQueue(t *testing.T) {
	observability.Default().Reset()
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	processor := &fakeProcessor{
		kind:        "operations_lifecycle",
		queue:       "operations_core",
		maxAttempts: 1,
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	for i := range defaultQueueCapacity {
		err := engine.Enqueue(context.Background(), Job{
			JobKind:        "operations_lifecycle",
			Queue:          "operations_core",
			MaxAttempts:    1,
			IdempotencyKey: fmt.Sprintf("fill_%d", i),
		})
		if err != nil {
			t.Fatalf("fill enqueue %d failed: %v", i, err)
		}
	}

	err := engine.Enqueue(context.Background(), Job{
		JobKind:       "operations_lifecycle",
		Queue:         "operations_core",
		MaxAttempts:   1,
		CorrelationID: "corr_queue_full",
	})
	if !errors.Is(err, errQueueFull) {
		t.Fatalf("expected queue full error, got %v", err)
	}
	snapshot := engine.HealthSnapshot()["corr:corr_queue_full"]
	if snapshot.State != JobHealthRejectedQueueFull {
		t.Fatalf("state = %s, want %s", snapshot.State, JobHealthRejectedQueueFull)
	}
}

func TestEngineAcceptsAfterQueueFullBackpressureClears(t *testing.T) {
	observability.Default().Reset()
	engine := NewEngine(map[string]int{"operations_core": 8}, nil)
	processor := &fakeProcessor{
		kind:        "operations_lifecycle",
		queue:       "operations_core",
		maxAttempts: 1,
	}
	if err := engine.Register(processor); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	for i := range defaultQueueCapacity {
		err := engine.Enqueue(context.Background(), Job{
			JobKind:        "operations_lifecycle",
			Queue:          "operations_core",
			MaxAttempts:    1,
			IdempotencyKey: fmt.Sprintf("fill_%d", i),
		})
		if err != nil {
			t.Fatalf("fill enqueue %d failed: %v", i, err)
		}
	}

	rejected := Job{
		JobKind:       "operations_lifecycle",
		Queue:         "operations_core",
		MaxAttempts:   1,
		CorrelationID: "corr_queue_full_then_retry",
	}
	err := engine.Enqueue(context.Background(), rejected)
	if !errors.Is(err, errQueueFull) {
		t.Fatalf("expected queue full error, got %v", err)
	}
	snapshot := engine.HealthSnapshot()["corr:corr_queue_full_then_retry"]
	if snapshot.State != JobHealthRejectedQueueFull {
		t.Fatalf("state = %s, want %s", snapshot.State, JobHealthRejectedQueueFull)
	}

	ctx := t.Context()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err = engine.Enqueue(ctx, Job{
			JobKind:       "operations_lifecycle",
			Queue:         "operations_core",
			MaxAttempts:   1,
			CorrelationID: "corr_after_backpressure",
		})
		if err == nil {
			break
		}
		if !errors.Is(err, errQueueFull) {
			t.Fatalf("unexpected enqueue error after start: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("queue did not accept after workers started draining")
		}
		time.Sleep(time.Millisecond)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		snapshot = engine.HealthSnapshot()["corr:corr_after_backpressure"]
		if snapshot.State == JobHealthSucceeded {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not succeed after backpressure cleared; state=%s", snapshot.State)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEngineSettersPruneSpawnAndWait(t *testing.T) {
	engine := NewEngine(map[string]int{"operations_core": 1}, nil)
	store := NewInMemoryMetadataStore()
	engine.SetMetadataStore(store)
	if engine.metadataStore != store {
		t.Fatalf("metadata store was not set")
	}
	engine.SetRiverClient(nil, nil)
	if engine.riverClient != nil || engine.metadataStore != store {
		t.Fatalf("nil river client should not replace explicit metadata store")
	}

	expired := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	engine.dedupe["expired"] = expired
	engine.dedupe["future"] = future
	engine.pruneDedupe(time.Now().UTC())
	if _, ok := engine.dedupe["expired"]; ok {
		t.Fatalf("expired dedupe entry was not pruned")
	}
	if _, ok := engine.dedupe["future"]; !ok {
		t.Fatalf("future dedupe entry was pruned")
	}

	ctx, cancel := context.WithCancel(context.Background())
	jobs := make(chan Job)
	engine.spawnWorkers(ctx, "operations_core", jobs, 2)
	cancel()
	engine.Wait()
	if engine.workers["operations_core"] != 3 {
		t.Fatalf("workers = %d, want 3", engine.workers["operations_core"])
	}
}

func waitForWorkerCondition(t *testing.T, timeout time.Duration, condition func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
		<-ticker.C
	}
}

func TestBridgeWorkDelegatesToProcessor(t *testing.T) {
	processor := &fakeProcessor{kind: "operations_lifecycle", queue: "operations_core", maxAttempts: 1}
	bridge := &Bridge{Processor: processor}
	if err := bridge.Work(context.Background(), &river.Job[Job]{Args: Job{JobKind: "operations_lifecycle", Queue: "operations_core", MaxAttempts: 1}}); err != nil {
		t.Fatalf("Bridge Work() error = %v", err)
	}
	if processor.calls.Load() != 1 {
		t.Fatalf("processor calls = %d", processor.calls.Load())
	}
}

func TestBridgeWorkInjectsCorrelationIntoProcessorContext(t *testing.T) {
	processor := &correlationProcessor{
		fakeProcessor: fakeProcessor{
			kind:        "operations_lifecycle",
			queue:       "operations_core",
			maxAttempts: 1,
		},
		seen: make(chan string, 1),
	}
	bridge := &Bridge{Processor: processor}
	err := bridge.Work(context.Background(), &river.Job[Job]{Args: Job{
		JobKind:       "operations_lifecycle",
		Queue:         "operations_core",
		MaxAttempts:   1,
		CorrelationID: "corr_bridge_context",
	}})
	if err != nil {
		t.Fatalf("Bridge Work() error = %v", err)
	}

	select {
	case got := <-processor.seen:
		if got != "corr_bridge_context" {
			t.Fatalf("correlation ID = %q, want %q", got, "corr_bridge_context")
		}
	case <-time.After(time.Second):
		t.Fatal("processor did not receive bridge job")
	}
}
