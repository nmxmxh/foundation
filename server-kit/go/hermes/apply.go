package hermes

import (
	"context"
	"fmt"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func (p *partition) applyBatch(ctx context.Context, events []Event) (ApplyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishing.Store(true)
	defer p.publishing.Store(false)
	result := ApplyResult{Epoch: p.epoch.Load()}
	registry := p.activeRegistry()
	publisher := newIndexPublisher()
	for _, event := range events {
		if err := ctxErr(ctx); err != nil {
			return p.finishApplyLocked(result, publisher), err
		}
		state, version, err := p.applyEventLocked(registry, publisher, event)
		if err != nil {
			p.rejectedApplies.Add(1)
			return p.finishApplyLocked(result, publisher), err
		}
		switch state {
		case applyStateApplied:
			p.observeSourceWatermark(version)
			result.Applied++
		case applyStateDuplicate:
			result.Duplicates++
		default:
			p.rejectedApplies.Add(1)
			result.Ignored++
		}
	}
	return p.finishApplyLocked(result, publisher), nil
}

func (p *partition) applyRecords(ctx context.Context, sourcePrefix string, baseVersion uint64, records []database.DomainRecord) (ApplyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishing.Store(true)
	defer p.publishing.Store(false)
	registry := p.activeRegistry()
	publisher := newIndexPublisher()
	result := ApplyResult{Epoch: p.epoch.Load()}
	for i, rec := range records {
		if err := ctxErr(ctx); err != nil {
			return p.finishApplyLocked(result, publisher), err
		}
		event := Event{Operation: OperationUpsert, Record: rec}
		if baseVersion > 0 {
			event.Version = baseVersion + uint64(i)
		}
		if sourcePrefix != "" {
			event.SourceID = sourcePrefix + ":" + rec.OrganizationID + ":" + rec.RecordID
		}
		state, version, err := p.applyEventLocked(registry, publisher, event)
		if err != nil {
			p.rejectedApplies.Add(1)
			return p.finishApplyLocked(result, publisher), err
		}
		if state == applyStateApplied {
			p.observeSourceWatermark(version)
			result.Applied++
		} else if state == applyStateDuplicate {
			result.Duplicates++
		} else {
			p.rejectedApplies.Add(1)
			result.Ignored++
		}
	}
	return p.finishApplyLocked(result, publisher), nil
}

func (p *partition) finishApplyLocked(result ApplyResult, publisher *indexPublisher) ApplyResult {
	if result.Applied > 0 {
		p.indexCompactions.Add(int64(publisher.publish()))
		result.Epoch = p.epoch.Add(1)
	}
	return result
}

type applyState byte

const (
	applyStateIgnored applyState = iota
	applyStateApplied
	applyStateDuplicate
)

func (p *partition) applyEventLocked(registry *partitionRegistry, publisher *indexPublisher, event Event) (applyState, uint64, error) {
	event.SourceID = strings.TrimSpace(event.SourceID)
	if p.alreadyAppliedLocked(event.SourceID) {
		return applyStateDuplicate, 0, nil
	}
	rec, err := normalizeEventRecord(event)
	if err != nil {
		return applyStateIgnored, 0, err
	}
	if err := p.validateRecordScope(rec); err != nil {
		return applyStateIgnored, 0, err
	}
	version := p.effectiveVersion(event.Version)
	key := recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)
	state, err := p.applyMutationLocked(registry, publisher, key, rec, event, version)
	if err != nil {
		return applyStateIgnored, version, err
	}
	p.rememberAppliedLocked(event.SourceID)
	return state, version, nil
}

func normalizeEventRecord(event Event) (database.DomainRecord, error) {
	if event.Operation == OperationPatch {
		return normalizePatchRecord(event.Record)
	}
	return normalizeRecord(event.Record)
}

func (p *partition) applyMutationLocked(registry *partitionRegistry, publisher *indexPublisher, key string, rec database.DomainRecord, event Event, version uint64) (applyState, error) {
	if event.Operation == "" || event.Operation == OperationUpsert {
		return p.upsertLocked(registry, publisher, key, rec, event.SourceID, version)
	}
	if event.Operation == OperationPatch {
		return p.patchLocked(registry, publisher, key, rec, event.SourceID, version)
	}
	if event.Operation == OperationDelete {
		return p.deleteLocked(registry, publisher, key, event.SourceID, version), nil
	}
	return applyStateIgnored, ErrInvalidEvent
}

func (p *partition) validateRecordScope(rec database.DomainRecord) error {
	if rec.Domain != p.spec.Domain || rec.Collection != p.spec.Collection {
		return fmt.Errorf("%w: event scope does not match projection", ErrInvalidEvent)
	}
	return nil
}

func (p *partition) effectiveVersion(version uint64) uint64 {
	if version > 0 {
		return version
	}
	return p.epoch.Load() + 1
}

func (p *partition) observeSourceWatermark(version uint64) {
	if version == 0 {
		return
	}
	for {
		current := p.watermark.Load()
		if version <= current {
			return
		}
		if p.watermark.CompareAndSwap(current, version) {
			return
		}
	}
}

