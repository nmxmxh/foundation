package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	"github.com/riverqueue/river"
	"go.uber.org/zap"
)

// Processor handles jobs of a specific kind.
type Processor interface {
	Kind() string
	Queue() string
	MaxAttempts() int
	Handle(context.Context, Job) error
}

// Engine is a worker runtime with queue fanout, retries, and idempotency guards.
// It can operate in-memory (legacy/dev) or delegate to River (production).
type Engine struct {
	mu         sync.RWMutex
	processors map[string]Processor
	queues     map[string]chan Job
	workers    map[string]int
	dedupe     map[string]struct{}
	jobHealth  map[string]JobHealthSnapshot
	log        logger.Logger
	wg         sync.WaitGroup

	riverClient   *river.Client[pgx.Tx]
	metadataStore MetadataStore
}

func NewEngine(queueWorkers map[string]int, l logger.Logger) *Engine {
	workers := map[string]int{}
	for queue, count := range queueWorkers {
		if count <= 0 {
			continue
		}
		workers[queue] = count
	}
	if l == nil {
		l, _ = logger.NewDefault()
	}
	return &Engine{
		processors: map[string]Processor{},
		queues:     map[string]chan Job{},
		workers:    workers,
		dedupe:     map[string]struct{}{},
		jobHealth:  map[string]JobHealthSnapshot{},
		log:        l.With(zap.String("component", "worker_engine")),
	}
}

// SetRiverClient enables the River-backed persistent queue.
func (e *Engine) SetRiverClient(client *river.Client[pgx.Tx]) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.riverClient = client
	if client != nil {
		e.metadataStore = NewPostgresMetadataStore(client)
	}
}

// SetMetadataStore manually overrides the metadata store.
func (e *Engine) SetMetadataStore(store MetadataStore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.metadataStore = store
}

func (e *Engine) Register(processor Processor) error {
	if processor == nil {
		return errors.New("processor is required")
	}
	kind := processor.Kind()
	if kind == "" {
		return errors.New("processor kind is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.processors[kind] = processor
	if _, ok := e.queues[processor.Queue()]; !ok {
		e.queues[processor.Queue()] = make(chan Job, 1024)
	}
	if _, ok := e.workers[processor.Queue()]; !ok {
		e.workers[processor.Queue()] = 1
	}
	return nil
}

func (e *Engine) Start(ctx context.Context) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.processors) == 0 {
		return errors.New("no processors registered")
	}

	// In-memory queue runners (only used if River is not active or for specific local-only queues)
	for queue, jobs := range e.queues {
		workerCount := e.workers[queue]
		if workerCount <= 0 {
			workerCount = 1
		}
		for i := 0; i < workerCount; i++ {
			e.wg.Add(1)
			go func(queue string, jobs <-chan Job, index int) {
				defer e.wg.Done()
				e.runQueue(ctx, queue, jobs, index)
			}(queue, jobs, i+1)
		}
	}
	return nil
}

func (e *Engine) Wait() {
	e.wg.Wait()
}

func (e *Engine) HealthSnapshot() map[string]JobHealthSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snapshot := make(map[string]JobHealthSnapshot, len(e.jobHealth))
	for key, value := range e.jobHealth {
		snapshot[key] = value
	}
	return snapshot
}

func (e *Engine) Enqueue(ctx context.Context, job Job) error {
	return e.EnqueueTx(ctx, nil, job)
}

