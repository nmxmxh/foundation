package hermes

import (
	"math"
	"math/rand/v2"
	"testing"
)

// Tests for the null algebra: identity substitution must be observationally
// equal to a naive "skip the nulls" reference for every operation, and the
// (value, count) pair must separate an all-null column from a column whose real
// extremum happens to equal the identity.

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// buildInt64Vec builds an Int64Vector of n rows where valid(i) decides presence.
// Null slots deliberately carry poison rather than zero, so any kernel that
// reads a null value instead of substituting the identity produces a wrong
// answer rather than an accidentally-right one.
func buildInt64Vec(n int, valid func(i int) bool, value func(i int) int64) *Int64Vector {
	v := newInt64Vector(n)
	for i := range v.values {
		if valid(i) {
			v.values[i] = value(i)
			v.validity.set(i)
			continue
		}
		v.values[i] = math.MaxInt64 / 3 // poison
	}
	return v
}

func buildFloat64Vec(n int, valid func(i int) bool, value func(i int) float64) *Float64Vector {
	v := newFloat64Vector(n)
	for i := range v.values {
		if valid(i) {
			v.values[i] = value(i)
			v.validity.set(i)
			continue
		}
		v.values[i] = math.NaN() // poison: leaks as NaN if ever read
	}
	return v
}

// naive references: the obvious branchy "skip nulls" implementations.

func refSumInt64(v *Int64Vector) (int64, int) {
	var sum int64
	count := 0
	for i := range v.values {
		if v.IsValid(i) {
			sum += v.values[i]
			count++
		}
	}
	return sum, count
}

func refExtremumInt64(v *Int64Vector, wantMin bool) (int64, int) {
	var best int64
	count := 0
	for i := range v.values {
		if !v.IsValid(i) {
			continue
		}
		x := v.values[i]
		if count == 0 || (wantMin && x < best) || (!wantMin && x > best) {
			best = x
		}
		count++
	}
	return best, count
}

func refSumFloat64(v *Float64Vector) (float64, int) {
	var sum float64
	count := 0
	for i := range v.values {
		if v.IsValid(i) {
			sum += v.values[i]
			count++
		}
	}
	return sum, count
}

func refExtremumFloat64(v *Float64Vector, wantMin bool) (float64, int) {
	best := math.Inf(1)
	if !wantMin {
		best = math.Inf(-1)
	}
	count := 0
	for i := range v.values {
		if !v.IsValid(i) {
			continue
		}
		x := v.values[i]
		if (wantMin && x < best) || (!wantMin && x > best) {
			best = x
		}
		count++
	}
	return best, count
}

// reduceLengths exercises the word boundary, the sub-word tail, and the 4-way
// interleave remainder inside the dense fast path.
var reduceLengths = []int{0, 1, 2, 3, 4, 5, 7, 8, 63, 64, 65, 127, 128, 129, 200, 1000}

// densities: all-null, sparse, half, dense-with-holes, all-valid.
var reduceDensities = []struct {
	name  string
	valid func(i int) bool
}{
	{"all-null", func(int) bool { return false }},
	{"sparse", func(i int) bool { return i%17 == 0 }},
	{"half", func(i int) bool { return i%2 == 0 }},
	{"dense-holes", func(i int) bool { return i%64 != 5 }},
	{"all-valid", func(int) bool { return true }},
}

// ---------------------------------------------------------------------------
// parity: identity substitution == naive null-skipping
// ---------------------------------------------------------------------------

