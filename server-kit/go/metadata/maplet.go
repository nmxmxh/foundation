package metadata

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Classification codes forming a security lattice:
// ClassificationPublic <= ClassificationInternal <= ClassificationConfidential <= ClassificationRestricted.
const (
	ClassificationUnspecified  uint8 = 0
	ClassificationPublic       uint8 = 1
	ClassificationInternal     uint8 = 2
	ClassificationConfidential uint8 = 3
	ClassificationRestricted   uint8 = 4
)

// Retention codes forming a retention lifetime lattice:
// RetentionEphemeral <= Retention30Days <= Retention1Year <= Retention7Years <= RetentionPermanent.
const (
	RetentionUnspecified uint8 = 0
	RetentionEphemeral   uint8 = 1
	Retention30Days      uint8 = 2
	Retention1Year       uint8 = 3
	Retention7Years      uint8 = 4
	RetentionPermanent   uint8 = 5
)

// EncodeClassification maps string classification to its 4-bit lattice code.
func EncodeClassification(c string) uint8 {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "public":
		return ClassificationPublic
	case "internal":
		return ClassificationInternal
	case "confidential":
		return ClassificationConfidential
	case "restricted":
		return ClassificationRestricted
	default:
		return ClassificationRestricted // fail-closed default
	}
}

// EncodeRetention maps string retention class to its 4-bit lattice code.
func EncodeRetention(r string) uint8 {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "ephemeral", "transient":
		return RetentionEphemeral
	case "30d", "30_days", "30days":
		return Retention30Days
	case "1y", "1_year", "1year", "365d":
		return Retention1Year
	case "7y", "7_years", "7years":
		return Retention7Years
	case "permanent", "indefinite":
		return RetentionPermanent
	default:
		return RetentionPermanent // fail-closed default
	}
}

// ProjectMetadataClassification extracts the 4-bit classification code from EnvelopeMetadata.
func ProjectMetadataClassification(m *EnvelopeMetadata) uint8 {
	if m == nil || m.Attributes == nil {
		return ClassificationRestricted
	}
	if class, ok := m.Attributes["data_classification"]; ok {
		return EncodeClassification(class)
	}
	return ClassificationRestricted
}

// ProjectMetadataRetention extracts the 4-bit retention code from EnvelopeMetadata.
func ProjectMetadataRetention(m *EnvelopeMetadata) uint8 {
	if m == nil || m.Attributes == nil {
		return RetentionPermanent
	}
	if ret, ok := m.Attributes["retention_class"]; ok {
		return EncodeRetention(ret)
	}
	return RetentionPermanent
}

// ---------------------------------------------------------------------------
// LatticeMaplet: Approximate Key-Value Mapping over a Join-Semilattice
// ---------------------------------------------------------------------------

// LatticeMaplet is a space-efficient approximate key-value mapping structure
// governed by an application-specific join-semilattice (V, <=, \lor).
//
// Invariant (One-Sided Error):
// For any key k, Lookup(k) returns an upper bound m[k] such that M[k] <= m[k].
// Internal fingerprint collisions between two inserted keys resolve via the
// lattice join: StoredVal = StoredVal \lor NewVal = max(StoredVal, NewVal).
//
// Memory Layout:
// Contiguous uint16 slab formatted into 4-slot buckets.
// Each 16-bit slot holds:
// - Fingerprint: 12 bits
// - Micro-value: 4 bits (values 0..15)
// Total space: ~18 bits per key at 85% load factor.
type LatticeMaplet struct {
	numBuckets uint32
	mask       uint32
	count      uint32
	maxCount   uint32
	buckets    []uint16
}

const (
	latticeSlotsPerBucket  = 4
	latticeFingerprintBits = 12
	latticeValueBits       = 4
	latticeMaxFingerprint  = (uint16(1) << latticeFingerprintBits) - 1
	latticeMaxValue        = (uint8(1) << latticeValueBits) - 1
	latticeMaxCuckooKicks  = 500
)

