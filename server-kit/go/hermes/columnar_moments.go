package hermes

import (
	"math"
)

// Second-moment reductions: variance and standard deviation over columnar
// vectors, and — the reason this file exists separately from the first-moment
// reductions in columnar_reduce.go — across *partitions* of them.
//
// The rule stated in columnar_reduce.go is that a reduction substitutes the
// identity element of its monoid for each null. That rule presumes the
// reduction is a monoid. Variance is not, and neither is standard deviation,
// for the same reason mean is not: it is a *projection* of a monoid rather than
// a monoid itself.
//
// This distinction is not pedantic. It is the difference between a distributed
// reduce that is correct and one that is quietly, plausibly wrong. Partial
// standard deviations cannot be averaged, and partial variances cannot be
// summed, because each partition computes deviations from its own local mean.
// Two partitions with identical internal spread but different centres carry no
// record, in their variances alone, of how far apart those centres were — and
// that separation is part of the combined variance. The information needed to
// recover it has to be carried explicitly.
//
// The monoid that does carry it is the triple:
//
//	(count, mean, M2)    M2 = Σ(xᵢ − mean)²
//
// with identity (0, 0, 0) and the merge below. Variance and standard deviation
// are then projections of that triple, exactly as mean is the projection of
// (sum, count). The algebra is the same shape as the one already in
// columnar_reduce.go, one moment higher.
//
// Why M2 and not the arithmetically simpler Σx²
//
// The naive triple (count, Σx, Σx²) is algebraically valid — variance is
// Σx²/n − (Σx/n)² — and numerically unusable. Both terms grow with the square
// of the magnitude of the data while their difference stays the size of the
// spread, so a column centred far from zero spends its precision computing two
// large numbers that nearly cancel. At magnitude 1e9 with unit spread, float64
// has roughly no significant digits left for the answer, and the subtraction
// can return a negative variance. TestMomentsSurvivesCatastrophicCancellation
// demonstrates exactly that failure against this implementation.
//
// The M2 form (Chan, Golub & LeVeque 1979) never forms those large
// intermediates: it accumulates deviations, which stay the size of the spread.
// It costs one extra multiply per merge and nothing per element.
//
// See docs/columnar_null_algebra.md for the identity-substitution derivation
// this inherits.

// Moments is the monoid from which mean, variance, and standard deviation are
// projected: the number of contributing rows, their mean, and M2, the sum of
// squared deviations from that mean.
//
// The zero value is the identity element. Merging it with anything returns that
// thing unchanged, which is what makes an empty partition free to include and a
// missing partition safe to omit.
//
// Count carries the same meaning as Reduction.Count: rows that actually
// contributed. Count == 0 means every row was null and Mean and M2 are the bare
// identity, not an answer — the projections below report that rather than
// returning a misleading zero.
type Moments struct {
	Count int
	Mean  float64
	M2    float64
}

// MergeMoments combines two partial reductions. This is the monoid operation:
// associative, commutative, with the zero Moments as identity — properties the
// tests assert rather than assume, because every distributed reduce built on
// this depends on all three.
//
// The delta form is deliberate. Computing the merged mean as
// (na·ma + nb·mb)/n reintroduces the large intermediates that the M2
// representation exists to avoid; shifting a by a correction proportional to
// b's share keeps every quantity the size of the data's spread.
func MergeMoments(a, b Moments) Moments {
	// Identity short-circuits. Not an optimisation: with count 0 the merge
	// below divides by zero when both sides are empty, and an empty side's Mean
	// is a bare identity that must not be allowed to pull the merged mean.
	if a.Count == 0 {
		return b
	}
	if b.Count == 0 {
		return a
	}

	countA := float64(a.Count)
	countB := float64(b.Count)
	total := countA + countB

	delta := b.Mean - a.Mean
	return Moments{
		Count: a.Count + b.Count,
		Mean:  a.Mean + delta*(countB/total),
		// The delta² term is the separation of the two centres, weighted by how
		// much mass sits on each side. It is precisely the quantity that summing
		// partial variances discards, and omitting it is the whole of the bug
		// this type exists to prevent.
		M2: a.M2 + b.M2 + delta*delta*(countA*countB/total),
	}
}