func TestInt64ReductionsMatchNaiveReference(t *testing.T) {
	for _, n := range reduceLengths {
		for _, d := range reduceDensities {
			vec := buildInt64Vec(n, d.valid, func(i int) int64 { return int64(i)*7 - 300 })

			wantSum, wantCount := refSumInt64(vec)
			if got := vec.SumValid(); got.Value != wantSum || got.Count != wantCount {
				t.Errorf("n=%d %s: SumValid=%v want {%d %d}", n, d.name, got, wantSum, wantCount)
			}
			if got := vec.CountValid(); got != wantCount {
				t.Errorf("n=%d %s: CountValid=%d want %d", n, d.name, got, wantCount)
			}

			wantMin, _ := refExtremumInt64(vec, true)
			got := vec.MinValid()
			if got.Count != wantCount || (wantCount > 0 && got.Value != wantMin) {
				t.Errorf("n=%d %s: MinValid=%v want {%d %d}", n, d.name, got, wantMin, wantCount)
			}

			wantMax, _ := refExtremumInt64(vec, false)
			got = vec.MaxValid()
			if got.Count != wantCount || (wantCount > 0 && got.Value != wantMax) {
				t.Errorf("n=%d %s: MaxValid=%v want {%d %d}", n, d.name, got, wantMax, wantCount)
			}
		}
	}
}

func TestFloat64ReductionsMatchNaiveReference(t *testing.T) {
	for _, n := range reduceLengths {
		for _, d := range reduceDensities {
			vec := buildFloat64Vec(n, d.valid, func(i int) float64 { return float64(i)*1.5 - 100 })

			wantSum, wantCount := refSumFloat64(vec)
			got := vec.SumValid()
			if got.Count != wantCount || got.Value != wantSum {
				t.Errorf("n=%d %s: SumValid=%v want {%v %d}", n, d.name, got, wantSum, wantCount)
			}

			wantMin, _ := refExtremumFloat64(vec, true)
			gotMin := vec.MinValid()
			if gotMin.Count != wantCount || (wantCount > 0 && gotMin.Value != wantMin) {
				t.Errorf("n=%d %s: MinValid=%v want {%v %d}", n, d.name, gotMin, wantMin, wantCount)
			}

			wantMax, _ := refExtremumFloat64(vec, false)
			gotMax := vec.MaxValid()
			if gotMax.Count != wantCount || (wantCount > 0 && gotMax.Value != wantMax) {
				t.Errorf("n=%d %s: MaxValid=%v want {%v %d}", n, d.name, gotMax, wantMax, wantCount)
			}

			gotMean := vec.MeanValid()
			if wantCount == 0 {
				if !gotMean.IsNull() {
					t.Errorf("n=%d %s: MeanValid=%v want null", n, d.name, gotMean)
				}
			} else if want := wantSum / float64(wantCount); gotMean.Value != want {
				t.Errorf("n=%d %s: MeanValid=%v want %v", n, d.name, gotMean, want)
			}
		}
	}
}

func TestReductionsMatchReferenceOnRandomValidity(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 17))
	for trial := range 200 {
		n := r.IntN(300)
		vec := buildInt64Vec(n,
			func(int) bool { return r.IntN(4) != 0 },
			func(int) int64 { return int64(r.Int64N(1<<40)) - (1 << 39) },
		)
		wantSum, wantCount := refSumInt64(vec)
		if got := vec.SumValid(); got.Value != wantSum || got.Count != wantCount {
			t.Fatalf("trial %d n=%d: SumValid=%v want {%d %d}", trial, n, got, wantSum, wantCount)
		}
		wantMin, _ := refExtremumInt64(vec, true)
		if got := vec.MinValid(); got.Count != wantCount || (wantCount > 0 && got.Value != wantMin) {
			t.Fatalf("trial %d n=%d: MinValid=%v want {%d %d}", trial, n, got, wantMin, wantCount)
		}
	}
}

// ---------------------------------------------------------------------------
// the reason Count exists
// ---------------------------------------------------------------------------

