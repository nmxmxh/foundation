package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

// The canonical projection job contract. Write repositories that keep durable
// truth in normalized tables enqueue one of these — via Engine.EnqueueTx, in
// the same transaction as the domain INSERT/UPDATE/DELETE — carrying only the
// record identity. A single RecordProjectionProcessor registration then covers
// every collection: it resolves the committed row (inline payload or
// read-back) and writes it through the ProjectedRuntimeStore, which buys the
// durable record-store mirror, the hot-partition apply, live gateway fan-out,
// and cold-start rebuildability in one call.
const (
	// RecordProjectionJobKind is the river job kind the generic processor
	// registers for.
	RecordProjectionJobKind = "hermes_record_projection"
	// RecordProjectionQueue is the default queue for projection jobs.
	RecordProjectionQueue = "hermes_projection"

	recordProjectionOpUpsert = "upsert"
	recordProjectionOpDelete = "delete"
)

// NewRecordProjectionJob builds the canonical projection job for a committed
// record. mutationTag disambiguates successive mutations of the same record in
// the engine's idempotency dedup (kind + IdempotencyKey): pass something that
// changes per mutation — updated_at, a version counter, or the event id. An
// empty tag falls back to enqueue time, which never dedupes (safe, at the cost
// of possible duplicate jobs; UpsertRecord replays are idempotent anyway).
//
// Data resolution is the processor's job, not the producer's: set
// job.RawPayload to the row's JSON to push data inline, or leave it empty and
// the processor reads the row back through the app's RecordFetcher.
func NewRecordProjectionJob(domain, collection, organizationID, recordID, mutationTag string) (worker.Job, error) {
	return newRecordProjectionJob(recordProjectionOpUpsert, domain, collection, organizationID, recordID, mutationTag)
}

// NewRecordProjectionDeleteJob builds the projection job that removes a record
// from the projection after the domain row was deleted.
func NewRecordProjectionDeleteJob(domain, collection, organizationID, recordID, mutationTag string) (worker.Job, error) {
	return newRecordProjectionJob(recordProjectionOpDelete, domain, collection, organizationID, recordID, mutationTag)
}

func newRecordProjectionJob(operation, domain, collection, organizationID, recordID, mutationTag string) (worker.Job, error) {
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)
	recordID = strings.TrimSpace(recordID)
	if domain == "" || collection == "" || organizationID == "" || recordID == "" {
		return worker.Job{}, errors.New("hermes projection job requires domain, collection, organization_id, and record_id")
	}
	mutationTag = strings.TrimSpace(mutationTag)
	if mutationTag == "" {
		mutationTag = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return worker.Job{
		JobKind: RecordProjectionJobKind,
		Queue:   RecordProjectionQueue,
		Payload: extension.Object{
			"operation":       extension.String(operation),
			"domain":          extension.String(domain),
			"collection":      extension.String(collection),
			"organization_id": extension.String(organizationID),
			"record_id":       extension.String(recordID),
		},
		CorrelationID:  fmt.Sprintf("proj:%s:%s:%s", domain, collection, recordID),
		IdempotencyKey: fmt.Sprintf("proj:%s:%s:%s:%s:%s", domain, collection, organizationID, recordID, mutationTag),
	}, nil
}

// RecordFetcher loads the committed data for one record identity — the
// read-back seam an app implements once, typically a SELECT from the
// normalized table that owns the collection, with Data keys matching the
// column names its projection consumers read. found=false means the row no
// longer exists; the processor converges the projection by deleting the
// record.
type RecordFetcher func(ctx context.Context, domain, collection, organizationID, recordID string) (database.RecordData, bool, error)

// RecordProjectionProcessor is the one generic worker.Processor for
// RecordProjectionJobKind. Register it once (mirroring any other river
// worker registration) and every collection's projection flows through it,
// keyed by job args.
type RecordProjectionProcessor struct {
	projected   *ProjectedRuntimeStore
	fetch       RecordFetcher
	queue       string
	maxAttempts int
}

func NewRecordProjectionProcessor(projected *ProjectedRuntimeStore, fetch RecordFetcher) (*RecordProjectionProcessor, error) {
	if projected == nil {
		return nil, errors.New("hermes projected runtime store is required")
	}
	if fetch == nil {
		return nil, errors.New("hermes record fetcher is required")
	}
	return &RecordProjectionProcessor{
		projected:   projected,
		fetch:       fetch,
		queue:       RecordProjectionQueue,
		maxAttempts: 3,
	}, nil
}

func (p *RecordProjectionProcessor) WithQueue(queue string) *RecordProjectionProcessor {
	if trimmed := strings.TrimSpace(queue); trimmed != "" {
		p.queue = trimmed
	}
	return p
}

func (p *RecordProjectionProcessor) WithMaxAttempts(maxAttempts int) *RecordProjectionProcessor {
	if maxAttempts > 0 {
		p.maxAttempts = maxAttempts
	}
	return p
}

func (p *RecordProjectionProcessor) Kind() string { return RecordProjectionJobKind }

func (p *RecordProjectionProcessor) Queue() string { return p.queue }

func (p *RecordProjectionProcessor) MaxAttempts() int { return p.maxAttempts }

func (p *RecordProjectionProcessor) Handle(ctx context.Context, job worker.Job) error {
	domain, _ := job.Payload.GetString("domain")
	collection, _ := job.Payload.GetString("collection")
	organizationID, _ := job.Payload.GetString("organization_id")
	recordID, _ := job.Payload.GetString("record_id")
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(collection) == "" ||
		strings.TrimSpace(organizationID) == "" || strings.TrimSpace(recordID) == "" {
		// Malformed identity is deterministic: a retry replays the same
		// malformed payload into the same rejection, burning attempts until
		// dead-letter. Drop it; the enqueue-side constructor makes this
		// unreachable for jobs built with NewRecordProjectionJob.
		return nil
	}

	operation, _ := job.Payload.GetString("operation")
	if operation == recordProjectionOpDelete {
		return p.projected.DeleteRecord(ctx, domain, collection, organizationID, recordID)
	}

	var data database.RecordData
	if len(job.RawPayload) > 0 {
		if err := data.UnmarshalJSON(job.RawPayload); err != nil {
			return fmt.Errorf("hermes projection job inline payload: %w", err)
		}
	} else {
		fetched, found, err := p.fetch(ctx, domain, collection, organizationID, recordID)
		if err != nil {
			return err
		}
		if !found {
			// The row vanished between commit and projection (or the job
			// replayed after a delete): converge by removing the record.
			return p.projected.DeleteRecord(ctx, domain, collection, organizationID, recordID)
		}
		data = fetched
	}

	_, err := p.projected.UpsertRecord(ctx, database.DomainRecord{
		Domain:         domain,
		Collection:     collection,
		OrganizationID: organizationID,
		RecordID:       recordID,
		Data:           data,
	})
	return err
}