func (e *Engine) EnqueueTx(ctx context.Context, tx pgx.Tx, job Job) error {
	job.Normalize()
	if err := job.Validate(); err != nil {
		return err
	}
	e.recordHealth(job, JobHealthQueued, nil, time.Time{}, time.Time{})

	e.mu.RLock()
	client := e.riverClient
	processor, ok := e.processors[job.Kind()]
	queueCh, qOk := e.queues[job.Queue]
	e.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no processor for kind %s", job.Kind())
	}

	// If River is active, we prefer it for persistence and reliability.
	if client != nil {
		opts := &river.InsertOpts{
			Queue:       job.Queue,
			MaxAttempts: job.MaxAttempts,
		}
		if !job.ScheduledAt.IsZero() {
			opts.ScheduledAt = job.ScheduledAt
		}

		var jobID int64
		if tx != nil {
			res, err := client.InsertTx(ctx, tx, job, opts)
			if err != nil {
				return fmt.Errorf("failed to insert job into river: %w", err)
			}
			jobID = res.Job.ID
		} else {
			res, err := client.Insert(ctx, job, opts)
			if err != nil {
				return fmt.Errorf("failed to insert job into river: %w", err)
			}
			jobID = res.Job.ID
		}

		// If there is a raw payload or explicit metadata, save it to the sidecar table
		if len(job.RawPayload) > 0 || len(job.Metadata) > 0 {
			e.mu.RLock()
			ms := e.metadataStore
			e.mu.RUnlock()

			if ms != nil {
				workflowName := job.Kind()
				if v, ok := job.Metadata["workflow_name"].(string); ok {
					workflowName = v
				}

				meta := JobMetadata{
					JobID:         jobID,
					WorkflowName:  workflowName,
					EntityType:    "job",
					EntityID:      fmt.Sprintf("%d", jobID),
					CorrelationID: job.CorrelationID,
					RawPayload:    job.RawPayload,
					TrackingData:  job.Metadata,
				}
				if err := ms.Save(ctx, meta); err != nil {
					e.log.Warn("failed to save job metadata", zap.Int64("job_id", jobID), zap.Error(err))
				}
			}
		}
		return nil
	}

	// Fallback to in-memory queue
	if !qOk {
		return fmt.Errorf("queue %s is not configured", job.Queue)
	}
	if processor.Queue() != job.Queue {
		return fmt.Errorf("processor %s expects queue %s, got %s", job.Kind(), processor.Queue(), job.Queue)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case queueCh <- job:
		observability.Default().RecordWorker(job.Kind(), job.Queue, "enqueued")
		observability.Default().RecordQueueDepth(job.Queue, len(queueCh))
		return nil
	}
}

func (e *Engine) runQueue(ctx context.Context, queue string, jobs <-chan Job, workerIndex int) {
	e.log.Info("worker queue runner started", zap.String("queue", queue), zap.Int("worker", workerIndex))
	for {
		observability.Default().RecordQueueDepth(queue, len(jobs))
		select {
		case <-ctx.Done():
			e.log.Info("worker queue runner stopped", zap.String("queue", queue), zap.Int("worker", workerIndex))
			return
		case job := <-jobs:
			e.handleJob(ctx, queue, workerIndex, job)
		}
	}
}

