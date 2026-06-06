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
	return part.bulkLoadFrom(ctx, func(visit database.RecordVisitor) error {
		return streamRebuildRecords(ctx, source, part.spec, query, visit)
	})
}

func streamRebuildRecords(ctx context.Context, source database.StateStore, spec ProjectionSpec, query Query, visit database.RecordVisitor) error {
	orgID := strings.TrimSpace(query.OrganizationID)
	if orgID == "" {
		return ErrInvalidEvent
	}
	limit := query.Limit
	if limit <= 0 {
		limit = spec.MaxRecords
	}
	recordQuery := query.RecordQuery()
	recordQuery.Limit = limit
	if snapshot, ok := source.(database.NormalizedSnapshotStore); ok {
		records, err := snapshot.ListNormalizedRecords(ctx, spec.Domain, spec.Collection, orgID, recordQuery)
		if err != nil {
			return err
		}
		for i := range records {
			if err := visit(records[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return source.ForEachRecord(ctx, spec.Domain, spec.Collection, orgID, recordQuery, visit)
}

func (p *partition) bulkLoad(ctx context.Context, records []database.DomainRecord) (ApplyResult, error) {
	return p.bulkLoadFrom(ctx, func(visit database.RecordVisitor) error {
		for i := range records {
			if err := visit(records[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *partition) bulkLoadFrom(ctx context.Context, load func(database.RecordVisitor) error) (ApplyResult, error) {
	if load == nil {
		return ApplyResult{}, ErrInvalidEvent
	}
	next := newPartition(p.spec)
	nextRegistry := next.activeRegistry()
	publisher := newIndexPublisher()
	result := ApplyResult{Epoch: p.epoch.Load()}
	version := uint64(1)
	watermark := uint64(0)
	err := load(func(rec database.DomainRecord) error {
		if err := ctxErr(ctx); err != nil {
			next.rejectedApplies.Add(1)
			return err
		}
		rec, err := normalizeRecord(rec)
		if err != nil {
			next.rejectedApplies.Add(1)
			return err
		}
		if err := next.validateRecordScope(rec); err != nil {
			next.rejectedApplies.Add(1)
			return err
		}
		key := recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)
		state, err := next.upsertLocked(nextRegistry, publisher, key, rec, "", version)
		if err != nil {
			next.rejectedApplies.Add(1)
			return err
		}
		if state != applyStateApplied {
			next.rejectedApplies.Add(1)
			result.Ignored++
			return nil
		}
		watermark = version
		version++
		result.Applied++
		return nil
	})
	if err != nil {
		p.rejectedApplies.Add(next.rejectedApplies.Load())
		return result, err
	}
	if result.Applied > 0 {
		next.indexCompactions.Add(int64(publisher.publish()))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishing.Store(true)
	defer p.publishing.Store(false)
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
