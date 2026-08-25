package runtimehost

import (
	"math"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Tests for SumFloat32Column.
//
// The risk this reduction carries is not "is the arithmetic right" — it is that
// a four-way interleaved loop has a tail. A column whose length is not a
// multiple of four exits the main loop with one to three rows unread, and a tail
// that is dropped or double-counted produces a plausible wrong number rather
// than a crash. The boundary test below walks every residue class.
//
// The second risk is bound drift: SumFloat32Column reads the same
// attacker-controlled descriptor as ReadFloat32Column, so it must reject
// exactly what ReadFloat32Column rejects. Those cases are asserted against both
// readers together rather than restated, so a future change cannot tighten one
// and leave the other open.

// exactSumFloat32 is the oracle: every element widened to float64 and summed
// sequentially. float64 carries 53 bits of mantissa against float32's 24, so
// for the magnitudes used here this accumulates without loss and is the value
// both the interleaved and the sequential float32 sums approximate.
func exactSumFloat32(values []float32) float64 {
	var sum float64
	for _, v := range values {
		sum += float64(v)
	}
	return sum
}

// naiveSumFloat32 is the single-accumulator shape SumFloat32Column replaces.
// Present so the test can show the interleaved result is at least as close to
// the oracle, not merely close.
func naiveSumFloat32(values []float32) float32 {
	var sum float32
	for _, v := range values {
		sum += v
	}
	return sum
}

func writeFloat32Fixture(t *testing.T, values []float32) (*Arena, ColumnarField) {
	t.Helper()
	needed := uint32(len(values))*4 + generated.ARENA_MIN_BYTES
	arena := testArena(t, needed)
	field, err := WriteFloat32Column(arena, 7, values)
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	return arena, field
}

// TestSumFloat32ColumnCoversInterleaveBoundaries walks the residue classes of
// the four-way loop. Lengths 1..9 cover every tail size (0, 1, 2, 3) at least
// twice, including the length-4 case where the tail loop must not run at all
// and the length-8 case where the main loop runs twice.
func TestSumFloat32ColumnCoversInterleaveBoundaries(t *testing.T) {
	for count := 1; count <= 9; count++ {
		values := make([]float32, count)
		for i := range values {
			// Distinct powers of two: every element is exactly representable,
			// so the expected sum is exact and a dropped or repeated tail
			// element changes the result unambiguously.
			values[i] = float32(int(1) << uint(i))
		}
		arena, field := writeFloat32Fixture(t, values)

		got, err := SumFloat32Column(arena, field)
		if err != nil {
			t.Fatalf("count %d: SumFloat32Column: %v", count, err)
		}
		want := float32(exactSumFloat32(values))
		if got != want {
			t.Fatalf("count %d: sum = %v, want exactly %v (tail handling)", count, got, want)
		}
	}
}

// TestSumFloat32ColumnMatchesExactOracle bounds the interleaved result against
// an exact float64 reduction over a column large enough that rounding order
// matters, and asserts the interleaved sum is no worse than the sequential one.
func TestSumFloat32ColumnMatchesExactOracle(t *testing.T) {
	const count = 4096
	values := make([]float32, count)
	for i := range values {
		// Mixed magnitudes: a large leading term followed by many small ones is
		// the classic case where a single accumulator loses the small terms.
		if i == 0 {
			values[i] = 1 << 20
			continue
		}
		values[i] = 0.125
	}
	arena, field := writeFloat32Fixture(t, values)

	got, err := SumFloat32Column(arena, field)
	if err != nil {
		t.Fatalf("SumFloat32Column: %v", err)
	}

	exact := exactSumFloat32(values)
	naive := naiveSumFloat32(values)
	interleavedErr := math.Abs(float64(got) - exact)
	naiveErr := math.Abs(float64(naive) - exact)

	// Hard bound: within one part in 2^20 of exact, which is far inside
	// float32's 24-bit mantissa for this magnitude.
	if tolerance := math.Abs(exact) / (1 << 20); interleavedErr > tolerance {
		t.Fatalf("sum = %v, exact = %v, error %v exceeds tolerance %v", got, exact, interleavedErr, tolerance)
	}
	if interleavedErr > naiveErr {
		t.Fatalf("interleaved error %v is worse than sequential error %v; four accumulators must not lose accuracy",
			interleavedErr, naiveErr)
	}
}

// TestSumFloat32ColumnAllocatesNothing asserts the no-allocation claim as
// behaviour rather than leaving it to a benchmark, per TE-18: the reduction
// exists specifically to avoid materializing the column, and a future change
// that reintroduces an intermediate slice must fail here.
func TestSumFloat32ColumnAllocatesNothing(t *testing.T) {
	values := make([]float32, 1024)
	for i := range values {
		values[i] = float32(i) * 0.5
	}
	arena, field := writeFloat32Fixture(t, values)

	allocs := testing.AllocsPerRun(100, func() {
		if _, err := SumFloat32Column(arena, field); err != nil {
			t.Fatalf("SumFloat32Column: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("SumFloat32Column allocated %v times per run, want 0", allocs)
	}
}

// TestFloat32ReadersRejectTheSameCorruptDescriptors keeps the two readers'
// bounds in lockstep. Both resolve the same descriptor through
// float32ColumnSlab; if a change gives one of them a different bound, the
// weaker one becomes an unguarded parse of attacker-controlled input.
func TestFloat32ReadersRejectTheSameCorruptDescriptors(t *testing.T) {
	// Variable, not constant: 1<<30 * 4 wraps to 0 in uint32, which would make
	// the byte requirement vanish. See TestReadFloat32ColumnRejectsOverflowingLength.
	wrapping := uint32(1) << 30
	if wrapping*4 != 0 {
		t.Fatalf("precondition failed: length %d should wrap the byte product to 0", wrapping)
	}

	cases := []struct {
		name   string
		length uint32
	}{
		{"length wraps the byte product", wrapping},
		{"length beyond the slab", 1 << 20},
		{"one element past the slab", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena, field := writeFloat32Fixture(t, []float32{1, 2, 3, 4})
			corrupt := field
			corrupt.Length = tc.length

			_, readErr := ReadFloat32Column(arena, corrupt)
			_, sumErr := SumFloat32Column(arena, corrupt)

			if readErr == nil {
				t.Error("ReadFloat32Column accepted a corrupt descriptor")
			}
			if sumErr == nil {
				t.Error("SumFloat32Column accepted a corrupt descriptor")
			}
		})
	}
}

// TestSumFloat32ColumnAgreesWithReadThenSum is the black-box equivalence: the
// reduction must be interchangeable with the copying read for any legal column.
func TestSumFloat32ColumnAgreesWithReadThenSum(t *testing.T) {
	values := []float32{0.5, -1.25, 3, 0, 7.5, -0.0625, 2}
	arena, field := writeFloat32Fixture(t, values)

	got, err := SumFloat32Column(arena, field)
	if err != nil {
		t.Fatalf("SumFloat32Column: %v", err)
	}
	back, err := ReadFloat32Column(arena, field)
	if err != nil {
		t.Fatalf("ReadFloat32Column: %v", err)
	}
	want := float32(exactSumFloat32(back))
	if got != want {
		t.Fatalf("reduction = %v, read-then-sum = %v", got, want)
	}
}