// NewLatticeMaplet initializes a LatticeMaplet with capacity for expectedKeys.
func NewLatticeMaplet(expectedKeys int) *LatticeMaplet {
	minSlots := float64(expectedKeys) / 0.85
	numBuckets := uint32(1)
	for float64(numBuckets*latticeSlotsPerBucket) < minSlots {
		numBuckets <<= 1
	}
	if numBuckets < 8 {
		numBuckets = 8
	}

	return &LatticeMaplet{
		numBuckets: numBuckets,
		mask:       numBuckets - 1,
		maxCount:   uint32(float64(numBuckets*latticeSlotsPerBucket) * 0.90),
		buckets:    make([]uint16, numBuckets*latticeSlotsPerBucket),
	}
}

func mapletHash64(key string) uint64 {
	var h uint64 = 0xcbf29ce484222325
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 0x100000001b3
	}
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}

func (m *LatticeMaplet) decompose(h uint64) (i1 uint32, fp uint16, i2 uint32) {
	i1 = uint32(h) & m.mask // #nosec G115 -- lower 32 bits used as bucket index
	fpRaw := uint16((h >> 32) & uint64(latticeMaxFingerprint))
	if fpRaw == 0 {
		fpRaw = 1
	}
	fp = fpRaw

	fpHash := uint32(hash16(fp))
	i2 = (i1 ^ fpHash) & m.mask
	return i1, fp, i2
}

func hash16(v uint16) uint32 {
	h := uint32(v) * 0x517cc1b7
	h ^= h >> 16
	return h
}

func packLatticeSlot(fp uint16, val uint8) uint16 {
	return (fp << latticeValueBits) | uint16(val&latticeMaxValue)
}

func unpackLatticeSlot(slot uint16) (fp uint16, val uint8) {
	return slot >> latticeValueBits, uint8(slot & uint16(latticeMaxValue))
}

// cuckooMaxKickPath bounds the displacement path that cuckooDisplace can undo.
const cuckooMaxKickPath = 512

// cuckooDisplace moves entry into the table by cuckoo displacement.
// It records each step and undoes all of them on failure. A failed insert then
// leaves every stored key in place. The old loop dropped the last victim, which
// removed an earlier key and broke the "a stored key is always found" contract.
// The kick slot comes from a xorshift sequence, so the walk does not cycle on slot 0.
func cuckooDisplace[S uint16 | uint32](
	buckets []S,
	slotsPerBucket, mask, start uint32,
	entry S,
	maxKicks int,
	seed uint64,
	altHash func(S) uint32,
	store func(idx uint32, v S),
) bool {
	maxKicks = min(maxKicks, cuckooMaxKickPath)
	var targets [cuckooMaxKickPath]uint32
	var victims [cuckooMaxKickPath]S
	rng := seed | 1
	currBucket := start
	currEntry := entry
	for kick := range maxKicks {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		targetIdx := currBucket*slotsPerBucket + uint32(rng%uint64(slotsPerBucket)) // #nosec G115 -- bounded by slotsPerBucket
		victim := buckets[targetIdx]
		targets[kick] = targetIdx
		victims[kick] = victim
		store(targetIdx, currEntry)

		currBucket = (currBucket ^ altHash(victim)) & mask
		currEntry = victim
		base := currBucket * slotsPerBucket
		for s := range slotsPerBucket {
			if buckets[base+s] == 0 {
				store(base+s, currEntry)
				return true
			}
		}
	}
	// Undo in reverse order. Each step restores the victim it displaced.
	for kick := maxKicks - 1; kick >= 0; kick-- {
		store(targets[kick], victims[kick])
	}
	return false
}

