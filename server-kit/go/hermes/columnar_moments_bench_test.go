package hermes

import (
	"math"
	"math/big"
	"testing"
)

// Candidate kernels for the second-moment reduction, measured against each
// other on throughput and against exact arithmetic on accuracy.
//
// The question this file answers is "what is the fastest kernel that still
// guarantees the answer", and it is a real question because the fastest kernel
// is not the accurate one. Four candidates:
//
//	A  blocked two-pass, scalar          — what shipped
//	B  blocked two-pass, 4-way MLP       — A with the interleaving the rest of
//	                                       this package already uses
//	C  shifted single-pass, 4-way MLP    — one pass, cancellation removed by
//	                                       subtracting a per-window origin
//	D  naive (Σx, Σx²)                   — the control; fastest and wrong
//
// All four fold windows with the same Chan merge, so the comparison isolates
// the per-window kernel rather than the composition.

// ---------------------------------------------------------------------------
// Exact oracle
// ---------------------------------------------------------------------------

// exactMomentsFloat64 returns the correctly-rounded true mean and sample
// variance of xs.
//
// Every finite float64 is a dyadic rational, so big.Rat holds each value, the
// mean Σx/n, and every squared deviation with zero error. The only rounding is
// the single Float64() conversion at the end. This is the same discipline as
// exactSumFloat64 in columnar_sum_test.go, one moment higher.
func exactMomentsFloat64(t *testing.T, xs []float64) (mean, sampleVariance float64) {
	t.Helper()
	if len(xs) == 0 {
		return 0, math.NaN()
	}

	sum := new(big.Rat)
	term := new(big.Rat)
	for i, x := range xs {
		if term.SetFloat64(x) == nil {
			t.Fatalf("exactMomentsFloat64: xs[%d]=%v is not finite", i, x)
		}
		sum.Add(sum, term)
	}
	n := new(big.Rat).SetInt64(int64(len(xs)))
	exactMean := new(big.Rat).Quo(sum, n)

	m2 := new(big.Rat)
	deviation := new(big.Rat)
	for _, x := range xs {
		deviation.SetFloat64(x)
		deviation.Sub(deviation, exactMean)
		m2.Add(m2, new(big.Rat).Mul(deviation, deviation))
	}

	meanOut, _ := exactMean.Float64()
	if len(xs) < 2 {
		return meanOut, math.NaN()
	}
	denominator := new(big.Rat).SetInt64(int64(len(xs) - 1))
	varianceOut, _ := new(big.Rat).Quo(m2, denominator).Float64()
	return meanOut, varianceOut
}

// relativeError reports |got-want|/|want|, or absolute error when want is zero.
func relativeError(got, want float64) float64 {
	if want == 0 {
		return math.Abs(got)
	}
	return math.Abs(got-want) / math.Abs(want)
}

// ---------------------------------------------------------------------------
// Baseline: the pre-promotion single-accumulator kernel
// ---------------------------------------------------------------------------

// momentsBlockedScalar is the pre-promotion kernel: the same blocked two-pass
// structure with a single accumulator per pass. Kept as the throughput and
// accuracy baseline the shipped kernel is measured against.
func momentsBlockedScalar(values []float64, validity []uint64) Moments {
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

		var sum float64
		count := 0
		for i := base; i < end; i++ {
			b := uint(i - base)
			sum += math.Float64frombits(math.Float64bits(values[i]) & maskBit(word, b))
			count += int((word >> b) & 1)
		}
		if count == 0 {
			continue
		}
		mean := sum / float64(count)

		var m2 float64
		for i := base; i < end; i++ {
			b := uint(i - base)
			deviation := values[i] - mean
			deviation = math.Float64frombits(math.Float64bits(deviation) & maskBit(word, b))
			m2 += deviation * deviation
		}

		acc = MergeMoments(acc, Moments{Count: count, Mean: mean, M2: m2})
	}
	return acc
}

// ---------------------------------------------------------------------------
// Candidate C: shifted single-pass with 4-way interleaved accumulators
// ---------------------------------------------------------------------------

