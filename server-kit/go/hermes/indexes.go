package hermes

import (
	"slices"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const maxIndexDeltaDepth = 512

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
	forEachPlannedFilter(query.Plan, func(filter QueryFilter) bool {
		if !p.isIndexedField(filter.Field) {
			return true
		}
		index := fieldIndex{scope: scope, field: filter.Field, kind: filter.Kind, value: filter.Value}
		consider(p.fieldSnapshot(registry, index))
		return true
	})
	if query.Plan.count > 0 {
		return selected
	}
	return selected
}

func (p *partition) candidateIndex(registry *partitionRegistry, query Query) *indexSnapshot {
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	selected := p.scopeSnapshot(registry, scope)
	forEachPlannedFilter(query.Plan, func(filter QueryFilter) bool {
		if !p.isIndexedField(filter.Field) {
			return true
		}
		index := p.fieldSnapshot(registry, fieldIndex{scope: scope, field: filter.Field, kind: filter.Kind, value: filter.Value})
		if selected == nil || index.len() < selected.len() {
			selected = index
		}
		return true
	})
	if query.Plan.count > 0 {
		if selected == nil || selected.len() == 0 {
			return emptyIndex
		}
		return selected
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
	entry, ok := recordEntryFromCell(value)
	if !ok {
		return recordEntry{}, false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		return recordEntry{}, false
	}
	return entry, true
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
		value, ok := rec.Data.Get(field)
		if !ok {
			continue
		}
		kind, indexValue, ok := value.ScalarIndex()
		if ok {
			fn(field, kind, indexValue)
		}
	}
}

func estimateRecordBytes(rec database.DomainRecord) int64 {
	total := len(rec.Domain) + len(rec.Collection) + len(rec.OrganizationID) + len(rec.RecordID)
	total += len(rec.Vector) * 4
	for _, field := range rec.Data {
		total += len(field.Name) + estimateValueBytes(field.Value) + 32
	}
	return int64(total + 128)
}

func estimateValueBytes(value any) int {
	switch typed := value.(type) {
	case database.RecordValue:
		if len(typed.Raw) > 0 {
			return len(typed.Raw)
		}
		return len(typed.Text)
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
