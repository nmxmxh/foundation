package hermes

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"sync/atomic"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const maxRangeIndexDeltaDepth = 32

type rangeIndexEntry struct {
	key      string
	version  uint64
	intVal   int64
	floatVal float64
}

type rangeIndexSnapshot struct {
	entries []rangeIndexEntry
	removes map[string]struct{}
	base    *rangeIndexSnapshot
	kind    byte
	size    int
	depth   int
}

type rangeIndexCell struct {
	ptr  atomic.Pointer[rangeIndexSnapshot]
	kind byte
}

type rangeIndexChange struct {
	adds    map[string]rangeIndexEntry
	removes map[string]struct{}
	delta   int
}

func newRangeIndexCell(kind byte) *rangeIndexCell {
	cell := &rangeIndexCell{kind: kind}
	cell.ptr.Store(&rangeIndexSnapshot{kind: kind})
	return cell
}

func (p *partition) isRangeIndexedField(field string) bool {
	return slices.Contains(p.spec.RangeIndexedFields, field)
}

func numericRangeValue(value database.RecordValue) (byte, int64, float64, bool) {
	switch value.Kind {
	case database.RecordValueInt, database.RecordValueUint:
		parsed, err := strconv.ParseInt(value.Text, 10, 64)
		return 'i', parsed, 0, err == nil
	case database.RecordValueFloat:
		parsed, err := strconv.ParseFloat(value.Text, 64)
		return 'f', 0, parsed, err == nil && !math.IsNaN(parsed)
	default:
		return 0, 0, 0, false
	}
}

func (p *partition) forEachRangeIndexedValue(rec database.DomainRecord, fn func(rangeIndex, rangeIndexEntry)) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	for _, field := range p.spec.RangeIndexedFields {
		value, ok := rec.Data.Get(field)
		if !ok {
			continue
		}
		kind, intVal, floatVal, ok := numericRangeValue(value)
		if ok {
			fn(rangeIndex{scope: scope, field: field, kind: kind}, rangeIndexEntry{intVal: intVal, floatVal: floatVal})
		}
	}
}

func (p *partition) rangeCellLocked(registry *partitionRegistry, index rangeIndex) *rangeIndexCell {
	if value, ok := registry.ranges.Load(index); ok {
		return value.(*rangeIndexCell)
	}
	cell := newRangeIndexCell(index.kind)
	value, _ := registry.ranges.LoadOrStore(index, cell)
	return value.(*rangeIndexCell)
}

func (p *partition) rangeSnapshot(registry *partitionRegistry, index rangeIndex) *rangeIndexSnapshot {
	value, ok := registry.ranges.Load(index)
	if !ok {
		return nil
	}
	cell, ok := value.(*rangeIndexCell)
	if !ok || cell == nil {
		return nil
	}
	return cell.ptr.Load()
}

func (publisher *indexPublisher) rangeChange(cell *rangeIndexCell) *rangeIndexChange {
	change := publisher.rangeChanges[cell]
	if change == nil {
		change = &rangeIndexChange{adds: map[string]rangeIndexEntry{}, removes: map[string]struct{}{}}
		publisher.rangeChanges[cell] = change
	}
	return change
}

func (publisher *indexPublisher) rangeAdd(cell *rangeIndexCell, entry rangeIndexEntry) {
	change := publisher.rangeChange(cell)
	if _, exists := change.adds[entry.key]; !exists {
		change.delta++
	}
	change.adds[entry.key] = entry
}

func (publisher *indexPublisher) rangeRemove(cell *rangeIndexCell, key string) {
	change := publisher.rangeChange(cell)
	if _, added := change.adds[key]; added {
		delete(change.adds, key)
		change.delta--
		if _, removed := change.removes[key]; !removed {
			// The key was introduced and removed inside this batch; retain a
			// tombstone for safety without changing the prior snapshot size.
			change.removes[key] = struct{}{}
		}
		return
	}
	if _, removed := change.removes[key]; !removed {
		change.removes[key] = struct{}{}
		change.delta--
	}
}

func (publisher *indexPublisher) publishRanges() {
	for cell, change := range publisher.rangeChanges {
		current := cell.ptr.Load()
		entries := make([]rangeIndexEntry, 0, len(change.adds))
		for _, entry := range change.adds {
			entries = append(entries, entry)
		}
		sortRangeEntries(entries, cell.kind)
		size, depth := len(entries), 1
		if current != nil {
			size = max(current.size+change.delta, 0)
			depth = current.depth + 1
		}
		next := &rangeIndexSnapshot{entries: entries, removes: change.removes, base: current, kind: cell.kind, size: size, depth: depth}
		if next.depth > maxRangeIndexDeltaDepth {
			next = compactRangeSnapshot(next)
		}
		cell.ptr.Store(next)
	}
}

func sortRangeEntries(entries []rangeIndexEntry, kind byte) {
	sort.Slice(entries, func(i, j int) bool {
		less, equal := rangeEntryCompare(kind, entries[i], entries[j])
		if !equal {
			return less
		}
		return entries[i].key < entries[j].key
	})
}

func rangeEntryCompare(kind byte, left, right rangeIndexEntry) (less, equal bool) {
	if kind == 'f' {
		return left.floatVal < right.floatVal, left.floatVal == right.floatVal
	}
	return left.intVal < right.intVal, left.intVal == right.intVal
}