// TestExtremumIdentityCollision is the case a bare value cannot express: a
// column whose real maximum is exactly MinInt64 (the max-identity) must be
// distinguishable from a column with no values at all.
func TestExtremumIdentityCollision(t *testing.T) {
	real := buildInt64Vec(4, func(int) bool { return true }, func(int) int64 { return math.MinInt64 })
	empty := buildInt64Vec(4, func(int) bool { return false }, func(int) int64 { return 0 })

	gotReal, gotEmpty := real.MaxValid(), empty.MaxValid()
	if gotReal.Value != math.MinInt64 || gotReal.Count != 4 {
		t.Fatalf("real MinInt64 column: MaxValid=%v want {%d 4}", gotReal, int64(math.MinInt64))
	}
	if !gotEmpty.IsNull() {
		t.Fatalf("all-null column: MaxValid=%v want IsNull", gotEmpty)
	}
	if gotReal.Value != gotEmpty.Value {
		t.Fatalf("precondition failed: the two values should collide, got %d and %d", gotReal.Value, gotEmpty.Value)
	}
	// Same collision at the other end.
	realMin := buildInt64Vec(4, func(int) bool { return true }, func(int) int64 { return math.MaxInt64 })
	if got := realMin.MinValid(); got.Value != math.MaxInt64 || got.Count != 4 {
		t.Fatalf("real MaxInt64 column: MinValid=%v want {%d 4}", got, int64(math.MaxInt64))
	}
	if got := empty.MinValid(); !got.IsNull() {
		t.Fatalf("all-null column: MinValid=%v want IsNull", got)
	}
}

// TestNullSlotsAreNeverRead pins identity substitution: null slots hold poison,
// so a kernel that reads them cannot produce the reference answer.
func TestNullSlotsAreNeverRead(t *testing.T) {
	vec := buildInt64Vec(100, func(i int) bool { return i%3 == 0 }, func(i int) int64 { return int64(i) })
	want, wantCount := refSumInt64(vec)
	if got := vec.SumValid(); got.Value != want || got.Count != wantCount {
		t.Fatalf("SumValid=%v want {%d %d} (poison leaked from a null slot)", got, want, wantCount)
	}
}

// ---------------------------------------------------------------------------
// documented float semantics
// ---------------------------------------------------------------------------

// TestFloatNaNSemantics pins the asymmetry documented on the kernels: NaN in a
// present row propagates through a sum but is ignored by min/max, because both
// comparisons against NaN are false.
func TestFloatNaNSemantics(t *testing.T) {
	vec := newFloat64Vector(4)
	values := []float64{1, math.NaN(), 3, 2}
	for i, x := range values {
		vec.values[i] = x
		vec.validity.set(i)
	}
	if got := vec.SumValid(); !math.IsNaN(got.Value) {
		t.Errorf("SumValid=%v want NaN (NaN propagates through +)", got)
	}
	if got := vec.MinValid(); got.Value != 1 || got.Count != 4 {
		t.Errorf("MinValid=%v want {1 4} (NaN ignored by comparison)", got)
	}
	if got := vec.MaxValid(); got.Value != 3 || got.Count != 4 {
		t.Errorf("MaxValid=%v want {3 4} (NaN ignored by comparison)", got)
	}
}

// TestFloatInfinityIsNotDestroyedByMasking guards the bit-mask choice: a 0/1
// multiply would turn a present ±Inf into NaN, clearing the bit pattern does not.
func TestFloatInfinityIsNotDestroyedByMasking(t *testing.T) {
	vec := buildFloat64Vec(64, func(i int) bool { return i%2 == 0 }, func(i int) float64 {
		if i == 0 {
			return math.Inf(1)
		}
		return 1
	})
	if got := vec.SumValid(); !math.IsInf(got.Value, 1) {
		t.Errorf("SumValid=%v want +Inf", got)
	}
	if got := vec.MaxValid(); !math.IsInf(got.Value, 1) {
		t.Errorf("MaxValid=%v want +Inf", got)
	}
}

