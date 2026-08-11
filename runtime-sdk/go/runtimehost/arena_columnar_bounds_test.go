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
