//go:build servicebacked

package servicebacked

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

func TestServiceBackedRiver_TransactionalRollbackPreventsJobExecution(t *testing.T) {
	_, pool, client, engine, proc := setupRiverBench(t, 4, nil)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction failed: %v", err)
	}

	job := worker.Job{
		JobKind:       proc.kind,
		Queue:         proc.queue,
		CorrelationID: "corr-rollback-test",
		Payload:       extension.Object{"status": extension.String("uncommitted")},
	}

	if err := engine.EnqueueTx(ctx, tx, job); err != nil {
		t.Fatalf("EnqueueTx failed: %v", err)
	}

	// Rollback the transaction: the job must NEVER become visible to River
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	// Wait briefly: processor must NOT be invoked
	time.Sleep(300 * time.Millisecond)
	if proc.handled.Load() != 0 {
		t.Fatalf("expected 0 jobs handled after rollback, got %d", proc.handled.Load())
	}
}

func TestServiceBackedRiver_TransactionalCommitExecutesJob(t *testing.T) {
	_, pool, client, engine, proc := setupRiverBench(t, 4, nil)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction failed: %v", err)
	}

	job := worker.Job{
		JobKind:       proc.kind,
		Queue:         proc.queue,
		CorrelationID: "corr-commit-test",
		Payload:       extension.Object{"status": extension.String("committed")},
	}

	if err := engine.EnqueueTx(ctx, tx, job); err != nil {
		t.Fatalf("EnqueueTx failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	done := make(chan struct{}, 1)
	proc.doneChan = done
	proc.targetCount = 1

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for committed job execution; handled=%d", proc.handled.Load())
	}

	if proc.handled.Load() != 1 {
		t.Fatalf("expected 1 job handled, got %d", proc.handled.Load())
	}
}

func TestServiceBackedRiver_BatchCopyFromIngestionAndDrain(t *testing.T) {
	_, pool, client, engine, proc := setupRiverBench(t, 8, nil)
	ctx := context.Background()

	const batchCount = 64
	jobs := make([]worker.Job, batchCount)
	for i := range jobs {
		jobs[i] = worker.Job{
			JobKind:       proc.kind,
			Queue:         proc.queue,
			CorrelationID: fmt.Sprintf("corr-batch-copyfrom-%d", i),
			RawPayload:    []byte(fmt.Sprintf(`{"batch_index":%d,"data":"inline-content"}`, i)),
		}
	}

	// This exercises client.InsertManyFastTx / client.InsertManyFast via Postgres COPY FROM protocol
	if err := engine.EnqueueMany(ctx, jobs); err != nil {
		t.Fatalf("EnqueueMany (COPY FROM) failed: %v", err)
	}

	done := make(chan struct{}, 1)
	proc.doneChan = done
	proc.targetCount = batchCount

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out draining COPY FROM batch: processed %d / %d", proc.handled.Load(), batchCount)
	}

	if proc.handled.Load() < batchCount {
		t.Fatalf("expected at least %d jobs handled, got %d", batchCount, proc.handled.Load())
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = $1", proc.queue)
}

func TestServiceBackedRiver_DeduplicationParity_CP38(t *testing.T) {
	_, pool, client, engine, proc := setupRiverBench(t, 4, nil)
	ctx := context.Background()

	const sameKey = "idem-key-duplicate-token-123"
	jobs := []worker.Job{
		{
			JobKind:        proc.kind,
			Queue:          proc.queue,
			CorrelationID:  "corr-dup-1",
			IdempotencyKey: sameKey,
		},
		{
			JobKind:        proc.kind,
			Queue:          proc.queue,
			CorrelationID:  "corr-dup-2",
			IdempotencyKey: sameKey,
		},
		{
			JobKind:        proc.kind,
			Queue:          proc.queue,
			CorrelationID:  "corr-dup-3",
			IdempotencyKey: sameKey,
		},
	}

	// Engine must process sequentially and deduplicate without CTE or database index collisions
	if err := engine.EnqueueMany(ctx, jobs); err != nil {
		t.Fatalf("EnqueueMany unique jobs failed: %v", err)
	}

	done := make(chan struct{}, 1)
	proc.doneChan = done
	proc.targetCount = 1

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for deduplicated job execution")
	}

	time.Sleep(200 * time.Millisecond)
	// Only 1 job must have been executed
	if proc.handled.Load() != 1 {
		t.Fatalf("expected exactly 1 deduplicated execution, got %d", proc.handled.Load())
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = $1", proc.queue)
}

