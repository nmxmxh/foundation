package worker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/tracing"
	"github.com/riverqueue/river"
)

const (
	defaultQueueCapacity      = 1024
	queueScaleThreshold       = defaultQueueCapacity / 2
	maxAdaptiveWorkers        = 64
	maxDedupeEntries          = 100000
	defaultRiverInsertTimeout = 5 * time.Second
)

var errQueueFull = errors.New("worker queue full")

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
	dedupe     map[string]time.Time
	jobHealth  map[string]JobHealthSnapshot
	log        logger.Logger
	predictor  ScalingPredictor
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
		dedupe:     map[string]time.Time{},
		jobHealth:  map[string]JobHealthSnapshot{},
		log:        l.With("component", "worker_engine"),
		predictor:  NewTrendPredictor(),
	}
}

// SetRiverClient enables the River-backed persistent queue.
func (e *Engine) SetRiverClient(client *river.Client[pgx.Tx], pool *pgxpool.Pool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.riverClient = client
	if pool != nil {
		e.metadataStore = NewPostgresMetadataStore(pool)
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
		e.queues[processor.Queue()] = make(chan Job, defaultQueueCapacity)
	}
	if _, ok := e.workers[processor.Queue()]; !ok {
		e.workers[processor.Queue()] = 1
	}
	return nil
}

func (e *Engine) Start(ctx context.Context) error {
	e.mu.RLock()
	if len(e.processors) == 0 {
		e.mu.RUnlock()
		return errors.New("no processors registered")
	}
	runners := make([]struct {
		queue       string
		jobs        <-chan Job
		workerCount int
	}, 0, len(e.queues))
	for queue, jobs := range e.queues {
		workerCount := e.workers[queue]
		if workerCount <= 0 {
			workerCount = 1
		}
		runners = append(runners, struct {
			queue       string
			jobs        <-chan Job
			workerCount int
		}{queue: queue, jobs: jobs, workerCount: workerCount})
	}
	e.mu.RUnlock()

	totalWorkers := 2
	for _, runner := range runners {
		totalWorkers += runner.workerCount
	}
	e.wg.Add(totalWorkers)

	// In-memory queue runners
	for _, runner := range runners {
		for i := 0; i < runner.workerCount; i++ {
			go func(queue string, jobs <-chan Job, index int) {
				defer e.wg.Done()
				e.runQueue(ctx, queue, jobs, index)
			}(runner.queue, runner.jobs, i+1)
		}
	}

	// Start adaptive scaling controller
	go func() {
		defer e.wg.Done()
		e.adaptiveScaler(ctx)
	}()

	// Start dedupe cleanup
	go func() {
		defer e.wg.Done()
		e.dedupeCleaner(ctx)
	}()

	return nil
}

func (e *Engine) dedupeCleaner(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.pruneDedupe(time.Now().UTC())
		}
	}
}

func (e *Engine) pruneDedupe(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, expiry := range e.dedupe {
		if now.After(expiry) {
			delete(e.dedupe, key)
		}
	}
	if len(e.dedupe) <= maxDedupeEntries {
		return
	}
	overflow := len(e.dedupe) - maxDedupeEntries
	for key := range e.dedupe {
		delete(e.dedupe, key)
		overflow--
		if overflow <= 0 {
			return
		}
	}
}

func (e *Engine) adaptiveScaler(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.mu.RLock()
			queues := make([]struct {
				name string
				ch   chan Job
			}, 0, len(e.queues))
			for name, ch := range e.queues {
				queues = append(queues, struct {
					name string
					ch   chan Job
				}{name, ch})
			}
			e.mu.RUnlock()

			for _, q := range queues {
				e.mu.RLock()
				depth := len(q.ch)
				currentWorkers := e.workers[q.name]
				e.mu.RUnlock()

				// Scale up if queue is getting full (> 50%) and we haven't reached a hard limit.
				if depth > queueScaleThreshold && currentWorkers < maxAdaptiveWorkers {
					newWorkers := 4
					if currentWorkers+newWorkers > maxAdaptiveWorkers {
						newWorkers = maxAdaptiveWorkers - currentWorkers
					}
					e.spawnWorkers(ctx, q.name, q.ch, newWorkers)
					e.mu.RLock()
					e.log.InfoContext(ctx, "scaled up workers (reactive)", "queue", q.name, "new_total", e.workers[q.name], "depth", depth)
					e.mu.RUnlock()
				} else if e.predictor != nil {
					// Pre-emptive scaling based on predictive signals
					if needed := e.predictor.Predict(ctx, q.name, depth, currentWorkers); needed > 0 {
						e.spawnWorkers(ctx, q.name, q.ch, needed)
						e.mu.RLock()
						e.log.InfoContext(ctx, "scaled up workers (predictive)", "queue", q.name, "added", needed, "new_total", e.workers[q.name], "depth", depth)
						e.mu.RUnlock()
					}
				}
			}
		}
	}
}

