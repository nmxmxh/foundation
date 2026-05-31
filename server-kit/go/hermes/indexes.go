package hermes

import (
	"context"
	"slices"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const maxIndexDeltaDepth = 4096

var emptyIndex = &indexSnapshot{adds: emptyRecordKeys}

type indexPublisher struct {
	changes map[*indexCell]*indexChange
}

type indexChange struct {
	adds    map[string]recordOrderEntry
	removes map[string]struct{}
	order   []recordOrderEntry
	delta   int
}

func newIndexPublisher() *indexPublisher {
	return &indexPublisher{changes: map[*indexCell]*indexChange{}}
}

func (p *partition) addIndexesLocked(publisher *indexPublisher, registry *partitionRegistry, key string, rec database.DomainRecord, version uint64) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	entry := recordOrderEntry{key: key, version: version}
	publisher.add(p.scopeCellLocked(registry, scope), entry)
	forEachIndexedField(rec, p.spec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		publisher.add(p.fieldCellLocked(registry, index), entry)
	})
}

func (p *partition) removeIndexesLocked(publisher *indexPublisher, registry *partitionRegistry, key string, rec database.DomainRecord) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	publisher.remove(p.scopeCellLocked(registry, scope), key)
	forEachIndexedField(rec, p.spec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		publisher.remove(p.fieldCellLocked(registry, index), key)
	})
}

func (p *partition) collectRecords(ctx context.Context, registry *partitionRegistry, query Query) ([]database.DomainRecord, error) {
	ordered := p.orderedCandidateIndex(registry, query)
	index := p.candidateIndex(registry, query)
	capacity := int(p.records.Load())
	if query.Limit > 0 && ordered.len() > 0 {
		capacity = query.Limit
	} else if index != nil {
		capacity = index.len()
	}
	if query.Limit > 0 && capacity > query.Limit {
		capacity = query.Limit
	}
	candidates := make([]database.DomainRecord, 0, capacity)
	return p.appendRecords(ctx, registry, query, candidates, ordered, index)
}

func (p *partition) appendRecords(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	candidates []database.DomainRecord,
	ordered *indexSnapshot,
	index *indexSnapshot,
) ([]database.DomainRecord, error) {
	if query.Limit > 0 && ordered.len() > 0 {
		return p.appendOrderedRecords(ctx, registry, query, candidates, ordered)
	}
	if index != nil {
		return p.appendIndexedRecords(ctx, registry, query, candidates, index)
	}
	return p.appendAllRecords(ctx, registry, query, candidates)
}