func (e *Engine) handleJob(ctx context.Context, queue string, workerIndex int, job Job) {
	e.mu.RLock()
	processor, ok := e.processors[job.Kind()]
	e.mu.RUnlock()
	if !ok {
		e.log.Error("dropping job without processor", zap.String("kind", job.Kind()), zap.String("queue", queue))
		observability.Default().RecordWorker(job.Kind(), queue, "dropped_no_processor")
		e.recordHealth(job, JobHealthDroppedNoProcessor, errors.New("processor not registered"), time.Time{}, time.Now().UTC())
		return
	}

	if key := dedupeKey(job); key != "" {
		e.mu.Lock()
		if _, exists := e.dedupe[key]; exists {
			e.mu.Unlock()
			e.log.Info("deduped job replay", zap.String("kind", job.Kind()), zap.String("queue", queue), zap.String("key", key))
			observability.Default().RecordWorker(job.Kind(), queue, "deduped")
			e.recordHealth(job, JobHealthDeduped, nil, time.Time{}, time.Now().UTC())
			return
		}
		e.mu.Unlock()
	}

	if !job.ScheduledAt.IsZero() && job.ScheduledAt.After(time.Now().UTC()) {
		timer := time.NewTimer(time.Until(job.ScheduledAt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	startedAt := time.Now().UTC()
	e.recordHealth(job, JobHealthProcessing, nil, startedAt, time.Time{})
	runCtx, cancel := context.WithTimeout(ctx, job.Timeout())
	defer cancel()
	observability.Default().RecordWorker(job.Kind(), queue, "processing")

	err := processor.Handle(runCtx, job)
	if err != nil {
		finishedAt := time.Now().UTC()
		timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded)
		e.log.Warn("job processing failed",
			zap.String("kind", job.Kind()),
			zap.String("queue", queue),
			zap.Int("attempt", job.Attempt+1),
			zap.Int("max_attempts", job.MaxAttempts),
			zap.Error(err),
			zap.Int("worker", workerIndex),
		)

		job.Attempt++
		if job.Attempt >= job.MaxAttempts {
			e.log.Error("job exhausted retries", zap.String("kind", job.Kind()), zap.String("queue", queue))
			observability.Default().RecordWorker(job.Kind(), queue, "failed_exhausted")
			if timedOut {
				e.recordHealth(job, JobHealthTimedOut, err, startedAt, finishedAt)
			} else {
				e.recordHealth(job, JobHealthFailedExhausted, err, startedAt, finishedAt)
			}
			return
		}
		backoff := job.NextBackoff()
		job.ScheduledAt = time.Now().UTC().Add(backoff)
		observability.Default().RecordWorker(job.Kind(), queue, "retry_scheduled")
		e.recordHealth(job, JobHealthRetryScheduled, err, startedAt, finishedAt)
		go func(retryJob Job) {
			timer := time.NewTimer(backoff)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_ = e.Enqueue(context.Background(), retryJob)
			}
		}(job)
		return
	}

	if key := dedupeKey(job); key != "" {
		e.mu.Lock()
		e.dedupe[key] = struct{}{}
		e.mu.Unlock()
	}
	e.log.Info("job processed",
		zap.String("kind", job.Kind()),
		zap.String("queue", queue),
		zap.Int("worker", workerIndex),
	)
	observability.Default().RecordWorker(job.Kind(), queue, "succeeded")
	e.recordHealth(job, JobHealthSucceeded, nil, startedAt, time.Now().UTC())
}

func dedupeKey(job Job) string {
	if job.IdempotencyKey != "" {
		return job.Kind() + ":" + job.IdempotencyKey
	}
	if v, ok := job.Metadata["idempotency_key"].(string); ok && v != "" {
		return job.Kind() + ":" + v
	}
	return ""
}

func (e *Engine) recordHealth(job Job, state JobHealthState, err error, startedAt, finishedAt time.Time) {
	key := job.HealthKey()
	if key == "" {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	snapshot := e.jobHealth[key]
	snapshot.Key = key
	snapshot.Kind = job.Kind()
	snapshot.Queue = job.Queue
	snapshot.State = state
	snapshot.Attempt = job.Attempt
	snapshot.MaxAttempts = job.MaxAttempts
	snapshot.TimeoutMS = int(job.Timeout() / time.Millisecond)
	snapshot.CorrelationID = job.CorrelationID
	snapshot.ScheduledAt = job.ScheduledAt
	if !startedAt.IsZero() {
		snapshot.StartedAt = startedAt
	}
	if !finishedAt.IsZero() {
		snapshot.FinishedAt = finishedAt
	}
	if err != nil {
		snapshot.LastError = err.Error()
	}
	snapshot.UpdatedAt = time.Now().UTC()
	e.jobHealth[key] = snapshot
}

// Bridge creates a river.Worker that delegates to a Processor.
type Bridge struct {
	river.WorkerDefaults[Job]
	Processor Processor
}

func (b *Bridge) Work(ctx context.Context, job *river.Job[Job]) error {
	return b.Processor.Handle(ctx, job.Args)
}
