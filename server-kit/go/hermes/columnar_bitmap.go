package hermes

import (
	"fmt"
	"math/bits"
)

// bitmap is the single packed bit-vector of the columnar engine.
//
// The engine carries two conceptually different masks over a batch — validity
// ("row i holds a value") and selection ("row i survived the predicate") — but
// they are the same structure over the same word kernels, and the engine's
// central step is the bitwise AND of the two. Keeping one implementation means
// the tail-word hygiene, the interleaved POPCNT kernel, and the shape checks
// are written once and inherited by both.
//
// Bit i lives at (words[i>>6]>>(i&63))&1. Bits at positions >= n are always
// zero: every operation that can raise them re-applies tailMask, so a
// complement can never introduce phantom rows.
//
// Word size is 64 bits, which is what the CPU lanes want. The packed byte
// layout is little-endian, so the same bytes are simultaneously a valid 32-bit
// word array — the width WGSL storage buffers and warp ballots use. See
// docs/columnar_null_algebra.md ("Crossing lanes") for why that matters and
// what still has to be proven before the GPU lanes rely on it.
type bitmap struct {
	words []uint64
	n     int
}

// newBitmap returns an all-zero bitmap covering n bits.
func newBitmap(n int) bitmap {
	if n < 0 {
		n = 0
	}
	return bitmap{words: make([]uint64, (n+63)/64), n: n}
}

// set raises bit i. Callers must hold i in [0, n).
func (b *bitmap) set(i int) {
	b.words[i>>6] |= 1 << uint(i&63)
}

// get reports whether bit i is set. Out-of-range bits read as zero.
func (b *bitmap) get(i int) bool {
	if i < 0 || i >= b.n {
		return false
	}
	return (b.words[i>>6]>>uint(i&63))&1 == 1
}

// count returns the number of set bits. See popcountWords for the interleaved
// POPCNT/CNT kernel.
func (b *bitmap) count() int {
	return popcountWords(b.words)
}

// tailMask zeroes any bits above n in the final word so complement-style
// operations cannot raise phantom rows.
func (b *bitmap) tailMask() {
	if b.n == 0 || len(b.words) == 0 {
		return
	}
	rem := uint(b.n & 63)
	if rem != 0 {
		b.words[len(b.words)-1] &= (1 << rem) - 1
	}
}

func (b *bitmap) sameShape(other *bitmap) error {
	if b.n != other.n {
		return fmt.Errorf("hermes bitmaps cover different row counts: %d vs %d", b.n, other.n)
	}
	return nil
}

// and intersects the receiver with other in place.
func (b *bitmap) and(other *bitmap) error {
	if err := b.sameShape(other); err != nil {
		return err
	}
	andWords(b.words, other.words)
	return nil
}

// or unions the receiver with other in place.
func (b *bitmap) or(other *bitmap) error {
	if err := b.sameShape(other); err != nil {
		return err
	}
	orWords(b.words, other.words)
	return nil
}

// andNot removes other's set bits from the receiver in place.
func (b *bitmap) andNot(other *bitmap) error {
	if err := b.sameShape(other); err != nil {
		return err
	}
	andNotWords(b.words, other.words)
	return nil
}

// not complements the receiver in place, preserving tail hygiene.
//
// Note for callers working with nulls: complementing a validity-masked mask
// turns null rows into set rows, because "did not match" and "had no value to
// match" are the same bit under two-valued logic. Any complement over a
// nullable column must be re-intersected with validity. See
// docs/columnar_null_algebra.md ("The complement trap").
func (b *bitmap) not() {
	notWords(b.words)
	b.tailMask()
}

// andClamped intersects the receiver with a raw word slice that may be shorter
// than the receiver. Receiver words beyond the source describe rows the source
// has no information about and are cleared.
//
// The bulk region runs through the interleaved andWords kernel; hoisting the
// length split out of the per-word loop keeps the hot path branch-free.
func (b *bitmap) andClamped(words []uint64) {
	if words == nil {
		return
	}
	n := min(len(words), len(b.words))
	andWords(b.words[:n], words[:n])
	for i := n; i < len(b.words); i++ {
		b.words[i] = 0
	}
}

// forEachSet visits set bits in ascending order until fn returns false.
// Iteration is a word bit-scan: zero words are skipped in one compare, and each
// set bit costs one TrailingZeros64.
func (b *bitmap) forEachSet(fn func(i int) bool) {
	for wi, w := range b.words {
		base := wi << 6
		for w != 0 {
			bit := bits.TrailingZeros64(w)
			if !fn(base + bit) {
				return
			}
			w &= w - 1
		}
	}
}

// wordMask returns the all-ones-or-all-zeros mask for bit i of a validity word,
// which is the branch-free form of "is row i present".
//
// This is the primitive behind identity-fill reduction: masking a value with
// it yields either the value or the zero pattern, with no branch for the
// processor to mispredict and no divergence for a GPU lane to serialize.
func maskBit(word uint64, bit uint) uint64 {
	return -((word >> bit) & 1)
}