// Merge returns the monoid product of m and other. Method form of
// MergeMoments, for folding over a slice of partition results.
func (m Moments) Merge(other Moments) Moments { return MergeMoments(m, other) }

// IsNull reports whether no row contributed, making every projection below
// meaningless.
func (m Moments) IsNull() bool { return m.Count == 0 }

// MeanValue returns the arithmetic mean and whether it is defined. It agrees
// with Float64Vector.MeanValid to within floating-point rounding; both are
// projections of a monoid, this one simply carries a second moment alongside.
func (m Moments) MeanValue() (float64, bool) {
	if m.Count == 0 {
		return 0, false
	}
	return m.Mean, true
}

// Variance returns the sample variance (Bessel-corrected, dividing by n−1) and
// whether it is defined.
//
// Fewer than two contributing rows has no sample variance — one point has no
// spread to measure — and this reports that rather than returning zero, which a
// caller would read as "measured, and found to be zero".
func (m Moments) Variance() (float64, bool) {
	if m.Count < 2 {
		return 0, false
	}
	return m.M2 / float64(m.Count-1), true
}

// PopulationVariance returns the population variance (dividing by n) and
// whether it is defined. Defined for a single row, where it is zero: one point
// is the whole population and a population does not deviate from itself.
func (m Moments) PopulationVariance() (float64, bool) {
	if m.Count == 0 {
		return 0, false
	}
	return m.M2 / float64(m.Count), true
}

// StdDev returns the sample standard deviation and whether it is defined.
func (m Moments) StdDev() (float64, bool) {
	variance, ok := m.Variance()
	if !ok {
		return 0, false
	}
	return math.Sqrt(variance), true
}

// PopulationStdDev returns the population standard deviation and whether it is
// defined.
func (m Moments) PopulationStdDev() (float64, bool) {
	variance, ok := m.PopulationVariance()
	if !ok {
		return 0, false
	}
	return math.Sqrt(variance), true
}

// MomentsValid reduces the present rows of the column to the moment triple in
// one pass over the values.
//
// Nulls are handled by the same identity substitution as every other reduction
// here, applied one level up: the masked quantity is the *deviation*, not the
// value. Masking the value would leave a null contributing (0 − mean), which is
// not zero and not nothing; masking the deviation makes it contribute exactly
// the identity of the M2 sum. As elsewhere in this package the mask is a value
// rather than a branch, so the loop runs over every row unconditionally.
//
// NaN in a present row propagates, matching SumValid rather than MinValid: a
// sum of an undefined quantity is undefined, and so is its spread.
//
// # Reducing a shard that may be empty
//
// Callers that reduce a *slice* of a projection — a sharded aggregation, a
// filtered batch — must not reach this method through a type assertion alone.
// buildFieldVector derives a column's type from the first present scalar value,
// so a batch in which the field is absent from every row yields a StringVector
// whatever the field holds when populated. `batch.Columns[i].Data.(*Float64Vector)`
// then reports ok=false, and that is not a type error: for a sharded reducer an
// empty shard is routine and its contribution is the identity.
//
// The type-agnostic test is on the Vector interface and covers both the
// zero-row case and the every-row-null case:
//
//	column := batch.Columns[i].Data
//	if column.Len() == column.NullCount() {
//	    return Moments{} // identity; merges into any accumulator for free
//	}
//	vector, ok := column.(*Float64Vector)
//	if !ok {
//	    // a genuinely non-float column: a real type error
//	}
//	partial := vector.MomentsValid()
func (v *Float64Vector) MomentsValid() Moments {
	return momentsFloat64Valid(v.values, v.validity.words)
}