func compactRangeSnapshot(snapshot *rangeIndexSnapshot) *rangeIndexSnapshot {
	live := make(map[string]rangeIndexEntry, snapshot.size)
	seen := make(map[string]struct{}, snapshot.size)
	for current := snapshot; current != nil; current = current.base {
		for _, entry := range current.entries {
			if _, done := seen[entry.key]; done {
				continue
			}
			seen[entry.key] = struct{}{}
			live[entry.key] = entry
		}
		for key := range current.removes {
			if _, done := seen[key]; !done {
				seen[key] = struct{}{}
			}
		}
	}
	entries := make([]rangeIndexEntry, 0, len(live))
	for _, entry := range live {
		entries = append(entries, entry)
	}
	sortRangeEntries(entries, snapshot.kind)
	return &rangeIndexSnapshot{entries: entries, kind: snapshot.kind, size: len(entries), depth: 1}
}

func (snapshot *rangeIndexSnapshot) bounds(op CompareOp, intOperand int64, floatOperand float64) (int, int, bool) {
	if snapshot == nil || op == CompareNe {
		return 0, 0, false
	}
	lower := sort.Search(len(snapshot.entries), func(i int) bool {
		if snapshot.kind == 'f' {
			return snapshot.entries[i].floatVal >= floatOperand
		}
		return snapshot.entries[i].intVal >= intOperand
	})
	upper := sort.Search(len(snapshot.entries), func(i int) bool {
		if snapshot.kind == 'f' {
			return snapshot.entries[i].floatVal > floatOperand
		}
		return snapshot.entries[i].intVal > intOperand
	})
	switch op {
	case CompareEq:
		return lower, upper, true
	case CompareLt:
		return 0, lower, true
	case CompareLe:
		return 0, upper, true
	case CompareGt:
		return upper, len(snapshot.entries), true
	case CompareGe:
		return lower, len(snapshot.entries), true
	default:
		return 0, 0, false
	}
}

func predicateRangeKindOperand(predicate ColumnPredicate) (byte, int64, float64, bool) {
	switch predicate.kind {
	case predicateInt64:
		return 'i', predicate.int64Operand, 0, true
	case predicateFloat64:
		return 'f', 0, predicate.floatOperand, !math.IsNaN(predicate.floatOperand)
	default:
		return 0, 0, 0, false
	}
}

type rangeCandidatePlan struct {
	snapshot     *rangeIndexSnapshot
	predicate    ColumnPredicate
	intOperand   int64
	floatOperand float64
	estimate     int
}

func (p *partition) bestRangeCandidatePlan(registry *partitionRegistry, query Query, predicates []ColumnPredicate) (rangeCandidatePlan, bool) {
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	var best rangeCandidatePlan
	found := false
	for _, predicate := range predicates {
		if !p.isRangeIndexedField(predicate.Field) {
			continue
		}
		kind, intOperand, floatOperand, ok := predicateRangeKindOperand(predicate)
		if !ok {
			continue
		}
		snapshot := p.rangeSnapshot(registry, rangeIndex{scope: scope, field: predicate.Field, kind: kind})
		estimate, ok := snapshot.estimate(predicate.Op, intOperand, floatOperand)
		if !ok {
			continue
		}
		if !found || estimate < best.estimate {
			best = rangeCandidatePlan{snapshot: snapshot, predicate: predicate, intOperand: intOperand, floatOperand: floatOperand, estimate: estimate}
			found = true
		}
	}
	return best, found
}

func (snapshot *rangeIndexSnapshot) estimate(op CompareOp, intOperand int64, floatOperand float64) (int, bool) {
	total := 0
	for current := snapshot; current != nil; current = current.base {
		start, end, ok := current.bounds(op, intOperand, floatOperand)
		if !ok {
			return 0, false
		}
		total += end - start
	}
	return total, true
}

func (plan rangeCandidatePlan) forEach(fn func(rangeIndexEntry) bool) {
	if plan.snapshot != nil && (plan.snapshot.base == nil || plan.snapshot.base.size == 0) && len(plan.snapshot.removes) == 0 {
		start, end, _ := plan.snapshot.bounds(plan.predicate.Op, plan.intOperand, plan.floatOperand)
		for i := start; i < end; i++ {
			if !fn(plan.snapshot.entries[i]) {
				return
			}
		}
		return
	}
	seen := make(map[string]struct{}, plan.estimate)
	for current := plan.snapshot; current != nil; current = current.base {
		start, end, _ := current.bounds(plan.predicate.Op, plan.intOperand, plan.floatOperand)
		for i := start; i < end; i++ {
			entry := current.entries[i]
			if _, done := seen[entry.key]; done {
				continue
			}
			seen[entry.key] = struct{}{}
			if !fn(entry) {
				return
			}
		}
		for key := range current.removes {
			if _, done := seen[key]; !done {
				seen[key] = struct{}{}
			}
		}
	}
}

func (registry *partitionRegistry) rangeIndexStats() (entries int, bytes int64) {
	registry.ranges.Range(func(_, value any) bool {
		cell, ok := value.(*rangeIndexCell)
		if !ok || cell == nil {
			return true
		}
		snapshot := cell.ptr.Load()
		if snapshot == nil {
			return true
		}
		entries += snapshot.size
		seen := map[string]struct{}{}
		for current := snapshot; current != nil; current = current.base {
			for _, entry := range current.entries {
				if _, done := seen[entry.key]; done {
					continue
				}
				seen[entry.key] = struct{}{}
				bytes += int64(len(entry.key) + 32)
			}
			for key := range current.removes {
				if _, done := seen[key]; !done {
					seen[key] = struct{}{}
				}
			}
		}
		return true
	})
	return entries, bytes
}