func (p *partition) appendOrderedRecords(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	candidates []database.DomainRecord,
	ordered *indexSnapshot,
) ([]database.DomainRecord, error) {
	var err error
	ordered.forEachOrderDesc(func(order recordOrderEntry) bool {
		if len(candidates) >= query.Limit {
			return false
		}
		entry, ok := p.recordForOrderEntry(registry, order)
		if !ok {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		if recordMatches(entry.record, p.spec, query) {
			candidates = append(candidates, entry.record)
		}
		return true
	})
	return candidates, err
}

func (p *partition) appendIndexedRecords(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	candidates []database.DomainRecord,
	index *indexSnapshot,
) ([]database.DomainRecord, error) {
	var err error
	index.forEachKey(func(key string) bool {
		entry, ok := p.recordEntry(registry, key)
		if !ok {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		if recordMatches(entry.record, p.spec, query) {
			candidates = appendListCandidate(candidates, entry.record, query.Limit)
		}
		return true
	})
	return candidates, err
}

func (p *partition) appendAllRecords(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	candidates []database.DomainRecord,
) ([]database.DomainRecord, error) {
	var err error
	registry.records.Range(func(_ any, value any) bool {
		entry, ok := recordEntryFromCell(value)
		if !ok || !recordMatches(entry.record, p.spec, query) {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		candidates = appendListCandidate(candidates, entry.record, query.Limit)
		return true
	})
	return candidates, err
}

func (p *partition) forEachViewSnapshot(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	epoch uint64,
	fn func(RecordView) error,
) (int, error) {
	ordered := p.orderedCandidateIndex(registry, query)
	if ordered.len() > 0 {
		return p.forEachOrderedView(ctx, registry, query, epoch, fn, ordered)
	}
	index := p.candidateIndex(registry, query)
	if index != nil {
		return p.forEachIndexedView(ctx, registry, query, epoch, fn, index)
	}
	return p.forEachAllView(ctx, registry, query, epoch, fn)
}

func (p *partition) forEachOrderedView(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	epoch uint64,
	fn func(RecordView) error,
	ordered *indexSnapshot,
) (int, error) {
	seen := 0
	var err error
	ordered.forEachOrderDesc(func(order recordOrderEntry) bool {
		entry, ok := p.recordForOrderEntry(registry, order)
		if !ok {
			return true
		}
		var done bool
		done, err = p.visitView(ctx, query, epoch, fn, entry, &seen)
		if err != nil || done {
			return false
		}
		return true
	})
	return seen, err
}

func (p *partition) forEachIndexedView(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	epoch uint64,
	fn func(RecordView) error,
	index *indexSnapshot,
) (int, error) {
	seen := 0
	var err error
	index.forEachKey(func(key string) bool {
		entry, ok := p.recordEntry(registry, key)
		if !ok {
			return true
		}
		var done bool
		done, err = p.visitView(ctx, query, epoch, fn, entry, &seen)
		if err != nil || done {
			return false
		}
		return true
	})
	return seen, err
}

func (p *partition) forEachAllView(
	ctx context.Context,
	registry *partitionRegistry,
	query Query,
	epoch uint64,
	fn func(RecordView) error,
) (int, error) {
	seen := 0
	var err error
	registry.records.Range(func(_ any, value any) bool {
		entry, ok := recordEntryFromCell(value)
		if !ok {
			return true
		}
		var done bool
		done, err = p.visitView(ctx, query, epoch, fn, entry, &seen)
		return err == nil && !done
	})
	return seen, err
}

func (p *partition) visitView(
	ctx context.Context,
	query Query,
	epoch uint64,
	fn func(RecordView) error,
	entry recordEntry,
	seen *int,
) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	if !recordMatches(entry.record, p.spec, query) {
		return false, nil
	}
	if query.Limit > 0 && *seen >= query.Limit {
		return true, nil
	}
	if err := fn(recordView(entry, epoch)); err != nil {
		return false, err
	}
	*seen++
	return false, nil
}

func (p *partition) orderedCandidateIndex(registry *partitionRegistry, query Query) *indexSnapshot {
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	var selected *indexSnapshot
	selectedCount := 0
	consider := func(snapshot *indexSnapshot) {
		if snapshot.len() == 0 {
			return
		}
		if selected == nil || snapshot.len() < selectedCount {
			selected = snapshot
			selectedCount = snapshot.len()
		}
	}
	consider(p.scopeSnapshot(registry, scope))
	for field, expected := range query.Filters {
		if !p.isIndexedField(field) {
			continue
		}
		kind, value, ok := indexableFieldValue(expected)
		if !ok {
			continue
		}
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		consider(p.fieldSnapshot(registry, index))
	}
	return selected
}

func (p *partition) candidateIndex(registry *partitionRegistry, query Query) *indexSnapshot {
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	selected := p.scopeSnapshot(registry, scope)
	for field, expected := range query.Filters {
		if !p.isIndexedField(field) {
			continue
		}
		kind, value, ok := indexableFieldValue(expected)
		if !ok {
			continue
		}
		index := p.fieldSnapshot(registry, fieldIndex{scope: scope, field: field, kind: kind, value: value})
		if selected == nil || index.len() < selected.len() {
			selected = index
		}
	}
	if selected == nil || selected.len() == 0 {
		return emptyIndex
	}
	return selected
}

func (p *partition) recordForOrderEntry(registry *partitionRegistry, entry recordOrderEntry) (recordEntry, bool) {
	rec, ok := p.recordEntry(registry, entry.key)
	if !ok || rec.version != entry.version {
		return recordEntry{}, false
	}
	return rec, true
}

func (p *partition) recordEntry(registry *partitionRegistry, key string) (recordEntry, bool) {
	value, ok := registry.records.Load(key)
	if !ok {
		return recordEntry{}, false
	}
	return recordEntryFromCell(value)
}

func recordEntryFromCell(value any) (recordEntry, bool) {
	cell, ok := value.(*recordCell)
	if !ok || cell == nil {
		return recordEntry{}, false
	}
	entry := cell.ptr.Load()
	if entry == nil {
		return recordEntry{}, false
	}
	return *entry, true
}

func (p *partition) recordCellLocked(registry *partitionRegistry, key string) *recordCell {
	if value, ok := registry.records.Load(key); ok {
		return value.(*recordCell)
	}
	cell := &recordCell{}
	value, _ := registry.records.LoadOrStore(key, cell)
	return value.(*recordCell)
}

func (p *partition) scopeCellLocked(registry *partitionRegistry, scope recordScope) *indexCell {
	if value, ok := registry.scopes.Load(scope); ok {
		return value.(*indexCell)
	}
	cell := newIndexCell()
	value, _ := registry.scopes.LoadOrStore(scope, cell)
	return value.(*indexCell)
}

func (p *partition) fieldCellLocked(registry *partitionRegistry, index fieldIndex) *indexCell {
	if value, ok := registry.fields.Load(index); ok {
		return value.(*indexCell)
	}
	cell := newIndexCell()
	value, _ := registry.fields.LoadOrStore(index, cell)
	return value.(*indexCell)
}

func newIndexCell() *indexCell {
	cell := &indexCell{}
	cell.ptr.Store(emptyIndex)
	return cell
}

func (p *partition) scopeSnapshot(registry *partitionRegistry, scope recordScope) *indexSnapshot {
	value, ok := registry.scopes.Load(scope)
	if !ok {
		return emptyIndex
	}
	return snapshotFromCell(value)
}

func (p *partition) fieldSnapshot(registry *partitionRegistry, index fieldIndex) *indexSnapshot {
	value, ok := registry.fields.Load(index)
	if !ok {
		return emptyIndex
	}
	return snapshotFromCell(value)
}

func snapshotFromCell(value any) *indexSnapshot {
	cell, ok := value.(*indexCell)
	if !ok || cell == nil {
		return emptyIndex
	}
	snapshot := cell.ptr.Load()
	if snapshot == nil {
		return emptyIndex
	}
	return snapshot
}

func (p *indexPublisher) add(cell *indexCell, entry recordOrderEntry) {
	change := p.change(cell)
	if _, removed := change.removes[entry.key]; removed {
		delete(change.removes, entry.key)
		change.delta++
	} else if _, exists := change.adds[entry.key]; !exists {
		change.delta++
	}
	change.adds[entry.key] = entry
	change.order = append(change.order, entry)
}

func (p *indexPublisher) remove(cell *indexCell, key string) {
	change := p.change(cell)
	if _, added := change.adds[key]; added {
		delete(change.adds, key)
		change.delta--
		return
	}
	if _, removed := change.removes[key]; !removed {
		change.removes[key] = struct{}{}
		change.delta--
	}
}

func (p *indexPublisher) publish() int {
	compactions := 0
	for cell, change := range p.changes {
		old := snapshotFromCell(cell)
		adds := make(map[string]struct{}, len(change.adds))
		order := make([]recordOrderEntry, 0, len(change.order))
		for key := range change.adds {
			adds[key] = struct{}{}
		}
		for _, entry := range change.order {
			if _, ok := change.adds[entry.key]; ok {
				order = append(order, entry)
			}
		}
		removes := cloneRemoveSet(change.removes)
		size := max(old.len()+change.delta, 0)
		next := &indexSnapshot{
			base:    old,
			adds:    adds,
			removes: removes,
			order:   order,
			size:    size,
			depth:   old.depth + 1,
		}
		if next.depth > maxIndexDeltaDepth {
			compactions++
		}
		cell.ptr.Store(compactIndexSnapshot(next))
	}
	return compactions
}

func (p *indexPublisher) change(cell *indexCell) *indexChange {
	change := p.changes[cell]
	if change != nil {
		return change
	}
	change = &indexChange{
		adds:    map[string]recordOrderEntry{},
		removes: map[string]struct{}{},
	}
	p.changes[cell] = change
	return change
}

func cloneRemoveSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func (s *indexSnapshot) len() int {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *indexSnapshot) contains(key string) bool {
	for current := s; current != nil; current = current.base {
		if _, removed := current.removes[key]; removed {
			return false
		}
		if _, added := current.adds[key]; added {
			return true
		}
	}
	return false
}

func (s *indexSnapshot) forEachKey(fn func(string) bool) {
	if s == nil || s.len() == 0 {
		return
	}
	seen := make(map[string]struct{}, s.len())
	for current := s; current != nil; current = current.base {
		for key := range current.removes {
			seen[key] = struct{}{}
		}
		for key := range current.adds {
			if _, done := seen[key]; done {
				continue
			}
			seen[key] = struct{}{}
			if s.contains(key) && !fn(key) {
				return
			}
		}
	}
}

func (s *indexSnapshot) forEachOrderDesc(fn func(recordOrderEntry) bool) {
	for current := s; current != nil; current = current.base {
		for i := len(current.order) - 1; i >= 0; i-- {
			if !fn(current.order[i]) {
				return
			}
		}
	}
}

func compactIndexSnapshot(snapshot *indexSnapshot) *indexSnapshot {
	if snapshot == nil || snapshot.depth <= maxIndexDeltaDepth {
		return snapshot
	}
	keys := compactKeys(snapshot)
	order := compactOrderEntries(snapshot, keys)
	return &indexSnapshot{adds: keys, order: order, size: len(keys)}
}

func compactKeys(snapshot *indexSnapshot) map[string]struct{} {
	states := map[string]bool{}
	seen := map[string]struct{}{}
	for current := snapshot; current != nil; current = current.base {
		for key := range current.removes {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			states[key] = false
		}
		for key := range current.adds {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			states[key] = true
		}
	}
	keys := make(map[string]struct{}, snapshot.len())
	for key, live := range states {
		if live {
			keys[key] = struct{}{}
		}
	}
	return keys
}

func compactOrderEntries(snapshot *indexSnapshot, keys map[string]struct{}) []recordOrderEntry {
	desc := make([]recordOrderEntry, 0, len(keys))
	seen := map[string]struct{}{}
	for current := snapshot; current != nil; current = current.base {
		for i := len(current.order) - 1; i >= 0; i-- {
			entry := current.order[i]
			if _, live := keys[entry.key]; !live {
				continue
			}
			if _, ok := seen[entry.key]; ok {
				continue
			}
			seen[entry.key] = struct{}{}
			desc = append(desc, entry)
		}
	}
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	return desc
}

func (p *partition) isIndexedField(field string) bool {
	return slices.Contains(p.spec.IndexedFields, field)
}

func forEachIndexedField(rec database.DomainRecord, spec ProjectionSpec, fn func(field string, kind byte, value string)) {
	for _, field := range spec.IndexedFields {
		value, ok := rec.Data[field]
		if !ok {
			continue
		}
		kind, indexValue, ok := indexableFieldValue(value)
		if ok {
			fn(field, kind, indexValue)
		}
	}
}

func recordView(entry recordEntry, epoch uint64) RecordView {
	rec := entry.record
	return RecordView{
		Domain:         rec.Domain,
		Collection:     rec.Collection,
		OrganizationID: rec.OrganizationID,
		RecordID:       rec.RecordID,
		Data:           rec.Data,
		Vector:         rec.Vector,
		CreatedAt:      rec.CreatedAt,
		UpdatedAt:      rec.UpdatedAt,
		Version:        entry.version,
		Epoch:          epoch,
	}
}

func estimateRecordBytes(rec database.DomainRecord) int64 {
	total := len(rec.Domain) + len(rec.Collection) + len(rec.OrganizationID) + len(rec.RecordID)
	total += len(rec.Vector) * 4
	for key, value := range rec.Data {
		total += len(key) + estimateValueBytes(value) + 32
	}
	return int64(total + 128)
}

func estimateValueBytes(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return len(typed)
	case []byte:
		return len(typed)
	case []float32:
		return len(typed) * 4
	case []float64:
		return len(typed) * 8
	case []string:
		total := 0
		for _, item := range typed {
			total += len(item)
		}
		return total
	case map[string]any:
		total := 0
		for key, item := range typed {
			total += len(key) + estimateValueBytes(item) + 16
		}
		return total
	case bool:
		return 1
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return 8
	case float32:
		return 4
	case float64:
		return 8
	default:
		return 64
	}
}
