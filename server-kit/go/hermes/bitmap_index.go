package hermes

import (
	"context"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

// slotMapletEntry represents an 8-byte entry storing a 32-bit fingerprint
// and a 32-bit slot index for single-cache-line slot hint resolution.
type slotMapletEntry struct {
	fp   uint32
	slot uint32
}

// slotMaplet is a compact, 2-choice cuckoo hash table mapping record keys to slot IDs.
// It acts as a single-cache-line slot accelerator verified against slotToKey.
type slotMaplet struct {
	entries []slotMapletEntry
	mask    uint32
	count   int
}

func newSlotMaplet(capacity int) slotMaplet {
	size := 16
	for size < capacity*2 {
		size <<= 1
	}
	return slotMaplet{
		entries: make([]slotMapletEntry, size),
		mask:    uint32(size - 1), // #nosec G115 -- power-of-two capacity bounded
	}
}

func (sm *slotMaplet) grow() {
	oldEntries := sm.entries
	newSize := max(len(oldEntries)*2, 32)
	sm.entries = make([]slotMapletEntry, newSize)
	sm.mask = uint32(newSize - 1) // #nosec G115 -- power-of-two capacity bounded
	sm.count = 0

	for _, e := range oldEntries {
		if e.fp != 0 {
			sm.reinsert(e)
		}
	}
}

func (sm *slotMaplet) reinsert(e slotMapletEntry) {
	b1 := e.fp & sm.mask
	if sm.entries[b1].fp == 0 {
		sm.entries[b1] = e
		sm.count++
		return
	}
	b2 := (e.fp ^ 0x5bd1e995) & sm.mask
	if sm.entries[b2].fp == 0 {
		sm.entries[b2] = e
		sm.count++
		return
	}
	sm.entries[b1] = e
}

func hashKey64(key string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h = (h ^ uint64(key[i])) * 1099511628211
	}
	return h
}

func (sm *slotMaplet) lookup(key string, slotToKey []string) (int, bool) {
	if sm == nil || len(sm.entries) == 0 {
		return -1, false
	}
	h := hashKey64(key)
	fp := uint32(h>>32) | 1

	b1 := uint32(h) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	e1 := sm.entries[b1]
	if e1.fp == fp {
		slot := int(e1.slot)
		if slot >= 0 && slot < len(slotToKey) && slotToKey[slot] == key {
			return slot, true
		}
	}

	b2 := (uint32(h) ^ 0x5bd1e995) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	e2 := sm.entries[b2]
	if e2.fp == fp {
		slot := int(e2.slot)
		if slot >= 0 && slot < len(slotToKey) && slotToKey[slot] == key {
			return slot, true
		}
	}

	return -1, false
}

func (sm *slotMaplet) insert(key string, slot int) {
	if sm == nil {
		return
	}
	if len(sm.entries) == 0 || sm.count*2 >= len(sm.entries) {
		sm.grow()
	}
	h := hashKey64(key)
	fp := uint32(h>>32) | 1

	b1 := uint32(h) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	if sm.entries[b1].fp == fp && int(sm.entries[b1].slot) == slot {
		return
	}
	b2 := (uint32(h) ^ 0x5bd1e995) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	if sm.entries[b2].fp == fp && int(sm.entries[b2].slot) == slot {
		return
	}

	if sm.entries[b1].fp == 0 {
		sm.entries[b1] = slotMapletEntry{fp: fp, slot: uint32(slot)} // #nosec G115 -- slot is positive index
		sm.count++
		return
	}
	if sm.entries[b2].fp == 0 {
		sm.entries[b2] = slotMapletEntry{fp: fp, slot: uint32(slot)} // #nosec G115 -- slot is positive index
		sm.count++
		return
	}

	sm.entries[b1] = slotMapletEntry{fp: fp, slot: uint32(slot)} // #nosec G115 -- slot is positive index
}

func (sm *slotMaplet) remove(key string, slot int) {
	if sm == nil || len(sm.entries) == 0 {
		return
	}
	h := hashKey64(key)
	fp := uint32(h>>32) | 1

	b1 := uint32(h) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	if sm.entries[b1].fp == fp && int(sm.entries[b1].slot) == slot {
		sm.entries[b1] = slotMapletEntry{}
		sm.count--
		return
	}
	b2 := (uint32(h) ^ 0x5bd1e995) & sm.mask // #nosec G115 -- lower 32-bit bucket index
	if sm.entries[b2].fp == fp && int(sm.entries[b2].slot) == slot {
		sm.entries[b2] = slotMapletEntry{}
		sm.count--
	}
}