// latticeJoin computes the least upper bound (join) of two values.
// In the classification and retention lattices, higher is more restrictive / longer.
func latticeJoin(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

// Insert adds or joins key with value val (0..15) in the LatticeMaplet.
func (m *LatticeMaplet) Insert(key string, val uint8) error {
	if val > latticeMaxValue {
		return fmt.Errorf("metadata: value %d exceeds max 4-bit value %d", val, latticeMaxValue)
	}

	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)

	// Check if matching fingerprint already exists in i1 or i2.
	// If yes, resolve via lattice join (least upper bound).
	base1 := i1 * latticeSlotsPerBucket
	for s := range uint32(latticeSlotsPerBucket) {
		slot := m.buckets[base1+s]
		if slot != 0 && (slot>>latticeValueBits) == fp {
			_, existingVal := unpackLatticeSlot(slot)
			joined := latticeJoin(existingVal, val)
			m.buckets[base1+s] = packLatticeSlot(fp, joined)
			return nil
		}
	}
	base2 := i2 * latticeSlotsPerBucket
	for s := range uint32(latticeSlotsPerBucket) {
		slot := m.buckets[base2+s]
		if slot != 0 && (slot>>latticeValueBits) == fp {
			_, existingVal := unpackLatticeSlot(slot)
			joined := latticeJoin(existingVal, val)
			m.buckets[base2+s] = packLatticeSlot(fp, joined)
			return nil
		}
	}

	if m.count >= m.maxCount {
		return errors.New("metadata: maplet capacity reached")
	}

	slotEntry := packLatticeSlot(fp, val)

	// Insert into empty slot in i1
	for s := range uint32(latticeSlotsPerBucket) {
		if m.buckets[base1+s] == 0 {
			m.buckets[base1+s] = slotEntry
			m.count++
			return nil
		}
	}

	// Insert into empty slot in i2
	for s := range uint32(latticeSlotsPerBucket) {
		if m.buckets[base2+s] == 0 {
			m.buckets[base2+s] = slotEntry
			m.count++
			return nil
		}
	}

	if cuckooDisplace(m.buckets, latticeSlotsPerBucket, m.mask, i1, slotEntry, latticeMaxCuckooKicks, h,
		func(slot uint16) uint32 { fp, _ := unpackLatticeSlot(slot); return hash16(fp) },
		func(idx uint32, v uint16) { m.buckets[idx] = v }) {
		m.count++
		return nil
	}
	return errors.New("metadata: maplet insertion failed: cuckoo cycle detected (table unchanged)")
}

// Lookup queries the LatticeMaplet for key.
// Returns (value, true) if found, or (0, false) if absent.
// For a stored key, the returned value is >= the true value under the lattice.
// CAUTION: an absent key can match a stored fingerprint (about 0.2% at 12 bits)
// and return an arbitrary value, including the least restrictive one.
func (m *LatticeMaplet) Lookup(key string) (uint8, bool) {
	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)

	base1 := i1 * latticeSlotsPerBucket
	for s := range uint32(latticeSlotsPerBucket) {
		slot := m.buckets[base1+s]
		if slot != 0 && (slot>>latticeValueBits) == fp {
			return uint8(slot & uint16(latticeMaxValue)), true
		}
	}

	base2 := i2 * latticeSlotsPerBucket
	for s := range uint32(latticeSlotsPerBucket) {
		slot := m.buckets[base2+s]
		if slot != 0 && (slot>>latticeValueBits) == fp {
			return uint8(slot & uint16(latticeMaxValue)), true
		}
	}

	return 0, false
}

// MemoryBytes returns the memory footprint of the maplet table in bytes.
func (m *LatticeMaplet) MemoryBytes() int {
	return len(m.buckets) * 2
}

// BitsPerKey returns the effective space cost per inserted key in bits.
func (m *LatticeMaplet) BitsPerKey() float64 {
	if m.count == 0 {
		return 0
	}
	return float64(len(m.buckets)*16) / float64(m.count)
}

// Count returns the number of unique fingerprints stored.
func (m *LatticeMaplet) Count() int {
	return int(m.count)
}

// ---------------------------------------------------------------------------
// WideMaplet: 32-bit Micro-Value Maplet (approximate; see CAUTION below)
// ---------------------------------------------------------------------------

// WideMaplet packs a 28-bit fingerprint and a 4-bit value into a 32-bit slot.
// Sized for unordered micro-values (Hermes slot tags, chunk offsets).
//
// CAUTION: two stored keys alias when they share a bucket pair and a fingerprint.
// Then Insert overwrites the other key's value. At N = 100,000 the chance of at
// least one alias is about 1e-3, not 1e-8. Absent keys can also match.
// Never use a maplet to decide authorization, rate limits, routing, or dedup.
type WideMaplet struct {
	numBuckets uint32
	mask       uint32
	count      uint32
	maxCount   uint32
	buckets    []uint32
}

const (
	wideSlotsPerBucket  = 4
	wideFingerprintBits = 28
	wideValueBits       = 4
	wideMaxFingerprint  = (uint32(1) << wideFingerprintBits) - 1
	wideMaxValue        = (uint8(1) << wideValueBits) - 1
	wideMaxCuckooKicks  = 500
)