func (e *Engine) Wait() {
	e.wg.Wait()
}

func (e *Engine) HealthSnapshot() map[string]JobHealthSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snapshot := make(map[string]JobHealthSnapshot, len(e.jobHealth))
	maps.Copy(snapshot, e.jobHealth)
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
		err := fmt.Errorf("no processor for kind %s", job.Kind())
		e.recordHealth(job, JobHealthDroppedNoProcessor, err, time.Time{}, time.Now().UTC())
		observability.Default().RecordWorker(job.Kind(), job.Queue, "dropped_no_processor")
		return err
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
			insertCtx, cancel := DetachedContextWithTimeout(ctx, defaultRiverInsertTimeout)
			defer cancel()
			res, err := client.Insert(insertCtx, job, opts)
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
				if v, ok := job.Metadata.GetString("workflow_name"); ok {
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
					e.log.WarnContext(ctx, "failed to save job metadata", "job_id", jobID, "error", err)
				}
			}
		}
		recordJobTrace(job, "worker.enqueue", "enqueued", "river insert accepted")
		return nil
	}

	// Fallback to in-memory queue
	if !qOk {
		err := fmt.Errorf("queue %s is not configured", job.Queue)
		e.recordHealth(job, JobHealthDroppedNoProcessor, err, time.Time{}, time.Now().UTC())
		observability.Default().RecordWorker(job.Kind(), job.Queue, "dropped_no_processor")
		return err
	}
	if processor.Queue() != job.Queue {
		err := fmt.Errorf("processor %s expects queue %s, got %s", job.Kind(), processor.Queue(), job.Queue)
		e.recordHealth(job, JobHealthDroppedNoProcessor, err, time.Time{}, time.Now().UTC())
		observability.Default().RecordWorker(job.Kind(), job.Queue, "dropped_no_processor")
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case queueCh <- job:
		observability.Default().RecordWorker(job.Kind(), job.Queue, "enqueued")
		observability.Default().RecordQueueDepth(job.Queue, len(queueCh))
		recordJobTrace(job, "worker.enqueue", "enqueued", "memory queue accepted")
		return nil
	default:
		err := fmt.Errorf("%w: %s depth=%d capacity=%d", errQueueFull, job.Queue, len(queueCh), cap(queueCh))
		e.recordHealth(job, JobHealthRejectedQueueFull, err, time.Time{}, time.Now().UTC())
		observability.Default().RecordWorker(job.Kind(), job.Queue, "rejected_queue_full")
		observability.Default().RecordQueueDepth(job.Queue, len(queueCh))
		recordJobTrace(job, "worker.enqueue", "rejected_queue_full", err.Error())
		return err
	}
}

func (e *Engine) spawnWorkers(ctx context.Context, queue string, jobs <-chan Job, count int) {
	indices := make([]int, 0, count)
	e.mu.Lock()
	for range count {
		e.workers[queue]++
		indices = append(indices, e.workers[queue])
	}
	e.mu.Unlock()

	for _, index := range indices {
		e.wg.Add(1)
		go func(q string, j <-chan Job, idx int) {
			defer e.wg.Done()
			e.runQueue(ctx, q, j, idx)
		}(queue, jobs, index)
	}
}

func (e *Engine) runQueue(ctx context.Context, queue string, jobs <-chan Job, workerIndex int) {
	e.log.InfoContext(ctx, "worker queue runner started", "queue", queue, "worker", workerIndex)
	for {
		observability.Default().RecordQueueDepth(queue, len(jobs))
		select {
		case <-ctx.Done():
			e.log.InfoContext(ctx, "worker queue runner stopped", "queue", queue, "worker", workerIndex)
			return
		case job := <-jobs:
			if ctx.Err() != nil {
				e.log.InfoContext(ctx, "worker queue runner stopped", "queue", queue, "worker", workerIndex)
				return
			}
			e.handleJob(ctx, queue, workerIndex, job)
		}
	}
}