// TestAllNullReductionsReportIdentity documents what Value holds when Count is
// zero, so callers reading it by mistake see a defined answer.
func TestAllNullReductionsReportIdentity(t *testing.T) {
	iv := buildInt64Vec(70, func(int) bool { return false }, func(int) int64 { return 0 })
	if got := iv.SumValid(); got.Value != 0 || !got.IsNull() {
		t.Errorf("int64 SumValid=%v want {0 0}", got)
	}
	if got := iv.MinValid(); got.Value != math.MaxInt64 || !got.IsNull() {
		t.Errorf("int64 MinValid=%v want {MaxInt64 0}", got)
	}
	if got := iv.MaxValid(); got.Value != math.MinInt64 || !got.IsNull() {
		t.Errorf("int64 MaxValid=%v want {MinInt64 0}", got)
	}

	fv := buildFloat64Vec(70, func(int) bool { return false }, func(int) float64 { return 0 })
	if got := fv.MinValid(); !math.IsInf(got.Value, 1) || !got.IsNull() {
		t.Errorf("float64 MinValid=%v want {+Inf 0}", got)
	}
	if got := fv.MaxValid(); !math.IsInf(got.Value, -1) || !got.IsNull() {
		t.Errorf("float64 MaxValid=%v want {-Inf 0}", got)
	}
}

// ---------------------------------------------------------------------------
// bitmap unification
// ---------------------------------------------------------------------------

// TestValidityAndSelectionShareBitmapSemantics is the point of the merge: the
// two masks are one structure, so a validity mask and a selection mask agree
// bit for bit and intersect in bulk.
func TestValidityAndSelectionShareBitmapSemantics(t *testing.T) {
	const n = 200
	vec := buildInt64Vec(n, func(i int) bool { return i%3 != 0 }, func(i int) int64 { return int64(i) })

	sel := NewSelectionBitmap(n)
	for i := range n {
		sel.set(i)
	}
	sel.maskValidity(vec)

	if got, want := sel.Count(), vec.CountValid(); got != want {
		t.Fatalf("selection∩validity count=%d want validCount=%d", got, want)
	}
	for i := range n {
		if sel.IsSelected(i) != vec.IsValid(i) {
			t.Fatalf("row %d: selected=%v valid=%v", i, sel.IsSelected(i), vec.IsValid(i))
		}
	}
}

// TestComplementResurrectsNulls pins the trap documented on Not(): complementing
// a validity-masked selection brings null rows back, because under two-valued
// logic "did not match" and "had no value" are the same bit.
func TestComplementResurrectsNulls(t *testing.T) {
	const n = 100
	vec := buildInt64Vec(n, func(i int) bool { return i%3 != 0 }, func(i int) int64 { return int64(i) })

	sel := NewSelectionBitmap(n)
	sel.maskValidity(vec) // empty selection, validity-masked
	sel.Not()             // complement: everything, including nulls

	nulls := 0
	for i := range n {
		if sel.IsSelected(i) && !vec.IsValid(i) {
			nulls++
		}
	}
	if nulls == 0 {
		t.Fatal("expected complement to re-select null rows; the documented trap did not reproduce")
	}
	// The documented remedy restores the invariant.
	sel.maskValidity(vec)
	for i := range n {
		if sel.IsSelected(i) && !vec.IsValid(i) {
			t.Fatalf("row %d still selected after re-masking validity", i)
		}
	}
}

// ---------------------------------------------------------------------------
// benchmarks
// ---------------------------------------------------------------------------

const reduceBenchRows = 1 << 16

func benchInt64Vec(valid func(i int) bool) *Int64Vector {
	return buildInt64Vec(reduceBenchRows, valid, func(i int) int64 { return int64(i) })
}

// sumInt64Oblivious is the null-oblivious control. It uses the same 4-way
// interleave as the real kernel so the only difference measured against
// SumValid is validity handling, not instruction-level parallelism.
func sumInt64Oblivious(xs []int64) int64 {
	var s0, s1, s2, s3 int64
	i := 0
	for ; i+4 <= len(xs); i += 4 {
		s0 += xs[i]
		s1 += xs[i+1]
		s2 += xs[i+2]
		s3 += xs[i+3]
	}
	sum := (s0 + s1) + (s2 + s3)
	for ; i < len(xs); i++ {
		sum += xs[i]
	}
	return sum
}