func TestServiceBackedRiver_MetadataSidecarStoreAndFetch(t *testing.T) {
	_, pool, _, _, _ := setupRiverBench(t, 1, nil)
	ctx := context.Background()

	store := worker.NewPostgresMetadataStore(pool)

	rawPayload := make([]byte, 128*1024) // 128 KiB
	for i := range rawPayload {
		rawPayload[i] = byte(i % 256)
	}

	const jobID = int64(987654321)
	// Ensure parent river_job row exists for foreign key constraint
	const insertParent = `
	INSERT INTO river_job (id, kind, queue, state)
	VALUES ($1, 'bench:sidecar', 'bench_river', 'available')
	ON CONFLICT (id) DO NOTHING;`
	if _, err := pool.Exec(ctx, insertParent, jobID); err != nil {
		t.Fatalf("insert parent river_job failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM river_job WHERE id = $1", jobID)
	}()

	meta := worker.JobMetadata{
		JobID:         jobID,
		WorkflowName:  "heavy_document_workflow",
		EntityType:    "pdf_report",
		EntityID:      "report_456",
		UserID:        "user_alice",
		CorrelationID: "corr_meta_test",
		RawPayload:    rawPayload,
		TrackingData: extension.Object{
			"status":   extension.String("in_progress"),
			"priority": extension.Int(10),
		},
	}

	// 1. Save to river_job_metadata
	if err := store.Save(ctx, meta); err != nil {
		t.Fatalf("PostgresMetadataStore.Save failed: %v", err)
	}

	// 2. Fetch from river_job_metadata
	loaded, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("PostgresMetadataStore.Get failed: %v", err)
	}

	if loaded.JobID != jobID || loaded.WorkflowName != meta.WorkflowName || loaded.EntityType != meta.EntityType {
		t.Fatalf("loaded metadata mismatch: %+v vs %+v", loaded, meta)
	}
	if !bytes.Equal(loaded.RawPayload, rawPayload) {
		t.Fatalf("loaded raw_payload bytes mismatch: len %d vs %d", len(loaded.RawPayload), len(rawPayload))
	}
	if status, _ := loaded.TrackingData.GetString("status"); status != "in_progress" {
		t.Fatalf("loaded tracking data status mismatch: %s", status)
	}

	// 3. Update tracking data
	updatedTracking := extension.Object{
		"status": extension.String("completed"),
		"pages":  extension.Int(42),
	}
	if err := store.UpdateTrackingData(ctx, jobID, updatedTracking); err != nil {
		t.Fatalf("UpdateTrackingData failed: %v", err)
	}

	loadedAfterUpdate, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if status, _ := loadedAfterUpdate.TrackingData.GetString("status"); status != "completed" {
		t.Fatalf("expected updated status 'completed', got %s", status)
	}
}

func TestServiceBackedRiver_ScratchArenaZeroAllocExecution(t *testing.T) {
	var arenaHandled atomic.Int64
	var arenaBytesAllocated atomic.Int64
	done := make(chan struct{}, 1)

	scratchProc := &testScratchHandler{
		kind:     "bench:river:scratch_test",
		queue:    "bench_river",
		doneChan: done,
		handled:  &arenaHandled,
		bytesAlloc: &arenaBytesAllocated,
	}

	_, pool, client, engine, _ := setupRiverBench(t, 4, scratchProc)
	ctx := context.Background()

	job := worker.Job{
		JobKind:       scratchProc.kind,
		Queue:         scratchProc.queue,
		CorrelationID: "corr-scratch-execution-test",
		Payload:       extension.Object{"items": extension.Int(5)},
	}

	if err := engine.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for scratch handler execution")
	}

	if arenaHandled.Load() != 1 {
		t.Fatalf("expected 1 scratch job handled, got %d", arenaHandled.Load())
	}
	if arenaBytesAllocated.Load() != 512 {
		t.Fatalf("expected 512 arena bytes allocated, got %d", arenaBytesAllocated.Load())
	}

	cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanCtx, "DELETE FROM river_job WHERE queue = $1", scratchProc.queue)
}

type testScratchHandler struct {
	kind       string
	queue      string
	doneChan   chan struct{}
	handled    *atomic.Int64
	bytesAlloc *atomic.Int64
}

func (h *testScratchHandler) Kind() string     { return h.kind }
func (h *testScratchHandler) Queue() string    { return h.queue }
func (h *testScratchHandler) MaxAttempts() int { return 3 }
func (h *testScratchHandler) Handle(_ context.Context, _ worker.Job) error {
	return nil
}

func (h *testScratchHandler) HandleWithArena(_ context.Context, arena *scratch.Arena, _ worker.Job) error {
	h.handled.Add(1)
	if arena != nil {
		buf, err := arena.Alloc(512)
		if err == nil {
			h.bytesAlloc.Add(int64(len(buf)))
		}
	}
	select {
	case h.doneChan <- struct{}{}:
	default:
	}
	return nil
}