// momentsFloat64Valid accumulates per 64-row window and folds the windows with
// the monoid.
//
// The blocking is what keeps this both fast and stable. Welford's per-element
// recurrence is stable but carries a division on every row; the two small passes
// here run over 64 contiguous values already resident in L1, and the merge that
// combines windows is the same operation used to combine partitions across
// hosts. The distributed case and the single-column case are therefore the same
// code path, which is why a partition-parity test can prove both.
//
// Both passes use four interleaved accumulators, for the reason given in
// columnar_mlp.go: a single accumulator carries a dependency chain and each
// iteration stalls on the previous one's latency. Here that choice is not only a
// throughput decision — four accumulators each sum a quarter of the terms, so
// the error growth is bounded by the shorter chain as well.
//
// Measured against three alternatives on a 65,536-row column
// (columnar_moments_bench_test.go, medians of 12 runs):
//
//	this kernel                      84,051 ns/op    relerr 9.3e-08 @1e12
//	same shape, one accumulator     130,973 ns/op    relerr 5.2e-06
//	shifted single-pass, 4-way       84,180 ns/op    relerr 3.0e-07
//	naive (Σx, Σx²)                  82,652 ns/op    NEGATIVE variance
//
// So the interleaving buys 1.56x over the form it replaced *and* 56x lower
// error, because four accumulators each sum a quarter of the terms and the
// shorter dependency chain bounds both latency and error growth.
//
// It is not, however, the fastest kernel here: the naive form is ~1.7% quicker
// because it makes one pass instead of two. That 1.7% is the entire price of
// the correct answer, and the naive kernel spends it returning a variance below
// zero on data offset to 1e12 — not an inaccurate answer but an impossible one.
// The shifted single-pass variant ties this kernel on speed and is 3.2x worse
// at 1e12, so it loses on the only axis that separates them.
func momentsFloat64Valid(values []float64, validity []uint64) Moments {
	var acc Moments
	n := len(values)

	for w, word := range validity {
		if word == 0 {
			continue
		}
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}

		// Pass one: the window's count and mean, via the masked sum that
		// sumFloat64Valid already establishes as the correct null handling.
		var s0, s1, s2, s3 float64
		count := 0
		i := base
		for ; i+4 <= end; i += 4 {
			b := uint(i - base)
			s0 += math.Float64frombits(math.Float64bits(values[i]) & maskBit(word, b))
			s1 += math.Float64frombits(math.Float64bits(values[i+1]) & maskBit(word, b+1))
			s2 += math.Float64frombits(math.Float64bits(values[i+2]) & maskBit(word, b+2))
			s3 += math.Float64frombits(math.Float64bits(values[i+3]) & maskBit(word, b+3))
			count += int((word >> b) & 1)
			count += int((word >> (b + 1)) & 1)
			count += int((word >> (b + 2)) & 1)
			count += int((word >> (b + 3)) & 1)
		}
		for ; i < end; i++ {
			b := uint(i - base)
			s0 += math.Float64frombits(math.Float64bits(values[i]) & maskBit(word, b))
			count += int((word >> b) & 1)
		}
		if count == 0 {
			continue
		}
		mean := ((s0 + s1) + (s2 + s3)) / float64(count)

		// Pass two: squared deviations from that mean, with the *deviation*
		// masked rather than the value. Masking the value would leave a null
		// contributing (0 − mean), which is neither zero nor nothing; masking
		// the deviation makes it contribute exactly the identity of the M2 sum.
		var d0, d1, d2, d3 float64
		i = base
		for ; i+4 <= end; i += 4 {
			b := uint(i - base)
			e0 := math.Float64frombits(math.Float64bits(values[i]-mean) & maskBit(word, b))
			e1 := math.Float64frombits(math.Float64bits(values[i+1]-mean) & maskBit(word, b+1))
			e2 := math.Float64frombits(math.Float64bits(values[i+2]-mean) & maskBit(word, b+2))
			e3 := math.Float64frombits(math.Float64bits(values[i+3]-mean) & maskBit(word, b+3))
			d0 += e0 * e0
			d1 += e1 * e1
			d2 += e2 * e2
			d3 += e3 * e3
		}
		for ; i < end; i++ {
			b := uint(i - base)
			e := math.Float64frombits(math.Float64bits(values[i]-mean) & maskBit(word, b))
			d0 += e * e
		}

		acc = MergeMoments(acc, Moments{Count: count, Mean: mean, M2: (d0 + d1) + (d2 + d3)})
	}

	return acc
}