// scopeBitmapIndex manages inverted bitmaps and slot IDs for a single tenant/partition scope.
//
// Memory is bounded two ways. Bitmap bytes are not charged to the projection
// byte budget, so the scope caps how many distinct indexed values it will hold
// and reports itself unsaturated-or-not so a query can tell "no such value" from
// "value not indexed". An entry that loses its last record is released outright,
// which keeps the working set proportional to live records rather than to every
// value the scope has ever seen.
type scopeBitmapIndex struct {
	mu sync.RWMutex

	// keyToSlot maps record key -> integer slot index (canonical durable truth).
	keyToSlot map[string]int

	// slotMaplet provides a single-cache-line O(1) slot hint accelerator.
	slotMaplet slotMaplet

	// slotToKey maps slot index -> record key.
	slotToKey []string

	// freeSlots holds recycled slot indexes for reuse.
	freeSlots []int

	// inverted: field index key -> bitmap
	inverted map[bitmapIndexKey]bitmap

	// saturated reports that maxValues distinct values are already indexed, so a
	// missing key means "not indexed" rather than "no records carry it".
	saturated bool
}

// bitmapBudgetNumerator / Denominator give the share of the projection byte
// budget that inverted bitmaps may occupy. Records keep the rest, because they
// are the thing the projection is for.
const (
	bitmapBudgetNumerator   = 1
	bitmapBudgetDenominator = 4

	// maxBitmapValuesCeiling bounds the derived cap so a tiny slot table with a
	// large byte budget does not resolve to an absurd value count.
	maxBitmapValuesCeiling = 1 << 20

	// minBitmapValuesFloor keeps a very large scope from resolving to a cap of
	// zero, which would disable the index entirely.
	minBitmapValuesFloor = 64
)

// bitmapValueCapFor bounds distinct indexed values for a scope holding slots
// records under a maxBytes budget.
//
// Every distinct value retains one bit per slot, so retained words are
// values x slots / 64. A flat value cap therefore costs proportionally more
// memory on a proportionally larger scope: the same cap over 10k slots retains
// about 5 MB, and over 50k slots about 25 MB. Deriving the cap from the byte
// budget instead keeps the retained footprint roughly constant across scopes.
//
// A low-cardinality field sits far below this bound at any scale, so the index
// keeps accelerating the enums and audience fields it was built for, while a
// per-record field (a UUID, a slug, a timestamp) stops paying for a full-width
// bitmap per value. Past the cap the scope reports itself saturated and queries
// fall back to the ordinary field index.
func bitmapValueCapFor(maxBytes int64, slots int) int {
	if slots < 1 {
		slots = 1
	}
	if maxBytes < 1 {
		maxBytes = defaultMaxBytes
	}
	bytesPerValue := int64((slots + 7) / 8)
	budget := maxBytes / bitmapBudgetDenominator * bitmapBudgetNumerator
	cap := int(budget / bytesPerValue)
	if cap < minBitmapValuesFloor {
		return minBitmapValuesFloor
	}
	if cap > maxBitmapValuesCeiling {
		return maxBitmapValuesCeiling
	}
	return cap
}

func newScopeBitmapIndex() *scopeBitmapIndex {
	return &scopeBitmapIndex{
		keyToSlot:  make(map[string]int),
		slotMaplet: newSlotMaplet(64),
		slotToKey:  make([]string, 0, 1024),
		freeSlots:  make([]int, 0, 128),
		inverted:   make(map[bitmapIndexKey]bitmap),
	}
}

// bitmapIndexKey identifies one indexed field value.
//
// It is a comparable struct rather than a joined string so that the key is
// injective by construction. A joined key needed escaping, because an indexed
// value carrying the separator could otherwise collide with a different field
// and kind: field "a:s" with kind 'b' and field "a" with kind 's' and value
// "b:" both rendered as "a:s:b:". Struct equality has no such ambiguity, and it
// allocates nothing on the query path.
type bitmapIndexKey struct {
	field string
	kind  byte
	value string
}

func newBitmapIndexKey(field string, kind byte, value string) bitmapIndexKey {
	return bitmapIndexKey{field: field, kind: kind, value: value}
}

// allocateSlotLocked assigns or reuses a slot for a record key.
// It probes slotMaplet first for a single-cache-line verified hit.
func (s *scopeBitmapIndex) allocateSlotLocked(key string) int {
	if slot, ok := s.slotMaplet.lookup(key, s.slotToKey); ok {
		return slot
	}
	if slot, ok := s.keyToSlot[key]; ok {
		s.slotMaplet.insert(key, slot)
		return slot
	}
	if len(s.freeSlots) > 0 {
		slot := s.freeSlots[len(s.freeSlots)-1]
		s.freeSlots = s.freeSlots[:len(s.freeSlots)-1]
		s.slotToKey[slot] = key
		s.keyToSlot[key] = slot
		s.slotMaplet.insert(key, slot)
		return slot
	}
	slot := len(s.slotToKey)
	s.slotToKey = append(s.slotToKey, key)
	s.keyToSlot[key] = slot
	s.slotMaplet.insert(key, slot)
	return slot
}

