package worker

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	time.Sleep(1200 * time.Millisecond)
	if processor.calls.Load() < 2 {
		t.Fatalf("expected retry execution")
	}
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	for i := 0; i < 2; i++ {
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

	time.Sleep(500 * time.Millisecond)
	if processor.calls.Load() != 1 {
		t.Fatalf("expected one call due to dedupe, got %d", processor.calls.Load())
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if err := engine.Enqueue(ctx, Job{
		JobKind:        "operations_lifecycle",
		Queue:          "operations_core",
		MaxAttempts:    2,
		IdempotencyKey: "idem_obs_1",
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := observability.Default().Snapshot()
		worker, _ := snapshot["worker"].(map[string]any)
		count, _ := worker["count"].(map[string]int64)
		if count["operations_lifecycle|operations_core|succeeded"] >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshot := observability.Default().Snapshot()
	worker, _ := snapshot["worker"].(map[string]any)
	count, _ := worker["count"].(map[string]int64)
	t.Fatalf("expected succeeded worker metric, got keys: %s", fmt.Sprint(count))
}
