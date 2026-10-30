package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

type mockScratchProcessor struct {
	kind       string
	queue      string
	arenaCalls atomic.Int64
}

func (p *mockScratchProcessor) Kind() string     { return p.kind }
func (p *mockScratchProcessor) Queue() string    { return p.queue }
func (p *mockScratchProcessor) MaxAttempts() int { return 1 }
func (p *mockScratchProcessor) Handle(_ context.Context, _ Job) error {
	return nil
}
func (p *mockScratchProcessor) HandleWithArena(_ context.Context, arena *scratch.Arena, _ Job) error {
	p.arenaCalls.Add(1)
	if arena != nil {
		_, _ = arena.Alloc(128)
	}
	return nil
}

func TestProcessorMapletLockFreeLookup(t *testing.T) {
	procA := &mockScratchProcessor{kind: "job:alpha", queue: "q_alpha"}
	procB := &mockScratchProcessor{kind: "job:beta", queue: "q_beta"}

	processors := map[string]Processor{
		"job:alpha": procA,
		"job:beta":  procB,
	}
	queues := map[string]chan Job{
		"q_alpha": make(chan Job, 10),
		"q_beta":  make(chan Job, 10),
	}

	maplet := NewProcessorMaplet(processors, queues)

	// Lookup hits
	gotA, ok := maplet.Lookup("job:alpha")
	if !ok || gotA != procA {
		t.Fatalf("expected procA, got %v (ok=%v)", gotA, ok)
	}

	gotB, ok := maplet.Lookup("job:beta")
	if !ok || gotB != procB {
		t.Fatalf("expected procB, got %v (ok=%v)", gotB, ok)
	}

	// Lookup miss
	_, ok = maplet.Lookup("job:gamma")
	if ok {
		t.Fatalf("expected false for unknown kind")
	}

	// Queue lookup
	chA, ok := maplet.Queue("q_alpha")
	if !ok || chA == nil {
		t.Fatalf("expected q_alpha channel, got %v", chA)
	}
}

func TestEngineScratchProcessorExecution(t *testing.T) {
	ctx := context.Background()
	proc := &mockScratchProcessor{kind: "job:scratch", queue: "q_scratch"}
	engine := NewEngine(map[string]int{"q_scratch": 1}, nil)
	if err := engine.Register(proc); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	job := Job{
		JobKind: "job:scratch",
		Queue:   "q_scratch",
	}

	if err := engine.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// Wait briefly for drain
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if proc.arenaCalls.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if proc.arenaCalls.Load() == 0 {
		t.Fatalf("expected HandleWithArena to be invoked")
	}
}