// momentsShiftedMLP accumulates deviations from a per-window origin K in one
// pass, then recovers the window's moments algebraically.
//
// The naive kernel fails because Σx² and n·mean² are both huge while their
// difference is small. Shifting by any K near the data removes that entirely:
// the accumulated quantities are Σ(x−K) and Σ(x−K)², which stay the size of the
// window's spread rather than the size of its magnitude. K is the window's first
// present value, which is free to obtain and, for locally coherent data, close
// to the window mean.
func momentsShiftedMLP(values []float64, validity []uint64) Moments {
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

		origin := 0.0
		count := 0
		for i := base; i < end; i++ {
			if (word>>uint(i-base))&1 == 1 {
				origin = values[i]
				break
			}
		}
		for i := base; i < end; i++ {
			count += int((word >> uint(i-base)) & 1)
		}
		if count == 0 {
			continue
		}

		var s0, s1, s2, s3 float64
		var q0, q1, q2, q3 float64
		i := base
		for ; i+4 <= end; i += 4 {
			b := uint(i - base)
			e0 := math.Float64frombits(math.Float64bits(values[i]-origin) & maskBit(word, b))
			e1 := math.Float64frombits(math.Float64bits(values[i+1]-origin) & maskBit(word, b+1))
			e2 := math.Float64frombits(math.Float64bits(values[i+2]-origin) & maskBit(word, b+2))
			e3 := math.Float64frombits(math.Float64bits(values[i+3]-origin) & maskBit(word, b+3))
			s0 += e0
			s1 += e1
			s2 += e2
			s3 += e3
			q0 += e0 * e0
			q1 += e1 * e1
			q2 += e2 * e2
			q3 += e3 * e3
		}
		for ; i < end; i++ {
			b := uint(i - base)
			e := math.Float64frombits(math.Float64bits(values[i]-origin) & maskBit(word, b))
			s0 += e
			q0 += e * e
		}

		shiftedSum := (s0 + s1) + (s2 + s3)
		shiftedSquares := (q0 + q1) + (q2 + q3)
		countF := float64(count)
		mean := origin + shiftedSum/countF
		m2 := shiftedSquares - shiftedSum*shiftedSum/countF

		acc = MergeMoments(acc, Moments{Count: count, Mean: mean, M2: m2})
	}
	return acc
}

// ---------------------------------------------------------------------------
// Candidate D: naive, as the accuracy control
// ---------------------------------------------------------------------------

