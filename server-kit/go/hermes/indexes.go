package hermes

import (
	"slices"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const maxIndexDeltaDepth = 512

var emptyIndex = &indexSnapshot{adds: emptyRecordKeys}

type indexPublisher struct {
	changes      map[*indexCell]*indexChange
	rangeChanges map[*rangeIndexCell]*rangeIndexChange
}

type indexChange struct {
	adds    map[string]recordOrderEntry
	removes map[string]struct{}
	order   []recordOrderEntry
	delta   int
}

func newIndexPublisher() *indexPublisher {
	return &indexPublisher{
		changes:      map[*indexCell]*indexChange{},
		rangeChanges: map[*rangeIndexCell]*rangeIndexChange{},
	}
}

func (p *partition) addIndexesLocked(publisher *indexPublisher, registry *partitionRegistry, key string, rec database.DomainRecord, version uint64) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	entry := recordOrderEntry{key: key, version: version}
	publisher.add(p.scopeCellLocked(registry, scope), entry)
	forEachIndexedField(rec, p.spec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		publisher.add(p.fieldCellLocked(registry, index), entry)
	})
	p.forEachRangeIndexedValue(rec, func(index rangeIndex, rangeEntry rangeIndexEntry) {
		rangeEntry.key = key
		rangeEntry.version = version
		publisher.rangeAdd(p.rangeCellLocked(registry, index), rangeEntry)
	})
	if registry != nil && registry.bitmaps != nil {
		registry.bitmaps.Add(scope, key, rec, p.spec)
	}
}

func (p *partition) removeIndexesLocked(publisher *indexPublisher, registry *partitionRegistry, key string, rec database.DomainRecord) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	publisher.remove(p.scopeCellLocked(registry, scope), key)
	forEachIndexedField(rec, p.spec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		publisher.remove(p.fieldCellLocked(registry, index), key)
	})
	p.forEachRangeIndexedValue(rec, func(index rangeIndex, _ rangeIndexEntry) {
		publisher.rangeRemove(p.rangeCellLocked(registry, index), key)
	})
	if registry != nil && registry.bitmaps != nil {
		registry.bitmaps.Remove(scope, key, rec, p.spec)
	}
}

