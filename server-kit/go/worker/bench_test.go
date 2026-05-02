package worker

import (
	"context"
	"testing"
	"time"
)

type benchProcessor struct {
	kind  string
	queue string
}

func (p *benchProcessor) Kind() string     { return p.kind }
func (p *benchProcessor) Queue() string    { return p.queue }
func (p *benchProcessor) MaxAttempts() int { return 1 }
func (p *benchProcessor) Handle(_ context.Context, _ Job) error {
	return nil
}

func BenchmarkEngine_Enqueue_InMemory(b *testing.B) {
	engine := NewEngine(map[string]int{"bench": 1}, nil)
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = engine.Start(ctx) // Start workers to drain the queue

	job := Job{
		JobKind: "bench_kind",
		Queue:   "bench",
		Payload: map[string]any{"key": "value", "id": 123},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.Enqueue(ctx, job)
	}
}

func BenchmarkEngine_Enqueue_RawPayload(b *testing.B) {
	engine := NewEngine(map[string]int{"bench": 1}, nil)
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = engine.Start(ctx)

	// Pre-serialized payload (e.g. Cap'n Proto or Protobuf)
	raw := []byte(`{"id":123,"data":"some-binary-data-here-that-is-large-enough-to-matter"}`)

	job := Job{
		JobKind:    "bench_kind",
		Queue:      "bench",
		RawPayload: raw,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.Enqueue(ctx, job)
	}
}

func BenchmarkEngine_Processing_Throughput(b *testing.B) {
	// We want to measure how many jobs per second the engine can drain
	engine := NewEngine(map[string]int{"bench": 8}, nil)
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = engine.Start(ctx)

	job := Job{
		JobKind: "bench_kind",
		Queue:   "bench",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.Enqueue(ctx, job)
	}
	// Note: this only benchmarks enqueue speed, drain speed is async.
}

func BenchmarkJob_Normalize(b *testing.B) {
	job := &Job{
		JobKind: "test",
		Metadata: map[string]any{"timeout_ms": 1000},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		job.Normalize()
	}
}

func BenchmarkJob_HealthKey(b *testing.B) {
	job := Job{
		ID:            "job-123",
		CorrelationID: "corr-456",
		JobKind:       "test",
		Queue:         "default",
		CreatedAt:     time.Now(),
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = job.HealthKey()
	}
}