func momentsNaive(values []float64, validity []uint64) Moments {
	var sum, sumSq float64
	count := 0
	n := len(values)
	for w, word := range validity {
		base, end := wordSpan(w, n)
		if base >= end {
			break
		}
		for i := base; i < end; i++ {
			if (word>>uint(i-base))&1 == 1 {
				sum += values[i]
				sumSq += values[i] * values[i]
				count++
			}
		}
	}
	if count == 0 {
		return Moments{}
	}
	mean := sum / float64(count)
	return Moments{Count: count, Mean: mean, M2: sumSq - float64(count)*mean*mean}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

type momentsFixture struct {
	name     string
	values   []float64
	validity []uint64
	present  []float64
	// illConditioned marks fixtures whose magnitude dwarfs their spread, which
	// is the regime the naive kernel fails in and the only regime where the
	// choice between kernels is observable.
	illConditioned bool
}

func buildMomentsFixture(name string, n int, value func(i int) float64, valid func(i int) bool) momentsFixture {
	values := make([]float64, n)
	validity := make([]uint64, (n+63)/64)
	present := make([]float64, 0, n)
	for i := range values {
		if valid(i) {
			values[i] = value(i)
			validity[i/64] |= 1 << uint(i%64)
			present = append(present, values[i])
		} else {
			values[i] = math.NaN()
		}
	}
	return momentsFixture{name: name, values: values, validity: validity, present: present}
}

func markIllConditioned(f momentsFixture) momentsFixture {
	f.illConditioned = true
	return f
}

func momentsFixtures(n int) []momentsFixture {
	allValid := func(int) bool { return true }
	return []momentsFixture{
		buildMomentsFixture("benign", n, func(i int) float64 {
			return math.Sin(float64(i))*100 + 500
		}, allValid),
		// The cancellation case: magnitude 1e9, spread ~1. Sigma-x-squared and
		// n*mean^2 agree to ~17 significant digits; float64 has ~16.
		markIllConditioned(buildMomentsFixture("offset_1e9", n, func(i int) float64 {
			return 1e9 + math.Sin(float64(i))
		}, allValid)),
		// Worse still: magnitude 1e12.
		markIllConditioned(buildMomentsFixture("offset_1e12", n, func(i int) float64 {
			return 1e12 + math.Sin(float64(i))
		}, allValid)),
		buildMomentsFixture("wide_range", n, func(i int) float64 {
			return math.Pow(10, float64(i%16)-8)
		}, allValid),
		markIllConditioned(buildMomentsFixture("sparse_nulls", n, func(i int) float64 {
			return 1e9 + float64(i%97)
		}, func(i int) bool { return i%3 != 0 })),
	}
}

// ---------------------------------------------------------------------------
// Accuracy
// ---------------------------------------------------------------------------

// accuracyCeiling is the measured relative error of the shipped kernel on each
// fixture, with roughly one order of magnitude of headroom.
//
// These are ceilings, not derived bounds. The textbook two-pass bound
// (n·u + (u·κ)², Higham §1.9 / Chan-Golub-LeVeque) describes a single
// unblocked pass; this kernel blocks into 64-row windows and folds them with the
// monoid, so the bound does not transfer cleanly and pretending otherwise would
// be worse than measuring. They follow the same rule as the coverage ratchet:
// a ceiling only ever falls. If a change improves accuracy, lower it.
var accuracyCeiling = map[string]float64{
	"benign":       1e-14,
	"offset_1e9":   5e-9,
	"offset_1e12":  1e-6,
	"wide_range":   1e-14,
	"sparse_nulls": 5e-9,
}

// TestMomentsKernelAccuracyAgainstExactArithmetic gates the shipped kernel
// against exact arithmetic and keeps the rejected alternatives measurable beside
// it.
//
// The comparison rows are the point as much as the gate is. A future change that
// reaches for the naive (Σx, Σx²) form because it "does less work" has to walk
// past a row showing that form returning a negative variance — and past a
// benchmark showing it is slower anyway.
func TestMomentsKernelAccuracyAgainstExactArithmetic(t *testing.T) {
	kernels := []struct {
		name    string
		fn      func([]float64, []uint64) Moments
		shipped bool
	}{
		{"shipped(blocked+MLP)", momentsFloat64Valid, true},
		{"alt:shifted+MLP", momentsShiftedMLP, false},
		{"rejected:naive", momentsNaive, false},
	}

	for _, fixture := range momentsFixtures(4096) {
		_, wantVariance := exactMomentsFloat64(t, fixture.present)
		t.Logf("--- %s (exact variance = %.17g)", fixture.name, wantVariance)

		var shippedErr, naiveErr float64
		for _, kernel := range kernels {
			got, ok := kernel.fn(fixture.values, fixture.validity).Variance()
			if !ok {
				t.Fatalf("%s/%s: variance undefined", fixture.name, kernel.name)
			}
			err := relativeError(got, wantVariance)
			t.Logf("    %-22s variance=%-24.17g relerr=%.3e", kernel.name, got, err)

			if kernel.name == "rejected:naive" {
				naiveErr = err
			}
			if !kernel.shipped {
				continue
			}
			shippedErr = err

			// A negative variance is not an inaccuracy, it is an impossible
			// answer, and no tolerance may excuse it.
			if got < 0 {
				t.Errorf("%s/%s: returned a negative variance (%v)", fixture.name, kernel.name, got)
			}
			ceiling, known := accuracyCeiling[fixture.name]
			if !known {
				t.Fatalf("%s has no accuracy ceiling recorded", fixture.name)
			}
			if err > ceiling {
				t.Errorf("%s/%s: relative error %.3e exceeds the recorded ceiling %.1e",
					fixture.name, kernel.name, err, ceiling)
			}
		}

		// The rejection, restated as a property rather than a comment: on
		// ill-conditioned data the shipped kernel must be decisively better than
		// the form it refuses to use. If this ever stops holding, the refusal has
		// stopped being justified and the comments explaining it are stale.
		if fixture.illConditioned && naiveErr <= shippedErr*1e3 {
			t.Errorf("%s: naive relerr %.3e is not decisively worse than shipped %.3e; "+
				"the documented reason for rejecting it no longer holds",
				fixture.name, naiveErr, shippedErr)
		}
	}
}

// ---------------------------------------------------------------------------
// Throughput
// ---------------------------------------------------------------------------

func benchmarkMomentsKernel(b *testing.B, fn func([]float64, []uint64) Moments) {
	fixture := buildMomentsFixture("bench", 65536, func(i int) float64 {
		return 1e6 + math.Sin(float64(i))*1000
	}, func(int) bool { return true })

	b.SetBytes(int64(len(fixture.values) * 8))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sink := fn(fixture.values, fixture.validity)
		if sink.Count == 0 {
			b.Fatal("empty reduction")
		}
	}
}

func BenchmarkMomentsShipped(b *testing.B)        { benchmarkMomentsKernel(b, momentsFloat64Valid) }
func BenchmarkMomentsScalarBaseline(b *testing.B) { benchmarkMomentsKernel(b, momentsBlockedScalar) }
func BenchmarkMomentsShiftedMLP(b *testing.B)     { benchmarkMomentsKernel(b, momentsShiftedMLP) }
func BenchmarkMomentsNaive(b *testing.B)          { benchmarkMomentsKernel(b, momentsNaive) }

// BenchmarkMomentsSumValidBaseline reduces the same column with the existing
// first-moment kernel, so the cost of carrying a second moment is legible as a
// ratio rather than as a bare nanosecond count.
func BenchmarkMomentsSumValidBaseline(b *testing.B) {
	fixture := buildMomentsFixture("bench", 65536, func(i int) float64 {
		return 1e6 + math.Sin(float64(i))*1000
	}, func(int) bool { return true })

	b.SetBytes(int64(len(fixture.values) * 8))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sum, count := sumFloat64Valid(fixture.values, fixture.validity)
		if count == 0 || sum == 0 {
			b.Fatal("empty reduction")
		}
	}
}