func (p *partition) bitmapCandidates(registry *partitionRegistry, query Query) ([]string, bool) {
	if registry == nil || registry.bitmaps == nil || query.Plan.count <= 1 {
		return nil, false
	}
	allIndexed := true
	filters := make([]QueryFilter, 0, query.Plan.count)
	forEachPlannedFilter(query.Plan, func(filter QueryFilter) bool {
		if !p.isIndexedField(filter.Field) {
			allIndexed = false
			return false
		}
		filters = append(filters, filter)
		return true
	})
	if !allIndexed || len(filters) == 0 {
		return nil, false
	}
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	return registry.bitmaps.QueryCompoundFilters(scope, filters)
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
		for key := range change.adds {
			adds[key] = struct{}{}
		}
		// The publisher is per-apply-batch and discarded after publish, so the
		// snapshot takes ownership of change.order (filtered in place) and
		// change.removes instead of allocating copies — the order re-slice and
		// the remove-set clone were hot allocations in the 2026-07-09
		// projection profile.
		kept := 0
		for _, entry := range change.order {
			if _, ok := change.adds[entry.key]; ok {
				change.order[kept] = entry
				kept++
			}
		}
		order := change.order[:kept]
		var removes map[string]struct{}
		if len(change.removes) > 0 {
			removes = change.removes
		}
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
	p.publishRanges()
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

func (s *indexSnapshot) len() int {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *indexSnapshot) forEachKey(fn func(string) bool) {
	if s == nil || s.len() == 0 {
		return
	}
	// A compact snapshot owns the complete live key set. Iterate it directly:
	// allocating a seen map proportional to the index made read-only counts and
	// candidate scans pay O(N) extra bytes even though there was no delta chain
	// to reconcile. emptyIndex may remain as a zero-sized base after a bulk load,
	// so treat that shape as compact too.
	if len(s.removes) == 0 && (s.base == nil || s.base.len() == 0) {
		for key := range s.adds {
			if !fn(key) {
				return
			}
		}
		return
	}
	// The chain always bottoms out in a flat snapshot: compaction produces one
	// with no base and no removes, and emptyIndex has the same shape. Only the
	// delta layers above it can restate or retract a key, so only they need a
	// dedup set — the flat base's own keys are unique by construction and can be
	// emitted with a lookup instead of an insert.
	//
	// Sizing that set to the whole index rather than to the changes was the
	// cost: one delta layer over a 100,000-key base allocated 3.5 MB on every
	// scan and ran 6.4x slower than the flat shape, and a 512-layer chain cost
	// barely more than a single layer. Nearly all of the read penalty was the
	// index-sized map, not the walking.
	terminal := s
	touched := 0
	for terminal.base != nil {
		touched += len(terminal.adds) + len(terminal.removes)
		terminal = terminal.base
	}
	// Two shapes reach here and they want opposite strategies. After a
	// compaction the terminal holds the whole live set and the layers above it
	// hold a handful of recent changes, so deduping against those changes is
	// far cheaper than against the index. After a bulk load the chain bottoms
	// out at emptyIndex instead, which means the bulk itself sits in a layer
	// *above* the terminal — dedup-against-changes would then absorb every key
	// in the index, and measured against the reconciling walk it was 14% slower
	// and allocated twice as much. Choosing on the measured split rather than
	// assuming the post-compaction shape is what keeps both cases fast.
	if len(terminal.removes) != 0 || touched >= terminal.len() {
		s.forEachKeyReconciled(fn)
		return
	}

	// Sized to the keys touched since the last compaction, not to the index,
	// and sized up front: growing it from empty re-hashed several times per
	// scan, which is most of what made the unsized form lose above.
	changed := make(map[string]struct{}, touched)
	for current := s; current != terminal; current = current.base {
		for key := range current.removes {
			changed[key] = struct{}{}
		}
		for key := range current.adds {
			if _, done := changed[key]; done {
				continue
			}
			changed[key] = struct{}{}
			if !fn(key) {
				return
			}
		}
	}
	for key := range terminal.adds {
		if _, done := changed[key]; done {
			continue
		}
		if !fn(key) {
			return
		}
	}
}

// forEachKeyReconciled is the general walk, kept for chains that do not bottom
// out in a flat snapshot. It dedups against every layer including the base, so
// it is correct for any shape and pays an index-sized set for the privilege.
func (s *indexSnapshot) forEachKeyReconciled(fn func(string) bool) {
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
			if !fn(key) {
				return
			}
		}
	}
}

func (s *indexSnapshot) forEachOrderDesc(fn func(recordOrderEntry) bool) {
	for current := s; current != nil; current = current.base {
		for _, v := range slices.Backward(current.order) {
			if !fn(v) {
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
	// Walk newest-to-oldest and keep the first verdict seen for each key, which
	// is the newest one. Presence in states *is* the seen-set: every key written
	// here is written exactly once, so a separate seen map tracked an identical
	// key set and doubled both the allocation and the hashing on what the
	// 2026-08-25 projection profile showed to be this package's largest single
	// allocator.
	//
	// Sized up front: compaction only runs once the delta chain is deep, so the
	// live count is a good estimate of the distinct keys about to be inserted
	// and growing the map from empty rehashed it several times per compaction.
	// Live keys accumulate straight into the result rather than into a verdict
	// map that is then filtered into a second one. The filtered form allocated
	// two maps the size of the whole index per compaction; this allocates one,
	// plus a tombstone set that is proportional to deletions rather than to the
	// index. Compaction walks every live key by construction, so the count of
	// map insertions is unchanged — what goes away is the second table and the
	// second pass over it.
	live := make(map[string]struct{}, snapshot.len())
	var dead map[string]struct{}
	seen := func(key string) bool {
		if _, ok := live[key]; ok {
			return true
		}
		_, ok := dead[key]
		return ok
	}
	for current := snapshot; current != nil; current = current.base {
		for key := range current.removes {
			if seen(key) {
				continue
			}
			if dead == nil {
				dead = make(map[string]struct{}, len(current.removes))
			}
			dead[key] = struct{}{}
		}
		for key := range current.adds {
			if seen(key) {
				continue
			}
			live[key] = struct{}{}
		}
	}
	return live
}

func compactOrderEntries(snapshot *indexSnapshot, keys map[string]struct{}) []recordOrderEntry {
	desc := make([]recordOrderEntry, 0, len(keys))
	seen := map[string]struct{}{}
	for current := snapshot; current != nil; current = current.base {
		for _, entry := range slices.Backward(current.order) {

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