// BenchmarkSumInt64NullOblivious is the control: same loop shape, validity
// never consulted. SumValid on an all-valid column should land on top of this.
func BenchmarkSumInt64NullOblivious(b *testing.B) {
	vec := benchInt64Vec(func(int) bool { return true })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = sumInt64Oblivious(vec.values)
	}
}

// BenchmarkSumInt64SingleAccumulator isolates the interleave itself, so the
// control above is not mistaken for a claim about null handling.
func BenchmarkSumInt64SingleAccumulator(b *testing.B) {
	vec := benchInt64Vec(func(int) bool { return true })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		var sum int64
		for _, x := range vec.values {
			sum += x
		}
		_ = sum
	}
}

// BenchmarkSumInt64ValidDense measures the all-valid path, where identity
// substitution is skipped entirely per word.
func BenchmarkSumInt64ValidDense(b *testing.B) {
	vec := benchInt64Vec(func(int) bool { return true })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.SumValid()
	}
}

// BenchmarkSumInt64ValidHoles measures the masked path: one null per word forces
// every word off the dense fast path.
func BenchmarkSumInt64ValidHoles(b *testing.B) {
	vec := benchInt64Vec(func(i int) bool { return i%64 != 5 })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.SumValid()
	}
}

// BenchmarkSumInt64ValidSparse measures the skip path: most validity words are
// zero and are rejected in one compare without touching values.
func BenchmarkSumInt64ValidSparse(b *testing.B) {
	vec := benchInt64Vec(func(i int) bool { return i%512 == 0 })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.SumValid()
	}
}

// BenchmarkSumInt64BranchyHoles is the naive per-row validity branch over the
// same regular pattern. i%64 != 5 is perfectly predictable, which is the best
// possible case for a branch, so this pair understates the branch-free win.
func BenchmarkSumInt64BranchyHoles(b *testing.B) {
	vec := benchInt64Vec(func(i int) bool { return i%64 != 5 })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_, _ = refSumInt64(vec)
	}
}

// randomValidity is an unpredictable ~50% validity pattern: the case a branch
// predictor cannot learn, and therefore the case identity substitution exists
// for. Seeded so the shape is identical across runs and across the pair below.
func randomValidity() func(i int) bool {
	r := rand.New(rand.NewPCG(0xA5, 0x5A))
	flags := make([]bool, reduceBenchRows)
	for i := range flags {
		flags[i] = r.IntN(2) == 0
	}
	return func(i int) bool { return flags[i] }
}

// BenchmarkSumInt64ValidRandom measures identity substitution over
// unpredictable validity: the mask costs the same regardless of the pattern.
func BenchmarkSumInt64ValidRandom(b *testing.B) {
	vec := benchInt64Vec(randomValidity())
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.SumValid()
	}
}

// BenchmarkSumInt64BranchyRandom is the same data through the per-row branch,
// where every row is a coin flip the predictor gets wrong half the time.
func BenchmarkSumInt64BranchyRandom(b *testing.B) {
	vec := benchInt64Vec(randomValidity())
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_, _ = refSumInt64(vec)
	}
}

// BenchmarkCountValid shows the count component is a bitmap scan, not a data
// scan: it reads 1/64th of the bytes the value buffer occupies.
func BenchmarkCountValid(b *testing.B) {
	vec := benchInt64Vec(func(i int) bool { return i%3 != 0 })
	b.SetBytes(reduceBenchRows / 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.CountValid()
	}
}

func BenchmarkMaxInt64Valid(b *testing.B) {
	vec := benchInt64Vec(func(i int) bool { return i%64 != 5 })
	b.SetBytes(reduceBenchRows * 8)
	b.ResetTimer()
	for b.Loop() {
		_ = vec.MaxValid()
	}
}
