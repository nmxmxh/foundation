package runtimehost

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Bounds tests for the columnar wire format.
//
// A columnar batch descriptor round-trips through a shared mapping, so every
// count and length read back out of the arena is untrusted input even when this
// process wrote it: the peer, a corrupted page, or a stale reuse can all present
// values the writer never produced. Each test below drives a header field to a
// value that used to wrap a uint32 size computation and turn a rejected batch
// into an out-of-range slice panic.

// writeOneColumnBatch produces a valid single-column batch and returns its
// descriptor id and slab so a test can corrupt one header field.
func writeOneColumnBatch(t *testing.T) (*Arena, uint32, []byte) {
	t.Helper()
	arena := testArena(t, generated.ARENA_MIN_BYTES)
	column, err := WriteFloat32Column(arena, 0, []float32{1, 2, 3})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	batchID, err := WriteColumnarBatch(arena, 3, []ColumnarField{column})
	if err != nil {
		t.Fatalf("WriteColumnarBatch: %v", err)
	}
	slab, err := arena.Slab(batchID)
	if err != nil {
		t.Fatalf("Slab: %v", err)
	}
	return arena, batchID, slab
}

// TestColumnarBatchRejectsOverflowingColumnCount is the regression that matters:
// COLUMNAR_FIELD_DESCRIPTOR_BYTES is 64, so a declared count of 2^26 multiplies
// to exactly 2^32 and wraps to zero. The required-size check then asks for only
// the 32-byte header, the batch is accepted, and the field loop walks off the
// end of the slab.
func TestColumnarBatchRejectsOverflowingColumnCount(t *testing.T) {
	arena, batchID, slab := writeOneColumnBatch(t)

	// Declared as a variable so the product is computed at run time: Go rejects
	// a constant expression that overflows its type at compile time.
	wrapping := uint32(1) << 26 // * 64 == 2^32 == 0 in uint32
	if wrapping*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES != 0 {
		t.Fatalf("precondition failed: %d columns should wrap the size product to 0", wrapping)
	}
	binary.LittleEndian.PutUint32(
		slab[generated.COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT*4:], wrapping)

	_, _, err := ReadColumnarBatch(arena, batchID)
	if err == nil {
		t.Fatal("a batch declaring 2^26 columns was accepted")
	}
}

// TestColumnarBatchRejectsColumnCountAboveMaximum covers the ordinary
// out-of-range case alongside the wrapping one.
func TestColumnarBatchRejectsColumnCountAboveMaximum(t *testing.T) {
	for _, columnCount := range []uint32{
		generated.COLUMNAR_BATCH_MAX_COLUMNS + 1,
		math.MaxUint32,
	} {
		arena, batchID, slab := writeOneColumnBatch(t)
		binary.LittleEndian.PutUint32(
			slab[generated.COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT*4:], columnCount)

		if _, _, err := ReadColumnarBatch(arena, batchID); err == nil {
			t.Errorf("a batch declaring %d columns was accepted", columnCount)
		}
	}
}

// TestColumnarBatchAcceptsMaximumColumnCountBoundary pins the accept/reject edge
// so the new guard cannot drift into rejecting a legal batch.
func TestColumnarBatchAcceptsMaximumColumnCountBoundary(t *testing.T) {
	arena := testArena(t, generated.ARENA_DEFAULT_BYTES)
	column, err := WriteFloat32Column(arena, 0, []float32{1})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	fields := make([]ColumnarField, generated.COLUMNAR_BATCH_MAX_COLUMNS)
	for i := range fields {
		fields[i] = column
	}
	batchID, err := WriteColumnarBatch(arena, 1, fields)
	if err != nil {
		t.Fatalf("WriteColumnarBatch at the column maximum: %v", err)
	}
	_, got, err := ReadColumnarBatch(arena, batchID)
	if err != nil {
		t.Fatalf("ReadColumnarBatch at the column maximum: %v", err)
	}
	if uint32(len(got)) != generated.COLUMNAR_BATCH_MAX_COLUMNS {
		t.Fatalf("read %d fields, want %d", len(got), generated.COLUMNAR_BATCH_MAX_COLUMNS)
	}
}

