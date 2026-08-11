package hermes

import (
	"math"
	"math/bits"
)

// Null-aware reductions over columnar vectors.
//
// The rule, stated once so every kernel below can be read against it: a null is
// not a value, it is the absence of one, so a reduction over a nullable column
// substitutes the identity element of the reduction's monoid for each null.
// Because x ⊕ e = x, nulls provably cannot perturb the result:
//
//	sum      (+, 0)                 null ↦ 0
//	product  (×, 1)                 null ↦ 1
//	min      (min, +∞ / MaxInt64)   null ↦ +∞
//	max      (max, −∞ / MinInt64)   null ↦ −∞
//	count    (+, 0)                 null ↦ 0
//
// Identity substitution is also the fast form. The fill is a value, not a
// branch, so the kernel runs over every row unconditionally: nothing for a CPU
// to mispredict, and nothing for a GPU lane to serialize on divergence. The
// mathematically correct handling and the cheapest handling are the same thing,
// which is what an identity element is for.
//
// Min and max need one extra piece. Their identities (MaxInt64, MinInt64, ±Inf)
// are themselves legal values, so a bare result cannot distinguish "every row
// was null" from "the real minimum is MaxInt64". The fix is to reduce over the
// product monoid (M, ⊕, e) × (ℕ, +, 0) and carry the count of contributing
// rows alongside the value — which is free, because that count is the POPCNT of
// the validity bitmap the column already holds. Mean falls out of the same pair
// as sum/count.
//
// See docs/columnar_null_algebra.md for the full derivation, the two-valued
// logic choice, and the cross-lane word-width argument.

// Reduction is the result of a null-aware reduction: the monoid value together
// with the number of present (non-null) rows that contributed to it.
//
// Count == 0 means every row was null and Value holds the bare identity, which
// is not a meaningful answer — check IsNull before reading Value.
type Reduction[T int64 | float64] struct {
	Value T
	Count int
}

// IsNull reports whether no row contributed, making Value meaningless.
func (r Reduction[T]) IsNull() bool { return r.Count == 0 }

// Identity elements, named for the operation they belong to: the identity of
// min is +∞ (no value can be larger), the identity of max is −∞. The identity
// of sum is plain 0 and is applied by masking rather than named here.
const (
	int64IdentityForMin = int64(math.MaxInt64)
	int64IdentityForMax = int64(math.MinInt64)
)

var (
	float64PosInfBits = math.Float64bits(math.Inf(1))
	float64NegInfBits = math.Float64bits(math.Inf(-1))
)

// wordSpan returns the row range covered by validity word w, clamped to n.
func wordSpan(w, n int) (base, end int) {
	base = w << 6
	return base, min(base+64, n)
}

// ---------------------------------------------------------------------------
// Int64Vector
// ---------------------------------------------------------------------------

// CountValid returns the number of present rows. One POPCNT per validity word;
// the values buffer is never touched.
func (v *Int64Vector) CountValid() int { return v.validity.validCount() }

// SumValid returns the sum over present rows, with nulls filled by the additive
// identity 0.
//
// A fully-present validity word takes an unmasked path: dense columns (every
// reserved field hermes builds is dense) never pay for the mask at all. Sparse
// words fall to the branch-free masked form.
func (v *Int64Vector) SumValid() Reduction[int64] {
	sum, count := sumInt64Valid(v.values, v.validity.words)
	return Reduction[int64]{Value: sum, Count: count}
}

// MinValid returns the minimum over present rows, with nulls filled by +∞
// (MaxInt64). Count distinguishes an all-null column from a column whose real
// minimum is MaxInt64.
func (v *Int64Vector) MinValid() Reduction[int64] {
	best, count := extremumInt64Valid(v.values, v.validity.words, true)
	return Reduction[int64]{Value: best, Count: count}
}

// MaxValid returns the maximum over present rows, with nulls filled by −∞
// (MinInt64). Count distinguishes an all-null column from a column whose real
// maximum is MinInt64.
func (v *Int64Vector) MaxValid() Reduction[int64] {
	best, count := extremumInt64Valid(v.values, v.validity.words, false)
	return Reduction[int64]{Value: best, Count: count}
}

func sumInt64Valid(values []int64, validity []uint64) (int64, int) {
	var s0, s1, s2, s3 int64
	count := 0
	n := len(values)
	for w, word := range validity {
		if word == 0 {
			continue
		}
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}
		count += bits.OnesCount64(word)
		if word == math.MaxUint64 && end-base == 64 {
			// Dense word: identity substitution is a no-op, so skip the mask
			// and run the plain 4-way interleaved accumulate.
			for i := base; i < end; i += 4 {
				s0 += values[i]
				s1 += values[i+1]
				s2 += values[i+2]
				s3 += values[i+3]
			}
			continue
		}
		i := base
		for ; i+4 <= end; i += 4 {
			b := uint(i - base)
			// #nosec G115 -- maskBit yields 0 or ^uint64(0); the int64 form is
			// the intended 0 / -1 two's-complement mask.
			s0 += values[i] & int64(maskBit(word, b))
			// #nosec G115
			s1 += values[i+1] & int64(maskBit(word, b+1))
			// #nosec G115
			s2 += values[i+2] & int64(maskBit(word, b+2))
			// #nosec G115
			s3 += values[i+3] & int64(maskBit(word, b+3))
		}
		for ; i < end; i++ {
			// #nosec G115
			s0 += values[i] & int64(maskBit(word, uint(i-base)))
		}
	}
	return (s0 + s1) + (s2 + s3), count
}

