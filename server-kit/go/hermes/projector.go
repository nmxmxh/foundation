package hermes

import (
	"context"
	"errors"
	"strings"

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
	return err
}
