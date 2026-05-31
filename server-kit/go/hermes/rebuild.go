package hermes

import (
	"context"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// Rebuild refreshes a projection from a canonical StateStore snapshot.
func (s *Store) Rebuild(ctx context.Context, projection string, source database.StateStore, query Query) (ApplyResult, error) {
	if source == nil {
		return ApplyResult{}, ErrInvalidEvent
	}
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	records, err := loadRebuildRecords(ctx, source, part.spec, query)
	if err != nil {
		return ApplyResult{}, err
	}
	return part.bulkLoad(ctx, records)
}

func loadRebuildRecords(ctx context.Context, source database.StateStore, spec ProjectionSpec, query Query) ([]database.DomainRecord, error) {
	orgID := strings.TrimSpace(query.OrganizationID)
	if orgID == "" {
		return nil, ErrInvalidEvent
	}
	limit := query.Limit
	if limit <= 0 {
		limit = spec.MaxRecords
	}
	records, err := source.ListRecords(ctx, spec.Domain, spec.Collection, orgID, query.Filters, limit)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (p *partition) bulkLoad(ctx context.Context, records []database.DomainRecord) (ApplyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishing.Store(true)
	defer p.publishing.Store(false)
	next := newPartition(p.spec)
	nextRegistry := next.activeRegistry()
	publisher := newIndexPublisher()
	result := ApplyResult{Epoch: p.epoch.Load()}
	version := uint64(1)
	watermark := uint64(0)
	for _, rec := range records {
		if err := ctxErr(ctx); err != nil {
			p.rejectedApplies.Add(1)
			return result, err
		}
		rec, err := normalizeRecord(rec)
		if err != nil {
			p.rejectedApplies.Add(1)
			return result, err
		}
		if err := next.validateRecordScope(rec); err != nil {
			p.rejectedApplies.Add(1)
			return result, err
		}
		key := recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)
		state, err := next.upsertLocked(nextRegistry, publisher, key, rec, "", version)
		if err != nil {
			p.rejectedApplies.Add(1)
			return result, err
		}
		if state != applyStateApplied {
			p.rejectedApplies.Add(1)
			result.Ignored++
			continue
		}
		watermark = version
		version++
		result.Applied++
	}
	if result.Applied > 0 {
		next.indexCompactions.Add(int64(publisher.publish()))
	}
	p.registry.Store(nextRegistry)
	p.tombstones = next.tombstones
	p.tombOrder = next.tombOrder
	p.applied = next.applied
	p.applyOrder = next.applyOrder
	p.bytes.Store(next.bytes.Load())
	p.records.Store(next.records.Load())
	p.watermark.Store(watermark)
	p.rejectedApplies.Store(next.rejectedApplies.Load())
	p.indexCompactions.Store(next.indexCompactions.Load())
	result.Epoch = p.epoch.Add(1)
	return result, nil
}