func (p *partition) upsertLocked(registry *partitionRegistry, publisher *indexPublisher, key string, rec database.DomainRecord, source string, version uint64) (applyState, error) {
	if p.blockedByNewerTombstoneLocked(key, version) {
		return applyStateIgnored, nil
	}
	cell := p.recordCellLocked(registry, key)
	existingPtr := cell.ptr.Load()
	exists := existingPtr != nil
	var existing recordEntry
	if exists {
		existing = *existingPtr
	}
	if exists && version < existing.version {
		return applyStateIgnored, nil
	}
	recBytes := estimateRecordBytes(rec)
	nextBytes := p.bytes.Load() + recBytes
	if exists {
		nextBytes -= existing.bytes
	}
	if !exists && p.records.Load() >= int64(p.spec.MaxRecords) {
		return applyStateIgnored, ErrProjectionLimit
	}
	if nextBytes > p.spec.MaxBytes {
		return applyStateIgnored, ErrProjectionLimit
	}
	if exists {
		p.removeIndexesLocked(publisher, registry, key, existing.record)
	} else {
		delete(p.tombstones, key)
		p.records.Add(1)
	}
	entry := &recordEntry{
		record:  rec,
		source:  source,
		version: version,
		bytes:   recBytes,
	}
	cell.ptr.Store(entry)
	p.bytes.Store(nextBytes)
	p.addIndexesLocked(publisher, registry, key, rec, version)
	return applyStateApplied, nil
}

func (p *partition) patchLocked(registry *partitionRegistry, publisher *indexPublisher, key string, patch database.DomainRecord, source string, version uint64) (applyState, error) {
	if p.blockedByNewerTombstoneLocked(key, version) {
		return applyStateIgnored, nil
	}
	cell := p.recordCellLocked(registry, key)
	existingPtr := cell.ptr.Load()
	if existingPtr == nil {
		return applyStateIgnored, nil
	}
	existing := *existingPtr
	if version < existing.version {
		return applyStateIgnored, nil
	}
	next := existing.record
	next.Data = existing.record.Data.Merge(patch.Data)
	if len(patch.Vector) > 0 {
		next.Vector = append([]float32(nil), patch.Vector...)
	}
	if !patch.UpdatedAt.IsZero() {
		next.UpdatedAt = patch.UpdatedAt
	}
	if next.UpdatedAt.Before(existing.record.UpdatedAt) {
		next.UpdatedAt = existing.record.UpdatedAt
	}
	recBytes := estimateRecordBytes(next)
	nextBytes := p.bytes.Load() + recBytes - existing.bytes
	if nextBytes > p.spec.MaxBytes {
		return applyStateIgnored, ErrProjectionLimit
	}
	p.removeIndexesLocked(publisher, registry, key, existing.record)
	entry := &recordEntry{
		record:  next,
		source:  source,
		version: version,
		bytes:   recBytes,
	}
	cell.ptr.Store(entry)
	p.bytes.Store(nextBytes)
	p.addIndexesLocked(publisher, registry, key, next, version)
	return applyStateApplied, nil
}

func (p *partition) deleteLocked(registry *partitionRegistry, publisher *indexPublisher, key string, source string, version uint64) applyState {
	value, ok := registry.records.Load(key)
	var existing recordEntry
	var cell *recordCell
	if ok {
		cell = value.(*recordCell)
		if ptr := cell.ptr.Load(); ptr != nil {
			existing = *ptr
		} else {
			ok = false
		}
	}
	exists := ok
	if exists && version < existing.version {
		return applyStateIgnored
	}
	if !exists && p.blockedByNewerTombstoneLocked(key, version) {
		return applyStateIgnored
	}
	if exists {
		p.removeIndexesLocked(publisher, registry, key, existing.record)
		cell.ptr.Store(nil)
		p.records.Add(-1)
		p.bytes.Add(-existing.bytes)
	}
	p.rememberTombstoneLocked(key, source, version)
	return applyStateApplied
}

func (p *partition) blockedByNewerTombstoneLocked(key string, version uint64) bool {
	tomb, ok := p.tombstones[key]
	return ok && version < tomb.version
}

func (p *partition) alreadyAppliedLocked(source string) bool {
	if source == "" {
		return false
	}
	_, ok := p.applied[source]
	return ok
}

func (p *partition) rememberAppliedLocked(source string) {
	if source == "" {
		return
	}
	if _, exists := p.applied[source]; exists {
		return
	}
	p.applied[source] = struct{}{}
	p.applyOrder = append(p.applyOrder, source)
	for len(p.applyOrder) > p.spec.MaxAppliedEvents {
		oldest := p.applyOrder[0]
		p.applyOrder = p.applyOrder[1:]
		delete(p.applied, oldest)
	}
}

func (p *partition) rememberTombstoneLocked(key string, source string, version uint64) {
	if p.spec.MaxTombstones <= 0 {
		return
	}
	if _, exists := p.tombstones[key]; !exists {
		p.tombOrder = append(p.tombOrder, key)
	}
	p.tombstones[key] = tombstoneEntry{version: version, source: source}
	for len(p.tombOrder) > p.spec.MaxTombstones {
		oldest := p.tombOrder[0]
		p.tombOrder = p.tombOrder[1:]
		delete(p.tombstones, oldest)
	}
}
