package hermes

import (
	"context"
	"errors"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

type JobDecoder func(context.Context, worker.Job) ([]Event, error)

type WorkerProcessor struct {
	store       *Store
	projection  string
	kind        string
	queue       string
	maxAttempts int
	decode      JobDecoder
}

func NewWorkerProcessor(store *Store, projection string, kind string, queue string, decode JobDecoder) (*WorkerProcessor, error) {
	if store == nil {
		return nil, errors.New("hermes store is required")
	}
	projection = strings.TrimSpace(projection)
	kind = strings.TrimSpace(kind)
	queue = strings.TrimSpace(queue)
	if projection == "" || kind == "" || queue == "" || decode == nil {
		return nil, errors.New("hermes worker processor configuration is invalid")
	}
	return &WorkerProcessor{
		store:       store,
		projection:  projection,
		kind:        kind,
		queue:       queue,
		maxAttempts: 3,
		decode:      decode,
	}, nil
}

func (p *WorkerProcessor) WithMaxAttempts(maxAttempts int) *WorkerProcessor {
	if maxAttempts > 0 {
		p.maxAttempts = maxAttempts
	}
	return p
}

func (p *WorkerProcessor) Kind() string {
	return p.kind
}

func (p *WorkerProcessor) Queue() string {
	return p.queue
}

func (p *WorkerProcessor) MaxAttempts() int {
	return p.maxAttempts
}

func (p *WorkerProcessor) Handle(ctx context.Context, job worker.Job) error {
	events, err := p.decode(ctx, job)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	correlationID := strings.TrimSpace(job.CorrelationID)
	sourceID := strings.TrimSpace(job.IdempotencyKey)
	for i := range events {
		if events[i].CorrelationID == "" {
			events[i].CorrelationID = correlationID
		}
		if events[i].SourceID == "" {
			events[i].SourceID = sourceID
		}
	}
	_, err = p.store.ApplyBatch(ctx, p.projection, events)
	// Event-level rejections (invalid scope, capacity) are deterministic: a
	// retry replays the same events into the same rejection, burning attempts
	// until dead-letter. The store already skipped only the offending events
	// and applied the rest, so treat the job as done. System errors return and
	// retry as usual.
	if errors.Is(err, ErrInvalidEvent) || errors.Is(err, ErrProjectionLimit) {
		return nil
	}
	return err
}

// RecordDecoder maps one job to the domain records it committed. It is the
// only piece an app supplies to the canonical projection path.
type RecordDecoder func(context.Context, worker.Job) ([]database.DomainRecord, error)

// RecordWorkerProcessor is the canonical event→projection bridge: a River
// worker.Processor that upserts decoded records through the
// ProjectedRuntimeStore. One write buys the full chain — durable
// governance_state_records row, hot-partition apply, live gateway fan-out, and
// cold-start rebuildability via WarmScope/ensureWarm — so a restart never
// leaves the projection empty, unlike applying to the in-memory Store alone.
//
// Enqueue the projection job with Engine.EnqueueTx in the same transaction as
// the domain write and the projection is exactly-once-tied-to-commit with no
// separate outbox. UpsertRecord is idempotent on
// (domain, collection, organization, record_id), so River retries are safe.
type RecordWorkerProcessor struct {
	projected   *ProjectedRuntimeStore
	kind        string
	queue       string
	maxAttempts int
	decode      RecordDecoder
}

func NewRecordWorkerProcessor(projected *ProjectedRuntimeStore, kind string, queue string, decode RecordDecoder) (*RecordWorkerProcessor, error) {
	if projected == nil {
		return nil, errors.New("hermes projected runtime store is required")
	}
	kind = strings.TrimSpace(kind)
	queue = strings.TrimSpace(queue)
	if kind == "" || queue == "" || decode == nil {
		return nil, errors.New("hermes record worker processor configuration is invalid")
	}
	return &RecordWorkerProcessor{
		projected:   projected,
		kind:        kind,
		queue:       queue,
		maxAttempts: 3,
		decode:      decode,
	}, nil
}

func (p *RecordWorkerProcessor) WithMaxAttempts(maxAttempts int) *RecordWorkerProcessor {
	if maxAttempts > 0 {
		p.maxAttempts = maxAttempts
	}
	return p
}

func (p *RecordWorkerProcessor) Kind() string { return p.kind }

func (p *RecordWorkerProcessor) Queue() string { return p.queue }

func (p *RecordWorkerProcessor) MaxAttempts() int { return p.maxAttempts }

func (p *RecordWorkerProcessor) Handle(ctx context.Context, job worker.Job) error {
	records, err := p.decode(ctx, job)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if _, err := p.projected.UpsertRecord(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}
