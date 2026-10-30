//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type riverTestProcessor struct {
	kind        string
	queue       string
	handled     atomic.Int64
	doneChan    chan struct{}
	targetCount int64
}

func (p *riverTestProcessor) Kind() string     { return p.kind }
func (p *riverTestProcessor) Queue() string    { return p.queue }
func (p *riverTestProcessor) MaxAttempts() int { return 3 }
func (p *riverTestProcessor) Handle(_ context.Context, _ worker.Job) error {
	n := p.handled.Add(1)
	if p.doneChan != nil && p.targetCount > 0 && n >= p.targetCount {
		select {
		case p.doneChan <- struct{}{}:
		default:
		}
	}
	return nil
}

type riverScratchTestProcessor struct {
	riverTestProcessor
	arenaAllocBytes atomic.Int64
}

func (p *riverScratchTestProcessor) HandleWithArena(_ context.Context, arena *scratch.Arena, _ worker.Job) error {
	n := p.handled.Add(1)
	if arena != nil {
		buf, _ := arena.Alloc(256)
		p.arenaAllocBytes.Add(int64(len(buf)))
	}
	if p.doneChan != nil && p.targetCount > 0 && n >= p.targetCount {
		select {
		case p.doneChan <- struct{}{}:
		default:
		}
	}
	return nil
}

func setupRiverBench(tb testing.TB, queueWorkers int, customProc worker.Processor) (serviceEnv, *pgxpool.Pool, *river.Client[pgx.Tx], *worker.Engine, *riverTestProcessor) {
	tb.Helper()
	env := requireServiceEnv(tb)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if queueWorkers <= 0 {
		queueWorkers = 16
	}

	pool, err := database.NewRiverPool(ctx, env.databaseURL, database.PoolOptions{
		MaxConns:       48,
		MinConns:       8,
		QueryTimeout:   10 * time.Second,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		tb.Fatalf("connect river postgres pool failed: %v", err)
	}

	// Apply migrations
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err == nil {
		_, _ = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	}

	// Ensure river_job_metadata sidecar table exists
	const metaSQL = `
	CREATE TABLE IF NOT EXISTS river_job_metadata (
		job_id BIGINT PRIMARY KEY REFERENCES river_job(id) ON DELETE CASCADE,
		workflow_name TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		user_id TEXT,
		correlation_id TEXT,
		raw_payload BYTEA,
		tracking_data JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);`
	if _, err := pool.Exec(ctx, metaSQL); err != nil {
		tb.Fatalf("create river_job_metadata failed: %v", err)
	}

	_ = database.ResyncRiverJobSequence(ctx, pool)

	var defaultProc *riverTestProcessor
	proc := customProc
	if proc == nil {
		defaultProc = &riverTestProcessor{
			kind:  "bench:river:work",
			queue: "bench_river",
		}
		proc = defaultProc
	} else if p, ok := customProc.(*riverTestProcessor); ok {
		defaultProc = p
	}

	engine := worker.NewEngine(map[string]int{"bench_river": queueWorkers}, nil)
	if err := engine.Register(proc); err != nil {
		tb.Fatalf("register processor failed: %v", err)
	}

	workers := river.NewWorkers()
	if err := engine.AddToWorkers(workers); err != nil {
		tb.Fatalf("add to workers failed: %v", err)
	}

	riverCfg := &river.Config{
		Queues: map[string]river.QueueConfig{
			"bench_river": {MaxWorkers: queueWorkers},
		},
		Workers: workers,
	}

	client, err := river.NewClient(riverpgxv5.New(pool), riverCfg)
	if err != nil {
		tb.Fatalf("create river client failed: %v", err)
	}

	engine.SetRiverClient(client, pool)

	tb.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanCancel()
		_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
		pool.Close()
	})

	return env, pool, client, engine, defaultProc
}