// NewWideMaplet initializes a WideMaplet for expectedKeys.
func NewWideMaplet(expectedKeys int) *WideMaplet {
	minSlots := float64(expectedKeys) / 0.85
	numBuckets := uint32(1)
	for float64(numBuckets*wideSlotsPerBucket) < minSlots {
		numBuckets <<= 1
	}
	if numBuckets < 8 {
		numBuckets = 8
	}

	return &WideMaplet{
		numBuckets: numBuckets,
		mask:       numBuckets - 1,
		maxCount:   uint32(float64(numBuckets*wideSlotsPerBucket) * 0.90),
		buckets:    make([]uint32, numBuckets*wideSlotsPerBucket),
	}
}

func (m *WideMaplet) decompose(h uint64) (i1 uint32, fp uint32, i2 uint32) {
	i1 = uint32(h) & m.mask // #nosec G115 -- lower 32 bits used as bucket index
	fpRaw := uint32((h >> 32) & uint64(wideMaxFingerprint))
	if fpRaw == 0 {
		fpRaw = 1
	}
	fp = fpRaw

	fpHash := uint32(hash32Val(fp))
	i2 = (i1 ^ fpHash) & m.mask
	return i1, fp, i2
}

func hash32Val(v uint32) uint32 {
	v ^= v >> 16
	v *= 0x7feb352d
	v ^= v >> 15
	v *= 0x846ca68b
	v ^= v >> 16
	return v
}

func packWideSlot(fp uint32, val uint8) uint32 {
	return (fp << wideValueBits) | uint32(val&wideMaxValue)
}

func unpackWideSlot(slot uint32) (fp uint32, val uint8) {
	return slot >> wideValueBits, uint8(slot & uint32(wideMaxValue))
}

// Insert places key with value val in the WideMaplet.
func (m *WideMaplet) Insert(key string, val uint8) error {
	if val > wideMaxValue {
		return fmt.Errorf("metadata: value %d exceeds max 4-bit value %d", val, wideMaxValue)
	}

	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)
	slotEntry := packWideSlot(fp, val)

	base1 := i1 * wideSlotsPerBucket
	for s := range uint32(wideSlotsPerBucket) {
		if m.buckets[base1+s] != 0 && (m.buckets[base1+s]>>wideValueBits) == fp {
			m.buckets[base1+s] = slotEntry
			return nil
		}
	}
	base2 := i2 * wideSlotsPerBucket
	for s := range uint32(wideSlotsPerBucket) {
		if m.buckets[base2+s] != 0 && (m.buckets[base2+s]>>wideValueBits) == fp {
			m.buckets[base2+s] = slotEntry
			return nil
		}
	}

	if m.count >= m.maxCount {
		return errors.New("metadata: wide maplet capacity reached")
	}

	for s := range uint32(wideSlotsPerBucket) {
		if m.buckets[base1+s] == 0 {
			m.buckets[base1+s] = slotEntry
			m.count++
			return nil
		}
	}
	for s := range uint32(wideSlotsPerBucket) {
		if m.buckets[base2+s] == 0 {
			m.buckets[base2+s] = slotEntry
			m.count++
			return nil
		}
	}

	if cuckooDisplace(m.buckets, wideSlotsPerBucket, m.mask, i1, slotEntry, wideMaxCuckooKicks, h,
		func(slot uint32) uint32 { fp, _ := unpackWideSlot(slot); return hash32Val(fp) },
		func(idx uint32, v uint32) { m.buckets[idx] = v }) {
		m.count++
		return nil
	}
	return errors.New("metadata: wide maplet insertion failed: cuckoo cycle detected (table unchanged)")
}

// Lookup queries the WideMaplet for key.
func (m *WideMaplet) Lookup(key string) (uint8, bool) {
	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)

	base1 := i1 * wideSlotsPerBucket
	for s := range uint32(wideSlotsPerBucket) {
		slot := m.buckets[base1+s]
		if slot != 0 && (slot>>wideValueBits) == fp {
			return uint8(slot & uint32(wideMaxValue)), true
		}
	}

	base2 := i2 * wideSlotsPerBucket
	for s := range uint32(wideSlotsPerBucket) {
		slot := m.buckets[base2+s]
		if slot != 0 && (slot>>wideValueBits) == fp {
			return uint8(slot & uint32(wideMaxValue)), true
		}
	}

	return 0, false
}