func (e *Engine) handleJob(ctx context.Context, queue string, workerIndex int, job Job) {
	e.mu.RLock()
	processor, ok := e.processors[job.Kind()]
	e.mu.RUnlock()
	if !ok {
		e.log.ErrorContext(ctx, "dropping job without processor", "kind", job.Kind(), "queue", queue)
		observability.Default().RecordWorker(job.Kind(), queue, "dropped_no_processor")
		e.recordHealth(job, JobHealthDroppedNoProcessor, errors.New("processor not registered"), time.Time{}, time.Now().UTC())
		return
	}

	if key := dedupeKey(job); key != "" {
		e.mu.Lock()
		if _, exists := e.dedupe[key]; exists {
			e.mu.Unlock()
			e.log.InfoContext(ctx, "deduped job replay", "kind", job.Kind(), "queue", queue, "key", key)
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
	runCtx = contextWithJobCorrelation(runCtx, job)
	observability.Default().RecordWorker(job.Kind(), queue, "processing")
	recordJobTrace(job, "worker.process", "processing", "")

	err := processor.Handle(runCtx, job)
	if err != nil {
		finishedAt := time.Now().UTC()
		timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded)
		e.log.WarnContext(runCtx, "job processing failed",
			"kind", job.Kind(),
			"queue", queue,
			"attempt", job.Attempt+1,
			"max_attempts", job.MaxAttempts,
			"error", err,
			"worker", workerIndex,
		)

		job.Attempt++
		if job.Attempt >= job.MaxAttempts {
			e.log.ErrorContext(runCtx, "job exhausted retries", "kind", job.Kind(), "queue", queue)
			observability.Default().RecordWorker(job.Kind(), queue, "failed_exhausted")
			recordJobTrace(job, "worker.process", "failed_exhausted", err.Error())
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
		recordJobTrace(job, "worker.process", "retry_scheduled", err.Error())
		e.recordHealth(job, JobHealthRetryScheduled, err, startedAt, finishedAt)
		timer := time.NewTimer(backoff)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			e.handleJob(ctx, queue, workerIndex, job)
		}
		return
	}

	if key := dedupeKey(job); key != "" {
		e.mu.Lock()
		e.dedupe[key] = time.Now().UTC().Add(24 * time.Hour)
		needsPrune := len(e.dedupe) > maxDedupeEntries
		e.mu.Unlock()
		if needsPrune {
			e.pruneDedupe(time.Now().UTC())
		}
	}
	e.log.InfoContext(runCtx, "job processed",
		"kind", job.Kind(),
		"queue", queue,
		"worker", workerIndex,
	)
	observability.Default().RecordWorker(job.Kind(), queue, "succeeded")
	recordJobTrace(job, "worker.process", "succeeded", "")
	e.recordHealth(job, JobHealthSucceeded, nil, startedAt, time.Now().UTC())
}

func recordJobTrace(job Job, stage, state, detail string) {
	if job.CorrelationID == "" {
		return
	}
	fields := map[string]string{
		"kind":  job.Kind(),
		"queue": job.Queue,
	}
	if job.IdempotencyKey != "" {
		fields["idempotency_key"] = job.IdempotencyKey
	}
	if orgID, ok := job.Metadata.GetString("organization_id"); ok && orgID != "" {
		fields["organization_id"] = orgID
	}
	observability.Default().RecordTrace(job.CorrelationID, stage, "", state, detail, fields)
}

func contextWithJobCorrelation(ctx context.Context, job Job) context.Context {
	if job.CorrelationID == "" {
		return ctx
	}
	md := metadata.FromContext(ctx)
	md.EnsureCorrelation(job.CorrelationID)
	ctx = metadata.IntoContext(ctx, md)
	return tracing.WithCorrelationID(ctx, job.CorrelationID)
}

// DetachedContextWithTimeout returns a bounded child context for durable follow-up
// operations such as cascading job enqueue, failure persistence, and audit writes.
// It preserves request values when possible, but detaches from cancellation when the
// parent is already cancelled or has less time remaining than the required budget.
func DetachedContextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultRiverInsertTimeout
	}
	if parent == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	if err := parent.Err(); err == nil {
		if deadline, ok := parent.Deadline(); !ok || time.Until(deadline) > timeout {
			return context.WithTimeout(parent, timeout)
		}
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func dedupeKey(job Job) string {
	if job.IdempotencyKey != "" {
		return job.Kind() + ":" + job.IdempotencyKey
	}
	if v, ok := job.Metadata.GetString("idempotency_key"); ok && v != "" {
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
	args := job.Args
	args.Normalize()
	runCtx := contextWithJobCorrelation(ctx, args)
	return b.Processor.Handle(runCtx, args)
}
