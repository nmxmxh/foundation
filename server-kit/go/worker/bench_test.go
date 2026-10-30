package worker

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	kitlogger "github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

type benchProcessor struct {
	kind  string
	queue string
	wg    *sync.WaitGroup
}

func (p *benchProcessor) Kind() string     { return p.kind }
func (p *benchProcessor) Queue() string    { return p.queue }
func (p *benchProcessor) MaxAttempts() int { return 1 }
func (p *benchProcessor) Handle(_ context.Context, _ Job) error {
	if p.wg != nil {
		p.wg.Done()
	}
	return nil
}

func BenchmarkEngine_Enqueue_InMemory(b *testing.B) {
	engine := NewEngine(map[string]int{"bench": 1}, benchmarkLogger(b))
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	ctx := b.Context()
	_ = engine.Start(ctx) // Start workers to drain the queue

	job := Job{
		JobKind: "bench_kind",
		Queue:   "bench",
		Payload: extension.Object{"key": extension.String("value"), "id": extension.Int(123)},
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = engine.Enqueue(ctx, job)
	}
}

func BenchmarkEngine_Enqueue_RawPayload(b *testing.B) {
	engine := NewEngine(map[string]int{"bench": 1}, benchmarkLogger(b))
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	ctx := b.Context()
	_ = engine.Start(ctx)

	// Pre-serialized payload (e.g. Cap'n Proto or Protobuf)
	raw := []byte(`{"id":123,"data":"some-binary-data-here-that-is-large-enough-to-matter"}`)

	job := Job{
		JobKind:    "bench_kind",
		Queue:      "bench",
		RawPayload: raw,
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = engine.Enqueue(ctx, job)
	}
}

func BenchmarkEngine_Processing_Throughput(b *testing.B) {
	// We want to measure how many jobs per second the engine can drain
	var wg sync.WaitGroup
	engine := NewEngine(map[string]int{"bench": 64}, benchmarkLogger(b)) // Large pool for throughput
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench", wg: &wg})

	ctx := b.Context()
	_ = engine.Start(ctx)

	job := Job{
		JobKind: "bench_kind",
		Queue:   "bench",
	}

	b.ResetTimer()
	b.ReportAllocs()
	wg.Add(b.N)
	for i := 0; i < b.N; i++ {
		if err := engine.Enqueue(ctx, job); err != nil {
			wg.Done()
		}
	}

	// Drain everything
	wg.Wait()
}

func BenchmarkEngine_Enqueue_River(b *testing.B) {
	// This benchmark requires a real Postgres instance.
	// In CI, use testcontainers-go to spin up a container.
	b.Skip("Skipping River benchmark - requires Postgres container")

	/*
		pool := setupTestPostgres(b)
		riverClient := setupRiverClient(b, pool)

		engine := NewEngine(map[string]int{"bench": 1}, nil)
		engine.SetRiverClient(riverClient, pool)

		job := Job{
			JobKind: "bench_kind",
			Queue:   "bench",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = engine.Enqueue(context.Background(), job)
		}
	*/
}

func BenchmarkJob_Normalize(b *testing.B) {
	job := &Job{
		JobKind:  "test",
		Metadata: extension.Object{"timeout_ms": extension.Int(1000)},
	}

	b.ReportAllocs()
	for b.Loop() {
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
	for b.Loop() {
		_ = job.HealthKey()
	}
}

func BenchmarkJob_ArgsSerialization_JSONMap(b *testing.B) {
	job := Job{
		ID:            "job-bench-1",
		JobKind:       "order:process",
		Queue:         "orders",
		CorrelationID: "corr-12345",
		Payload: extension.Object{
			"order_id": extension.String("ord-998877"),
			"amount":   extension.Int(4500),
			"currency": extension.String("USD"),
			"status":   extension.String("pending"),
		},
		Metadata: extension.Object{
			"organization_id": extension.String("org-acme"),
			"actor_id":        extension.String("user-42"),
		},
	}

	b.ReportAllocs()
	for b.Loop() {
		bytes, err := json.Marshal(job)
		if err != nil {
			b.Fatal(err)
		}
		if len(bytes) == 0 {
			b.Fatal("empty bytes")
		}
	}
}

func BenchmarkJob_ArgsSerialization_RawPayload(b *testing.B) {
	raw := []byte(`{"order_id":"ord-998877","amount":4500,"currency":"USD","status":"pending","org":"org-acme"}`)
	job := Job{
		ID:            "job-bench-1",
		JobKind:       "order:process",
		Queue:         "orders",
		CorrelationID: "corr-12345",
		RawPayload:    raw,
		Metadata: extension.Object{
			"organization_id": extension.String("org-acme"),
		},
	}

	b.ReportAllocs()
	for b.Loop() {
		bytes, err := json.Marshal(job)
		if err != nil {
			b.Fatal(err)
		}
		if len(bytes) == 0 {
			b.Fatal("empty bytes")
		}
	}
}

func BenchmarkJob_ArgsDeserialization(b *testing.B) {
	job := Job{
		ID:            "job-bench-1",
		JobKind:       "order:process",
		Queue:         "orders",
		CorrelationID: "corr-12345",
		RawPayload:    []byte(`{"order_id":"ord-998877","amount":4500,"currency":"USD"}`),
		Metadata: extension.Object{
			"organization_id": extension.String("org-acme"),
		},
	}
	data, _ := json.Marshal(job)

	b.ReportAllocs()
	for b.Loop() {
		var decoded Job
		if err := json.Unmarshal(data, &decoded); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessor_Dispatch_Map(b *testing.B) {
	var mu sync.RWMutex
	processors := map[string]Processor{
		"order:process":   &benchProcessor{kind: "order:process", queue: "orders"},
		"email:send":      &benchProcessor{kind: "email:send", queue: "notifications"},
		"payment:capture": &benchProcessor{kind: "payment:capture", queue: "payments"},
		"report:generate": &benchProcessor{kind: "report:generate", queue: "analytics"},
	}

	kinds := []string{"order:process", "email:send", "payment:capture", "report:generate", "unknown:kind"}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		kind := kinds[i%len(kinds)]
		mu.RLock()
		proc := processors[kind]
		mu.RUnlock()
		_ = proc
	}
}

func BenchmarkEngine_EnqueueMany_InMemory64(b *testing.B) {
	engine := NewEngine(map[string]int{"bench": 1}, benchmarkLogger(b))
	_ = engine.Register(&benchProcessor{kind: "bench_kind", queue: "bench"})
	ctx := b.Context()
	_ = engine.Start(ctx)

	jobs := make([]Job, 64)
	for i := range jobs {
		jobs[i] = Job{
			JobKind: "bench_kind",
			Queue:   "bench",
			Payload: extension.Object{"id": extension.Int(int64(i))},
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = engine.EnqueueMany(ctx, jobs)
	}
	b.ReportMetric(64, "jobs/op")
}

func benchmarkLogger(b *testing.B) kitlogger.Logger {
	b.Helper()
	l, err := kitlogger.New(kitlogger.Config{
		Environment: "production",
		LogLevel:    "error",
		ServiceName: "worker-benchmark",
	})
	if err != nil {
		b.Fatalf("benchmark logger: %v", err)
	}
	return l
}
