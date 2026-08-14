package hermes

import (
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// scopeBitmapIndex manages inverted bitmaps and slot IDs for a single tenant/partition scope.
type scopeBitmapIndex struct {
	mu sync.RWMutex

	// keyToSlot maps record key -> integer slot index.
	keyToSlot map[string]int

	// slotToKey maps slot index -> record key.
	slotToKey []string

	// freeSlots holds recycled slot indexes for reuse.
	freeSlots []int

	// inverted: fieldIndex -> bitmap
	inverted map[string]bitmap
}

func newScopeBitmapIndex() *scopeBitmapIndex {
	return &scopeBitmapIndex{
		keyToSlot: make(map[string]int),
		slotToKey: make([]string, 0, 1024),
		freeSlots: make([]int, 0, 128),
		inverted:  make(map[string]bitmap),
	}
}

func fieldIndexKey(field string, kind byte, value string) string {
	return field + ":" + string(kind) + ":" + value
}

// allocateSlotLocked assigns or reuses a slot for a record key.
func (s *scopeBitmapIndex) allocateSlotLocked(key string) int {
	if slot, ok := s.keyToSlot[key]; ok {
		return slot
	}
	if len(s.freeSlots) > 0 {
		slot := s.freeSlots[len(s.freeSlots)-1]
		s.freeSlots = s.freeSlots[:len(s.freeSlots)-1]
		s.slotToKey[slot] = key
		s.keyToSlot[key] = slot
		return slot
	}
	slot := len(s.slotToKey)
	s.slotToKey = append(s.slotToKey, key)
	s.keyToSlot[key] = slot
	return slot
}

// releaseSlotLocked recycles a slot index.
func (s *scopeBitmapIndex) releaseSlotLocked(key string) (int, bool) {
	slot, ok := s.keyToSlot[key]
	if !ok {
		return -1, false
	}
	delete(s.keyToSlot, key)
	s.slotToKey[slot] = ""
	s.freeSlots = append(s.freeSlots, slot)
	return slot, true
}

// AddRecordLocked indexes secondary attributes into inverted bitmaps.
func (s *scopeBitmapIndex) AddRecordLocked(key string, rec database.DomainRecord, spec ProjectionSpec) {
	slot := s.allocateSlotLocked(key)
	capacity := len(s.slotToKey)

	forEachIndexedField(rec, spec, func(field string, kind byte, value string) {
		k := fieldIndexKey(field, kind, value)
		bm, ok := s.inverted[k]
		if !ok {
			bm = newBitmap(capacity)
		}
		bm.set(slot)
		s.inverted[k] = bm
	})
}

// RemoveRecordLocked clears index bits for a record.
func (s *scopeBitmapIndex) RemoveRecordLocked(key string, rec database.DomainRecord, spec ProjectionSpec) {
	slot, ok := s.releaseSlotLocked(key)
	if !ok {
		return
	}

	forEachIndexedField(rec, spec, func(field string, kind byte, value string) {
		k := fieldIndexKey(field, kind, value)
		if bm, exists := s.inverted[k]; exists {
			bm.clear(slot)
			s.inverted[k] = bm
		}
	})
}

// IntersectFilters returns candidate keys satisfying all query filters via bitwise AND.
func (s *scopeBitmapIndex) IntersectFilters(filters []QueryFilter) (keys []string, coveredAll bool) {
	if len(filters) == 0 {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result *bitmap
	for _, f := range filters {
		k := fieldIndexKey(f.Field, f.Kind, f.Value)
		bm, ok := s.inverted[k]
		if !ok {
			// A filter with no matching bitmap means zero matches for an AND query.
			return nil, true
		}
		if result == nil {
			cloned := bm.clone()
			result = &cloned
		} else {
			result.andClamped(bm.words)
		}
	}

	if result == nil {
		return nil, false
	}

	matchingCount := result.count()
	if matchingCount == 0 {
		return nil, true
	}

	keys = make([]string, 0, matchingCount)
	result.forEachSet(func(slot int) bool {
		if slot >= 0 && slot < len(s.slotToKey) {
			k := s.slotToKey[slot]
			if k != "" {
				keys = append(keys, k)
			}
		}
		return true
	})

	return keys, true
}

// BitmapIndexRegistry manages bitmap indexes across tenant scopes.
type BitmapIndexRegistry struct {
	scopes sync.Map // map[recordScope]*scopeBitmapIndex
}

// NewBitmapIndexRegistry creates a new secondary attribute bitmap registry.
func NewBitmapIndexRegistry() *BitmapIndexRegistry {
	return &BitmapIndexRegistry{}
}

func (r *BitmapIndexRegistry) getScope(scope recordScope) *scopeBitmapIndex {
	if val, ok := r.scopes.Load(scope); ok {
		return val.(*scopeBitmapIndex)
	}
	created := newScopeBitmapIndex()
	actual, _ := r.scopes.LoadOrStore(scope, created)
	return actual.(*scopeBitmapIndex)
}

// Add updates inverted bitmaps for a record.
func (r *BitmapIndexRegistry) Add(scope recordScope, key string, rec database.DomainRecord, spec ProjectionSpec) {
	sc := r.getScope(scope)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.AddRecordLocked(key, rec, spec)
}

// Remove clears inverted bitmaps for a record.
func (r *BitmapIndexRegistry) Remove(scope recordScope, key string, rec database.DomainRecord, spec ProjectionSpec) {
	sc := r.getScope(scope)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.RemoveRecordLocked(key, rec, spec)
}

// QueryCompoundFilters evaluates composite secondary attribute queries using bitwise AND.
func (r *BitmapIndexRegistry) QueryCompoundFilters(scope recordScope, filters []QueryFilter) ([]string, bool) {
	sc := r.getScope(scope)
	return sc.IntersectFilters(filters)
}