// MemoryBytes returns the raw memory size in bytes.
func (m *WideMaplet) MemoryBytes() int {
	return len(m.buckets) * 4
}

// BitsPerKey returns the effective space cost per key in bits.
func (m *WideMaplet) BitsPerKey() float64 {
	if m.count == 0 {
		return 0
	}
	return float64(len(m.buckets)*32) / float64(m.count)
}

// Count returns the number of keys stored.
func (m *WideMaplet) Count() int {
	return int(m.count)
}

// ---------------------------------------------------------------------------
// CountingMaplet: Approximate Frequency Tracking with Saturating Counters
// ---------------------------------------------------------------------------

// CountingMaplet provides space-efficient frequency counting (inspired by Squeakr).
// It tracks request rates and key occurrences with bounded one-sided over-estimation.
// Slot layout (uint32):
// - Fingerprint: 24 bits
// - Saturating counter: 8 bits (0..255)
// Space: 4 bytes per slot.
type CountingMaplet struct {
	mu         sync.Mutex
	numBuckets uint32
	mask       uint32
	count      uint32
	maxCount   uint32
	buckets    []uint32
}

const (
	counterSlotsPerBucket  = 4
	counterFingerprintBits = 24
	counterMaxFingerprint  = (uint32(1) << counterFingerprintBits) - 1
	counterMaxKicks        = 500
	counterMaxCount        = 255
)

// NewCountingMaplet creates a CountingMaplet sized for expectedKeys.
func NewCountingMaplet(expectedKeys int) *CountingMaplet {
	minSlots := float64(expectedKeys) / 0.85
	numBuckets := uint32(1)
	for float64(numBuckets*counterSlotsPerBucket) < minSlots {
		numBuckets <<= 1
	}
	if numBuckets < 8 {
		numBuckets = 8
	}

	return &CountingMaplet{
		numBuckets: numBuckets,
		mask:       numBuckets - 1,
		maxCount:   uint32(float64(numBuckets*counterSlotsPerBucket) * 0.90),
		buckets:    make([]uint32, numBuckets*counterSlotsPerBucket),
	}
}

func (m *CountingMaplet) decompose(h uint64) (i1 uint32, fp uint32, i2 uint32) {
	i1 = uint32(h) & m.mask // #nosec G115 -- lower 32 bits used as bucket index
	fpRaw := uint32((h >> 32) & uint64(counterMaxFingerprint))
	if fpRaw == 0 {
		fpRaw = 1
	}
	fp = fpRaw

	fpHash := uint32(hash32Val(fp))
	i2 = (i1 ^ fpHash) & m.mask
	return i1, fp, i2
}

func packCounterSlot(fp uint32, val uint8) uint32 {
	return (fp << 8) | uint32(val)
}

func unpackCounterSlot(slot uint32) (fp uint32, val uint8) {
	return slot >> 8, uint8(slot & 0xFF)
}

// Increment adds 1 to key's counter, saturating at 255, and returns the new count.
// Zero heap allocations.
func (m *CountingMaplet) Increment(key string) uint8 {
	m.mu.Lock()
	defer m.mu.Unlock()

	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)

	// Check if already present in i1
	base1 := i1 * counterSlotsPerBucket
	for s := range uint32(counterSlotsPerBucket) {
		slot := m.buckets[base1+s]
		if slot != 0 && (slot>>8) == fp {
			_, curr := unpackCounterSlot(slot)
			if curr < counterMaxCount {
				curr++
			}
			atomic.StoreUint32(&m.buckets[base1+s], packCounterSlot(fp, curr))
			return curr
		}
	}

	// Check if already present in i2
	base2 := i2 * counterSlotsPerBucket
	for s := range uint32(counterSlotsPerBucket) {
		slot := m.buckets[base2+s]
		if slot != 0 && (slot>>8) == fp {
			_, curr := unpackCounterSlot(slot)
			if curr < counterMaxCount {
				curr++
			}
			atomic.StoreUint32(&m.buckets[base2+s], packCounterSlot(fp, curr))
			return curr
		}
	}

	// Not found: insert with initial count 1
	slotEntry := packCounterSlot(fp, 1)

	for s := range uint32(counterSlotsPerBucket) {
		if m.buckets[base1+s] == 0 {
			atomic.StoreUint32(&m.buckets[base1+s], slotEntry)
			m.count++
			return 1
		}
	}
	for s := range uint32(counterSlotsPerBucket) {
		if m.buckets[base2+s] == 0 {
			atomic.StoreUint32(&m.buckets[base2+s], slotEntry)
			m.count++
			return 1
		}
	}

	// Cuckoo displacement if both full. A key in flight can read as 0 from GetCount.
	if m.count < m.maxCount && cuckooDisplace(m.buckets, counterSlotsPerBucket, m.mask, i1, slotEntry, counterMaxKicks, h,
		func(slot uint32) uint32 { fp, _ := unpackCounterSlot(slot); return hash32Val(fp) },
		func(idx uint32, v uint32) { atomic.StoreUint32(&m.buckets[idx], v) }) {
		m.count++
		return 1
	}

	// Overflow condition: return saturating max. The table is unchanged.
	return counterMaxCount
}