// extremumInt64Valid computes min (wantMin) or max over present rows by filling
// nulls with the corresponding identity, so the validity test never becomes a
// branch. The only branch left is the comparison itself, which is data.
func extremumInt64Valid(values []int64, validity []uint64, wantMin bool) (int64, int) {
	// The accumulator seeds at the same identity that fills nulls. That single
	// choice gives both properties for free: no null can ever win a comparison,
	// and an all-null column returns the bare identity alongside Count == 0.
	identity := int64IdentityForMax
	if wantMin {
		identity = int64IdentityForMin
	}
	best := identity

	count := 0
	n := len(values)
	for w, word := range validity {
		if word == 0 {
			continue
		}
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}
		count += bits.OnesCount64(word)
		for i := base; i < end; i++ {
			// #nosec G115 -- 0 / ^uint64(0) mask, see sumInt64Valid.
			m := int64(maskBit(word, uint(i-base)))
			v := (values[i] & m) | (identity &^ m)
			if wantMin {
				if v < best {
					best = v
				}
				continue
			}
			if v > best {
				best = v
			}
		}
	}
	return best, count
}

// ---------------------------------------------------------------------------
// Float64Vector
// ---------------------------------------------------------------------------

// CountValid returns the number of present rows.
func (v *Float64Vector) CountValid() int { return v.validity.validCount() }

// SumValid returns the sum over present rows, with nulls filled by 0.
//
// Masking is done on the bit pattern rather than by multiplying by 0/1: a
// multiply would turn a valid ±Inf into NaN. Clearing all bits yields +0.0,
// the additive identity, for every null regardless of what the value slot held.
//
// NaN in a *present* row propagates, because NaN + x = NaN. That is the correct
// behaviour for a sum and it differs from MinValid/MaxValid — see the note
// there.
func (v *Float64Vector) SumValid() Reduction[float64] {
	sum, count := sumFloat64Valid(v.values, v.validity.words)
	return Reduction[float64]{Value: sum, Count: count}
}

// MinValid returns the minimum over present rows, with nulls filled by +Inf.
//
// NaN in a present row is ignored rather than propagated: both `v < best` and
// `v > best` are false for NaN, so it can never win a comparison. Callers that
// need NaN-poisoning semantics must test for it separately.
func (v *Float64Vector) MinValid() Reduction[float64] {
	best, count := extremumFloat64Valid(v.values, v.validity.words, true)
	return Reduction[float64]{Value: best, Count: count}
}

// MaxValid returns the maximum over present rows, with nulls filled by −Inf.
// NaN handling matches MinValid.
func (v *Float64Vector) MaxValid() Reduction[float64] {
	best, count := extremumFloat64Valid(v.values, v.validity.words, false)
	return Reduction[float64]{Value: best, Count: count}
}

// MeanValid returns the arithmetic mean over present rows.
//
// Mean is not itself a monoid, which is why it is derived rather than reduced:
// the (sum, count) pair is the monoid, and the mean is its projection. That is
// also why it needs no second pass over the data.
func (v *Float64Vector) MeanValid() Reduction[float64] {
	sum := v.SumValid()
	if sum.Count == 0 {
		return Reduction[float64]{}
	}
	return Reduction[float64]{Value: sum.Value / float64(sum.Count), Count: sum.Count}
}

func sumFloat64Valid(values []float64, validity []uint64) (float64, int) {
	var s0, s1, s2, s3 float64
	count := 0
	n := len(values)
	for w, word := range validity {
		if word == 0 {
			continue
		}
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}
		count += bits.OnesCount64(word)
		if word == math.MaxUint64 && end-base == 64 {
			for i := base; i < end; i += 4 {
				s0 += values[i]
				s1 += values[i+1]
				s2 += values[i+2]
				s3 += values[i+3]
			}
			continue
		}
		i := base
		for ; i+4 <= end; i += 4 {
			b := uint(i - base)
			s0 += math.Float64frombits(math.Float64bits(values[i]) & maskBit(word, b))
			s1 += math.Float64frombits(math.Float64bits(values[i+1]) & maskBit(word, b+1))
			s2 += math.Float64frombits(math.Float64bits(values[i+2]) & maskBit(word, b+2))
			s3 += math.Float64frombits(math.Float64bits(values[i+3]) & maskBit(word, b+3))
		}
		for ; i < end; i++ {
			s0 += math.Float64frombits(math.Float64bits(values[i]) & maskBit(word, uint(i-base)))
		}
	}
	return (s0 + s1) + (s2 + s3), count
}

func extremumFloat64Valid(values []float64, validity []uint64, wantMin bool) (float64, int) {
	identityBits := float64NegInfBits
	best := math.Inf(-1)
	if wantMin {
		identityBits = float64PosInfBits
		best = math.Inf(1)
	}

	count := 0
	n := len(values)
	for w, word := range validity {
		if word == 0 {
			continue
		}
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}
		count += bits.OnesCount64(word)
		for i := base; i < end; i++ {
			m := maskBit(word, uint(i-base))
			v := math.Float64frombits((math.Float64bits(values[i]) & m) | (identityBits &^ m))
			if wantMin {
				if v < best {
					best = v
				}
				continue
			}
			if v > best {
				best = v
			}
		}
	}
	if count == 0 {
		// Every row was null: report the bare identity alongside Count == 0.
		if wantMin {
			return math.Inf(1), 0
		}
		return math.Inf(-1), 0
	}
	return best, count
}