func BenchmarkServiceBackedRiver_EnqueueSingle(b *testing.B) {
	_, pool, _, engine, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	job := worker.Job{
		JobKind:       "bench:river:work",
		Queue:         "bench_river",
		CorrelationID: "corr-river-bench-single",
		Payload: extension.Object{
			"order_id": extension.String("ord-100"),
			"status":   extension.String("queued"),
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if err := engine.Enqueue(ctx, job); err != nil {
			b.Fatalf("enqueue failed: %v", err)
		}
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_EnqueueInlineRawPayload(b *testing.B) {
	_, pool, _, engine, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	raw := []byte(`{"order_id":"ord-100","details":"inline-payload-optimization","items":[1,2,3]}`)
	job := worker.Job{
		JobKind:        "bench:river:work",
		Queue:          "bench_river",
		CorrelationID:  "corr-river-bench-inline",
		RawPayload:     raw,
		RequireSidecar: false,
		Metadata: extension.Object{
			"organization_id": extension.String("org-fintech"),
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if err := engine.Enqueue(ctx, job); err != nil {
			b.Fatalf("enqueue inline failed: %v", err)
		}
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_EnqueueWithSidecar(b *testing.B) {
	_, pool, _, engine, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	raw := []byte(`{"order_id":"ord-100","details":"large-payload-testing-sidecar-table-overhead","items":[1,2,3]}`)
	job := worker.Job{
		JobKind:        "bench:river:work",
		Queue:          "bench_river",
		CorrelationID:  "corr-river-bench-sidecar",
		RawPayload:     raw,
		RequireSidecar: true, // Forces 2nd SQL insert into river_job_metadata
		Metadata: extension.Object{
			"organization_id": extension.String("org-fintech"),
			"workflow_name":   extension.String("bench:river:work"),
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if err := engine.Enqueue(ctx, job); err != nil {
			b.Fatalf("enqueue with sidecar failed: %v", err)
		}
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_EnqueueManyPipelined64(b *testing.B) {
	_, pool, _, engine, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	jobs := make([]worker.Job, 64)
	for i := range jobs {
		jobs[i] = worker.Job{
			JobKind:       "bench:river:work",
			Queue:         "bench_river",
			CorrelationID: fmt.Sprintf("corr-river-pipe-%d", i),
			Payload: extension.Object{
				"item_index": extension.Int(int64(i)),
			},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if err := engine.EnqueueMany(ctx, jobs); err != nil {
			b.Fatalf("enqueue many pipelined failed: %v", err)
		}
	}
	b.ReportMetric(64, "jobs/op")

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_InsertManyFastCopyFrom64(b *testing.B) {
	_, pool, client, _, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	params := make([]river.InsertManyParams, 64)
	for i := range params {
		params[i] = river.InsertManyParams{
			Args: worker.Job{
				JobKind:       "bench:river:work",
				Queue:         "bench_river",
				CorrelationID: fmt.Sprintf("corr-river-fast-%d", i),
				Payload: extension.Object{
					"item_index": extension.Int(int64(i)),
				},
			},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		count, err := client.InsertManyFast(ctx, params)
		if err != nil {
			b.Fatalf("insert many fast failed: %v", err)
		}
		if count != 64 {
			b.Fatalf("expected 64 jobs inserted, got %d", count)
		}
	}
	b.ReportMetric(64, "jobs/op")

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_EnqueueManySequential64(b *testing.B) {
	_, pool, _, engine, _ := setupRiverBench(b, 1, nil)
	ctx := context.Background()

	// Using IdempotencyKey triggers the safe deduplicating sequential loop
	jobs := make([]worker.Job, 64)
	for i := range jobs {
		jobs[i] = worker.Job{
			JobKind:        "bench:river:work",
			Queue:          "bench_river",
			CorrelationID:  fmt.Sprintf("corr-river-seq-%d", i),
			IdempotencyKey: fmt.Sprintf("idem-key-%d", i),
			Payload: extension.Object{
				"item_index": extension.Int(int64(i)),
			},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if err := engine.EnqueueMany(ctx, jobs); err != nil {
			b.Fatalf("enqueue many sequential failed: %v", err)
		}
	}
	b.ReportMetric(64, "jobs/op")

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_WorkerDrainThroughput(b *testing.B) {
	_, pool, client, engine, proc := setupRiverBench(b, 16, nil)
	ctx := context.Background()

	const batchSize = 100
	jobs := make([]worker.Job, batchSize)
	for i := range jobs {
		jobs[i] = worker.Job{
			JobKind:       "bench:river:work",
			Queue:         "bench_river",
			CorrelationID: fmt.Sprintf("corr-river-drain-%d", i),
		}
	}

	if err := client.Start(ctx); err != nil {
		b.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		done := make(chan struct{}, 1)
		proc.doneChan = done
		proc.targetCount = proc.handled.Load() + batchSize

		if err := engine.EnqueueMany(ctx, jobs); err != nil {
			b.Fatalf("enqueue many failed: %v", err)
		}

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			b.Fatalf("drain timeout: processed %d / %d", proc.handled.Load(), proc.targetCount)
		}
	}
	b.ReportMetric(float64(batchSize), "jobs/op")

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}

func BenchmarkServiceBackedRiver_WorkerDrainThroughput_Scratch(b *testing.B) {
	scratchProc := &riverScratchTestProcessor{
		riverTestProcessor: riverTestProcessor{
			kind:  "bench:river:work",
			queue: "bench_river",
		},
	}
	_, pool, client, engine, _ := setupRiverBench(b, 16, scratchProc)
	ctx := context.Background()

	const batchSize = 100
	jobs := make([]worker.Job, batchSize)
	for i := range jobs {
		jobs[i] = worker.Job{
			JobKind:       "bench:river:work",
			Queue:         "bench_river",
			CorrelationID: fmt.Sprintf("corr-river-drain-scratch-%d", i),
		}
	}

	if err := client.Start(ctx); err != nil {
		b.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		done := make(chan struct{}, 1)
		scratchProc.doneChan = done
		scratchProc.targetCount = scratchProc.handled.Load() + batchSize

		if err := engine.EnqueueMany(ctx, jobs); err != nil {
			b.Fatalf("enqueue many failed: %v", err)
		}

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			b.Fatalf("drain timeout: processed %d / %d", scratchProc.handled.Load(), scratchProc.targetCount)
		}
	}
	b.ReportMetric(float64(batchSize), "jobs/op")

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = 'bench_river'")
}