// GetCount performs a lock-free atomic lookup of key's frequency count.
func (m *CountingMaplet) GetCount(key string) uint8 {
	h := mapletHash64(key)
	i1, fp, i2 := m.decompose(h)

	base1 := i1 * counterSlotsPerBucket
	for s := range uint32(counterSlotsPerBucket) {
		slot := atomic.LoadUint32(&m.buckets[base1+s])
		if slot != 0 && (slot>>8) == fp {
			return uint8(slot & 0xFF)
		}
	}

	base2 := i2 * counterSlotsPerBucket
	for s := range uint32(counterSlotsPerBucket) {
		slot := atomic.LoadUint32(&m.buckets[base2+s])
		if slot != 0 && (slot>>8) == fp {
			return uint8(slot & 0xFF)
		}
	}

	return 0
}

// Reset clears all counters in-place with zero heap allocations.
func (m *CountingMaplet) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// GetCount reads without the lock, so every write is atomic.
	for i := range m.buckets {
		atomic.StoreUint32(&m.buckets[i], 0)
	}
	m.count = 0
}

// MemoryBytes returns table memory in bytes.
func (m *CountingMaplet) MemoryBytes() int {
	return len(m.buckets) * 4
}

// Count returns the number of active distinct keys tracked.
func (m *CountingMaplet) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int(m.count)
}

// ---------------------------------------------------------------------------
// ScalableLatticeMaplet: Dynamically Chained Lattice Maplets
// ---------------------------------------------------------------------------

// ScalableLatticeMaplet chains multiple LatticeMaplet layers dynamically.
// When layer capacity is reached, it allocates a doubled secondary layer.
type ScalableLatticeMaplet struct {
	mu     sync.RWMutex
	layers []*LatticeMaplet
}

// NewScalableLatticeMaplet initializes a scalable maplet starting with initialCapacity.
func NewScalableLatticeMaplet(initialCapacity int) *ScalableLatticeMaplet {
	return &ScalableLatticeMaplet{
		layers: []*LatticeMaplet{NewLatticeMaplet(initialCapacity)},
	}
}

// Insert adds key and val to the scalable maplet, chaining new layers on overflow.
func (s *ScalableLatticeMaplet) Insert(key string, val uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	last := s.layers[len(s.layers)-1]
	err := last.Insert(key, val)
	if err == nil {
		return nil
	}

	// Current layer full: allocate next layer doubled in capacity
	nextCap := int(last.maxCount) * 2
	nextLayer := NewLatticeMaplet(nextCap)
	if err := nextLayer.Insert(key, val); err != nil {
		return err
	}
	s.layers = append(s.layers, nextLayer)
	return nil
}

// Lookup queries all layers and returns the least upper bound (join) of any matches.
func (s *ScalableLatticeMaplet) Lookup(key string) (uint8, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best uint8
	var foundAny bool

	// Check from newest to oldest
	for i := len(s.layers) - 1; i >= 0; i-- {
		val, found := s.layers[i].Lookup(key)
		if found {
			foundAny = true
			if val > best {
				best = val
			}
		}
	}

	return best, foundAny
}

// LayerCount returns the number of chained maplet layers.
func (s *ScalableLatticeMaplet) LayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.layers)
}

// MemoryBytes returns the combined memory of all layers in bytes.
func (s *ScalableLatticeMaplet) MemoryBytes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, l := range s.layers {
		total += l.MemoryBytes()
	}
	return total
}
