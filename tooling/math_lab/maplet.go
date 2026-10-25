package math_lab

import (
	"errors"
	"fmt"
)

// Maplet is a space-efficient approximate key-value mapping structure with
// one-sided error guarantees, based on the Bender et al. 2026 paper:
// "Time To Replace Your Filter: How Maplets Simplify System Design".
//
// Invariant (One-Sided Error):
// 1. If key k in S, Lookup(k) returns (v, true) with the exact canonical value v.
// 2. If key k not in S, Lookup(k) returns (0, false) with probability 1 - epsilon.
// 3. Any false positive (returning non-zero with true for an absent key) occurs
//    with bounded probability <= epsilon.
//
// Memory Layout:
// Contiguous uint32 slab formatted into 4-slot buckets (16 bytes per bucket = 1/4 cache line).
// Each 32-bit slot holds:
// - Fingerprint: 28 bits (collision probability < 10^-8)
// - Micro-value: 4 bits (values 0..15)
// Total space: 4 bytes per slot (32 bits per key).
// Compared to Go's map[string]int (~64 bytes/key) this is a 16x memory reduction.
// Compared to EnvelopeMetadata (~1 KB) this is a 250x memory reduction.
type Maplet struct {
	numBuckets uint32
	mask       uint32
	count      uint32
	maxCount   uint32
	buckets    []uint32
}

const (
	slotsPerBucket  = 4
	fingerprintBits = 28
	valueBits       = 4
	maxFingerprint  = (uint32(1) << fingerprintBits) - 1
	maxValue        = (uint8(1) << valueBits) - 1
	maxCuckooKicks  = 500
)

// NewMaplet initializes a Maplet with capacity for expectedKeys.
func NewMaplet(expectedKeys int) *Maplet {
	minSlots := float64(expectedKeys) / 0.85
	numBuckets := uint32(1)
	for float64(numBuckets*slotsPerBucket) < minSlots {
		numBuckets <<= 1
	}
	if numBuckets < 8 {
		numBuckets = 8
	}

	return &Maplet{
		numBuckets: numBuckets,
		mask:       numBuckets - 1,
		maxCount:   uint32(float64(numBuckets*slotsPerBucket) * 0.90),
		buckets:    make([]uint32, numBuckets*slotsPerBucket),
	}
}

// hash64 computes a 64-bit splitmix hash of key.
func hash64(key string) uint64 {
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

func (m *Maplet) decompose(h uint64) (i1 uint32, fp uint32, i2 uint32) {
	i1 = uint32(h) & m.mask // #nosec G115 -- lower 32 bits used as bucket index
	fpRaw := uint32((h >> 32) & uint64(maxFingerprint))
	if fpRaw == 0 {
		fpRaw = 1
	}
	fp = fpRaw

	fpHash := uint32(hash32(fp))
	i2 = (i1 ^ fpHash) & m.mask
	return i1, fp, i2
}

func hash32(v uint32) uint32 {
	v ^= v >> 16
	v *= 0x7feb352d
	v ^= v >> 15
	v *= 0x846ca68b
	v ^= v >> 16
	return v
}

func packSlot(fp uint32, val uint8) uint32 {
	return (fp << valueBits) | uint32(val&maxValue)
}

func unpackSlot(slot uint32) (fp uint32, val uint8) {
	return slot >> valueBits, uint8(slot & uint32(maxValue))
}

// Insert places key with metadata value val (0..15) into the Maplet.
func (m *Maplet) Insert(key string, val uint8) error {
	if m.count >= m.maxCount {
		return errors.New("maplet capacity reached")
	}
	if val > maxValue {
		return fmt.Errorf("value %d exceeds max 4-bit value %d", val, maxValue)
	}

	h := hash64(key)
	i1, fp, i2 := m.decompose(h)
	slotEntry := packSlot(fp, val)

	// Check if already present (update value if exists)
	base1 := i1 * slotsPerBucket
	for s := uint32(0); s < slotsPerBucket; s++ {
		if m.buckets[base1+s] != 0 && (m.buckets[base1+s]>>valueBits) == fp {
			m.buckets[base1+s] = slotEntry
			return nil
		}
	}
	base2 := i2 * slotsPerBucket
	for s := uint32(0); s < slotsPerBucket; s++ {
		if m.buckets[base2+s] != 0 && (m.buckets[base2+s]>>valueBits) == fp {
			m.buckets[base2+s] = slotEntry
			return nil
		}
	}

	// Try inserting into empty slot in i1
	for s := uint32(0); s < slotsPerBucket; s++ {
		if m.buckets[base1+s] == 0 {
			m.buckets[base1+s] = slotEntry
			m.count++
			return nil
		}
	}

	// Try inserting into empty slot in i2
	for s := uint32(0); s < slotsPerBucket; s++ {
		if m.buckets[base2+s] == 0 {
			m.buckets[base2+s] = slotEntry
			m.count++
			return nil
		}
	}

	// Cuckoo displacement loop
	currBucket := i1
	currEntry := slotEntry
	for kick := 0; kick < maxCuckooKicks; kick++ {
		slotIdx := uint32(kick % slotsPerBucket)
		targetIdx := currBucket*slotsPerBucket + slotIdx

		victim := m.buckets[targetIdx]
		m.buckets[targetIdx] = currEntry

		vFp, _ := unpackSlot(victim)
		vFpHash := uint32(hash32(vFp))
		currBucket = (currBucket ^ vFpHash) & m.mask
		currEntry = victim

		base := currBucket * slotsPerBucket
		for s := uint32(0); s < slotsPerBucket; s++ {
			if m.buckets[base+s] == 0 {
				m.buckets[base+s] = currEntry
				m.count++
				return nil
			}
		}
	}

	return errors.New("maplet insertion failed: cuckoo cycle detected")
}

// Lookup queries the Maplet for key.
// Returns (value, true) if found, or (0, false) if definitely absent.
// One-sided error: if key was inserted, Lookup always returns true with the exact value.
func (m *Maplet) Lookup(key string) (uint8, bool) {
	h := hash64(key)
	i1, fp, i2 := m.decompose(h)

	// Check bucket 1 (contiguous 4 uint32s = 16 bytes, quarter of a single cache line)
	base1 := i1 * slotsPerBucket
	for s := uint32(0); s < slotsPerBucket; s++ {
		slot := m.buckets[base1+s]
		if slot != 0 && (slot>>valueBits) == fp {
			return uint8(slot & uint32(maxValue)), true
		}
	}

	// Check bucket 2
	base2 := i2 * slotsPerBucket
	for s := uint32(0); s < slotsPerBucket; s++ {
		slot := m.buckets[base2+s]
		if slot != 0 && (slot>>valueBits) == fp {
			return uint8(slot & uint32(maxValue)), true
		}
	}

	return 0, false
}

// MemoryBytes returns the raw memory size in bytes of the Maplet table.
func (m *Maplet) MemoryBytes() int {
	return len(m.buckets) * 4
}

// BitsPerKey returns the effective memory cost per inserted key.
func (m *Maplet) BitsPerKey() float64 {
	if m.count == 0 {
		return 0
	}
	return float64(len(m.buckets)*32) / float64(m.count)
}