// releaseSlotLocked recycles a slot index.
func (s *scopeBitmapIndex) releaseSlotLocked(key string) (int, bool) {
	slot, ok := s.slotMaplet.lookup(key, s.slotToKey)
	if !ok {
		slot, ok = s.keyToSlot[key]
		if !ok {
			return -1, false
		}
	}
	delete(s.keyToSlot, key)
	s.slotMaplet.remove(key, slot)
	s.slotToKey[slot] = ""
	s.freeSlots = append(s.freeSlots, slot)
	return slot, true
}

// AddRecordLocked indexes secondary attributes into inverted bitmaps.
func (s *scopeBitmapIndex) AddRecordLocked(key string, rec database.DomainRecord, spec ProjectionSpec) {
	slot := s.allocateSlotLocked(key)
	capacity := len(s.slotToKey)
	valueCap := bitmapValueCapFor(spec.MaxBytes, capacity)

	forEachIndexedField(rec, spec, func(field string, kind byte, value string) {
		k := newBitmapIndexKey(field, kind, value)
		bm, ok := s.inverted[k]
		if !ok {
			if len(s.inverted) >= valueCap {
				// Past the cap. Stop indexing new values and let queries fall
				// back to the ordinary candidate index, which is slower but
				// exact. Keeping the entry would cost a bit per slot for a value
				// the byte budget never sees.
				s.saturated = true
				return
			}
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
		k := newBitmapIndexKey(field, kind, value)
		bm, exists := s.inverted[k]
		if !exists {
			return
		}
		bm.clear(slot)
		// Release a value that no record carries any more. Holding a full-size
		// bitmap per value ever seen is what made this structure grow with
		// lifetime volume instead of live volume.
		if bm.count() == 0 {
			delete(s.inverted, k)
			return
		}
		s.inverted[k] = bm
	})
}

// IntersectFilters returns candidate keys satisfying all query filters via bitwise AND.
//
// The second result reports whether the answer is authoritative. It is false
// when the scope is saturated and the query names a value that was never
// indexed, because there a missing bitmap means unknown, not no matches.
func (s *scopeBitmapIndex) IntersectFilters(filters []QueryFilter) (keys []string, coveredAll bool) {
	return s.IntersectFiltersWithContext(context.Background(), filters)
}

// IntersectFiltersWithContext returns candidate keys satisfying all query filters via bitwise AND.
// When an arena is attached to ctx, intermediate bitmap clone words and candidate keys slices
// are allocated from the frame scratch arena, avoiding heap allocations.
func (s *scopeBitmapIndex) IntersectFiltersWithContext(ctx context.Context, filters []QueryFilter) (keys []string, coveredAll bool) {
	if len(filters) == 0 {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	arena := scratch.FromContext(ctx)
	var result *bitmap
	var scratchResult bitmap

	for _, f := range filters {
		k := newBitmapIndexKey(f.Field, f.Kind, f.Value)
		bm, ok := s.inverted[k]
		if !ok {
			if s.saturated {
				// The value may simply not be indexed. Defer to the caller.
				return nil, false
			}
			// A filter with no matching bitmap means zero matches for an AND query.
			return nil, true
		}
		if result == nil {
			if arena != nil {
				if slice, err := scratch.AllocSlice[uint64](arena, len(bm.words)); err == nil {
					copy(slice, bm.words)
					scratchResult = newBitmapWithWords(slice, bm.n)
					result = &scratchResult
				}
			}
			if result == nil {
				cloned := bm.clone()
				result = &cloned
			}
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

	if arena != nil {
		if slice, err := scratch.AllocSlice[string](arena, matchingCount); err == nil {
			keys = slice[:0]
		}
	}
	if keys == nil {
		keys = make([]string, 0, matchingCount)
	}

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
	return r.QueryCompoundFiltersWithContext(context.Background(), scope, filters)
}

// QueryCompoundFiltersWithContext evaluates composite secondary attribute queries using bitwise AND,
// drawing intermediate allocations from any scratch.Arena attached to ctx.
func (r *BitmapIndexRegistry) QueryCompoundFiltersWithContext(ctx context.Context, scope recordScope, filters []QueryFilter) ([]string, bool) {
	sc := r.getScope(scope)
	return sc.IntersectFiltersWithContext(ctx, filters)
}