// TestWriteColumnarBatchRejectsTooManyColumns keeps the writer's bound in int
// space, where a slice longer than the maximum cannot truncate past the check.
func TestWriteColumnarBatchRejectsTooManyColumns(t *testing.T) {
	arena := testArena(t, generated.ARENA_DEFAULT_BYTES)
	column, err := WriteFloat32Column(arena, 0, []float32{1})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	fields := make([]ColumnarField, generated.COLUMNAR_BATCH_MAX_COLUMNS+1)
	for i := range fields {
		fields[i] = column
	}
	if _, err := WriteColumnarBatch(arena, 1, fields); err == nil {
		t.Fatal("a batch above the column maximum was accepted")
	}
}

// TestReadFloat32ColumnRejectsOverflowingLength drives the second wrap: a
// declared element count above MaxUint32/4 makes Length*4 in uint32 arithmetic
// smaller than the slab, so the size check passes and the read runs past the end.
func TestReadFloat32ColumnRejectsOverflowingLength(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)
	column, err := WriteFloat32Column(arena, 7, []float32{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}

	// Variable, not constant: see TestColumnarBatchRejectsOverflowingColumnCount.
	wrapping := uint32(1) << 30 // * 4 == 2^32 == 0 in uint32
	if wrapping*4 != 0 {
		t.Fatalf("precondition failed: length %d should wrap the byte product to 0", wrapping)
	}
	corrupt := column
	corrupt.Length = wrapping

	if _, err := ReadFloat32Column(arena, corrupt); err == nil {
		t.Fatal("a column declaring 2^30 elements was accepted")
	}
}

// TestReadFloat32ColumnRejectsLengthBeyondSlab covers the ordinary
// too-long-for-the-slab case that does not involve wrapping.
func TestReadFloat32ColumnRejectsLengthBeyondSlab(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)
	column, err := WriteFloat32Column(arena, 7, []float32{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	corrupt := column
	corrupt.Length = 1 << 20

	if _, err := ReadFloat32Column(arena, corrupt); err == nil {
		t.Fatal("a column longer than its slab was accepted")
	}
}

// TestReadFloat32ColumnRoundTripsUnchanged guards against the bounds work
// having tightened a legal read.
func TestReadFloat32ColumnRoundTripsUnchanged(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)
	want := []float32{0.5, -1.25, 3, 0}
	column, err := WriteFloat32Column(arena, 7, want)
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	got, err := ReadFloat32Column(arena, column)
	if err != nil {
		t.Fatalf("ReadFloat32Column: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestArenaRejectsRegionOutsideBounds pins both arena size bounds after the
// comparisons moved out of uint32 space.
func TestArenaRejectsRegionOutsideBounds(t *testing.T) {
	if _, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES-1)); err == nil {
		t.Error("an undersized arena region was accepted")
	}
	if _, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES)); err != nil {
		t.Errorf("the minimum arena region was rejected: %v", err)
	}
}

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

func TestWriteFloat32MatrixKeepsOneContiguousColumn(t *testing.T) {
	arena, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES))
	if err != nil {
		t.Fatalf("NewArenaOver() error = %v", err)
	}
	matrix, err := WriteFloat32Matrix(arena, 7, 2, 3, []float32{1, 2, 3, 4, 5, 6})
	if err != nil {
		t.Fatalf("WriteFloat32Matrix() error = %v", err)
	}
	if matrix.Field.Length != 6 || matrix.Rows != 2 || matrix.Dimensions != 3 {
		t.Fatalf("matrix = %+v", matrix)
	}
	values, err := ReadFloat32Column(arena, matrix.Field)
	if err != nil {
		t.Fatalf("ReadFloat32Column() error = %v", err)
	}
	if values[0] != 1 || values[5] != 6 {
		t.Fatalf("values = %v", values)
	}
}

func TestWriteFloat32MatrixRejectsInvalidShape(t *testing.T) {
	arena, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES))
	if err != nil {
		t.Fatalf("NewArenaOver() error = %v", err)
	}
	if _, err := WriteFloat32Matrix(arena, 7, 2, 3, []float32{1}); err == nil {
		t.Fatal("WriteFloat32Matrix() accepted wrong value count")
	}
	if _, err := WriteFloat32Matrix(arena, 7, 0, 3, nil); err == nil {
		t.Fatal("WriteFloat32Matrix() accepted zero rows")
	}
}
