package runtimehost

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Columnar batches are how bulk numeric work crosses the host boundary.
//
// The arena spec models this on Arrow's physical vocabulary — a record batch of
// fields sharing a row count, each field pointing at values/validity/offsets
// buffers, 64-byte aligned for SIMD. That is exactly the shape of the payloads
// that were previously being squeezed through the 1 KiB control payload: an
// embedding matrix is a fixed-width float32 field of rows x dimensions, and a
// ranking batch is a handful of parallel float64 columns.
//
// Writing them as a columnar batch rather than an ad-hoc envelope buys three
// things. The consumer reads a `&[f32]` view directly over the mapping with no
// deserialization; the alignment means that view is SIMD-safe on both sides; and
// the layout is the one the browser host already produces, so a kernel does not
// need a second decoder for the native path.

// maxFixedWidth4Elements is the largest element count whose 4-byte-wide byte
// length still fits in the uint32 the wire format uses. Guarding on it before
// the multiply is what keeps `count * 4` from wrapping to a small number and
// letting an oversized column past a size check.
const maxFixedWidth4Elements = math.MaxUint32 / 4

// ColumnarField describes one fixed-width column in a batch.
type ColumnarField struct {
	// FieldID identifies the column to the consumer.
	FieldID uint32
	// LogicalType is a COLUMNAR_LOGICAL_TYPE_* constant.
	LogicalType uint32
	// Length is the element count, not the byte count.
	Length uint32
	// ByteWidth is the size of one element.
	ByteWidth uint32
	// ValuesDescriptorID addresses the slab holding the values.
	ValuesDescriptorID uint32
}

// WriteFloat32Column copies a float32 column into its own slab and returns the
// field descriptor for it.
//
// A dedicated slab per column, rather than one interleaved buffer, is what makes
// the consumer's view zero-copy: a column is a contiguous run of one type at a
// page-aligned offset, so it maps to `&[f32]` without a stride or a gather.
func WriteFloat32Column(arena *Arena, fieldID uint32, values []float32) (ColumnarField, error) {
	if len(values) == 0 {
		return ColumnarField{}, fmt.Errorf("float32 column %d is empty", fieldID)
	}
	if len(values) > maxFixedWidth4Elements {
		return ColumnarField{}, fmt.Errorf("float32 column %d has %d elements, above the %d addressable in a uint32 byte length",
			fieldID, len(values), maxFixedWidth4Elements)
	}
	// #nosec G115 -- guarded above: len(values) <= MaxUint32/4, so both the
	// count and the count*4 byte length fit in a uint32 without wrapping.
	count := uint32(len(values))
	desc, slab, err := arena.Allocate(count*4, generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES)
	if err != nil {
		return ColumnarField{}, fmt.Errorf("allocate float32 column %d: %w", fieldID, err)
	}
	for i, v := range values {
		binary.LittleEndian.PutUint32(slab[i*4:i*4+4], math.Float32bits(v))
	}
	arena.Commit(desc)

	return ColumnarField{
		FieldID:            fieldID,
		LogicalType:        generated.COLUMNAR_LOGICAL_TYPE_FLOAT,
		Length:             count,
		ByteWidth:          4,
		ValuesDescriptorID: desc.ID,
	}, nil
}

// WriteUint32Column copies a uint32 column into its own slab.
func WriteUint32Column(arena *Arena, fieldID uint32, values []uint32) (ColumnarField, error) {
	if len(values) == 0 {
		return ColumnarField{}, fmt.Errorf("uint32 column %d is empty", fieldID)
	}
	if len(values) > maxFixedWidth4Elements {
		return ColumnarField{}, fmt.Errorf("uint32 column %d has %d elements, above the %d addressable in a uint32 byte length",
			fieldID, len(values), maxFixedWidth4Elements)
	}
	// #nosec G115 -- guarded above, see WriteFloat32Column.
	count := uint32(len(values))
	desc, slab, err := arena.Allocate(count*4, generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES)
	if err != nil {
		return ColumnarField{}, fmt.Errorf("allocate uint32 column %d: %w", fieldID, err)
	}
	for i, v := range values {
		binary.LittleEndian.PutUint32(slab[i*4:i*4+4], v)
	}
	arena.Commit(desc)

	return ColumnarField{
		FieldID:            fieldID,
		LogicalType:        generated.COLUMNAR_LOGICAL_TYPE_UINT,
		Length:             count,
		ByteWidth:          4,
		ValuesDescriptorID: desc.ID,
	}, nil
}

// WriteColumnarBatch writes the batch header and field table, returning the
// descriptor id the consumer needs. That id is the only thing the control buffer
// carries — four bytes standing in for however many megabytes the batch holds.
func WriteColumnarBatch(arena *Arena, rowCount uint32, fields []ColumnarField) (uint32, error) {
	if len(fields) == 0 {
		return 0, fmt.Errorf("columnar batch has no fields")
	}
	// Compared in int space: converting first would let a slice longer than
	// MaxUint32 truncate to a small count and pass the bound.
	if len(fields) > int(generated.COLUMNAR_BATCH_MAX_COLUMNS) {
		return 0, fmt.Errorf("columnar batch has %d columns, above the %d maximum",
			len(fields), generated.COLUMNAR_BATCH_MAX_COLUMNS)
	}
	// #nosec G115 -- guarded above: len(fields) <= COLUMNAR_BATCH_MAX_COLUMNS.
	columnCount := uint32(len(fields))

	size := generated.COLUMNAR_BATCH_HEADER_BYTES +
		columnCount*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
	desc, slab, err := arena.Allocate(size, generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH)
	if err != nil {
		return 0, fmt.Errorf("allocate columnar batch header: %w", err)
	}

	put := func(off, value uint32) {
		binary.LittleEndian.PutUint32(slab[off:off+4], value)
	}
	put(generated.COLUMNAR_BATCH_HEADER_IDX_MAGIC*4, generated.COLUMNAR_BATCH_MAGIC)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION*4, generated.COLUMNAR_BATCH_SCHEMA_VERSION)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT*4, rowCount)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT*4, columnCount)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_FLAGS*4, 0)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID*4, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID*4, generated.COLUMNAR_DESCRIPTOR_ID_NONE)

	// base advances by one descriptor per field rather than being recomputed
	// from the index, which removes the int->uint32 conversion entirely.
	base := generated.COLUMNAR_BATCH_HEADER_BYTES
	for _, field := range fields {
		putField := func(idx, value uint32) {
			off := base + idx*4
			binary.LittleEndian.PutUint32(slab[off:off+4], value)
		}
		putField(generated.COLUMNAR_FIELD_IDX_FIELD_ID, field.FieldID)
		putField(generated.COLUMNAR_FIELD_IDX_LOGICAL_TYPE, field.LogicalType)
		putField(generated.COLUMNAR_FIELD_IDX_PHYSICAL_TYPE, generated.COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH)
		putField(generated.COLUMNAR_FIELD_IDX_FLAGS, 0)
		putField(generated.COLUMNAR_FIELD_IDX_LENGTH, field.Length)
		putField(generated.COLUMNAR_FIELD_IDX_NULL_COUNT, 0)
		// No validity buffer: these columns are dense by construction. Saying so
		// explicitly keeps the consumer from reading a stale descriptor id.
		putField(generated.COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
		putField(generated.COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
		putField(generated.COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID, field.ValuesDescriptorID)
		putField(generated.COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
		putField(generated.COLUMNAR_FIELD_IDX_BYTE_WIDTH, field.ByteWidth)
		putField(generated.COLUMNAR_FIELD_IDX_DICTIONARY_ID, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
		base += generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
	}

	arena.Commit(desc)
	return desc.ID, nil
}

// ReadColumnarBatch reads a batch header and its field table back.
//
// Present so the producer can verify what it wrote and so a test can assert the
// wire form without a Rust process; the consumer side of this lives in
// ovrt-native.
func ReadColumnarBatch(arena *Arena, batchDescriptorID uint32) (uint32, []ColumnarField, error) {
	slab, err := arena.Slab(batchDescriptorID)
	if err != nil {
		return 0, nil, err
	}
	// Slab lengths are compared in int space throughout: converting a length to
	// uint32 first can truncate a large slab to a small one and invert the test.
	if len(slab) < int(generated.COLUMNAR_BATCH_HEADER_BYTES) {
		return 0, nil, fmt.Errorf("columnar batch header is %d bytes, want at least %d",
			len(slab), generated.COLUMNAR_BATCH_HEADER_BYTES)
	}
	get := func(off uint32) uint32 {
		return binary.LittleEndian.Uint32(slab[off : off+4])
	}
	if magic := get(generated.COLUMNAR_BATCH_HEADER_IDX_MAGIC * 4); magic != generated.COLUMNAR_BATCH_MAGIC {
		return 0, nil, fmt.Errorf("columnar batch magic %#x, want %#x", magic, generated.COLUMNAR_BATCH_MAGIC)
	}
	rowCount := get(generated.COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT * 4)
	columnCount := get(generated.COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT * 4)

	// columnCount comes off the wire, so it is bounded before it is used in any
	// arithmetic. Without this, a declared count above MaxUint32/64 wraps
	// `need` to a small value, passes the length check below, and then walks the
	// field loop straight off the end of the slab.
	if columnCount > generated.COLUMNAR_BATCH_MAX_COLUMNS {
		return 0, nil, fmt.Errorf("columnar batch declares %d columns, above the %d maximum",
			columnCount, generated.COLUMNAR_BATCH_MAX_COLUMNS)
	}

	need := generated.COLUMNAR_BATCH_HEADER_BYTES + columnCount*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
	if len(slab) < int(need) {
		return 0, nil, fmt.Errorf("columnar batch slab is %d bytes, want %d for %d columns",
			len(slab), need, columnCount)
	}

	fields := make([]ColumnarField, 0, columnCount)
	for i := range columnCount {
		base := generated.COLUMNAR_BATCH_HEADER_BYTES + i*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
		getField := func(idx uint32) uint32 { return get(base + idx*4) }
		fields = append(fields, ColumnarField{
			FieldID:            getField(generated.COLUMNAR_FIELD_IDX_FIELD_ID),
			LogicalType:        getField(generated.COLUMNAR_FIELD_IDX_LOGICAL_TYPE),
			Length:             getField(generated.COLUMNAR_FIELD_IDX_LENGTH),
			ByteWidth:          getField(generated.COLUMNAR_FIELD_IDX_BYTE_WIDTH),
			ValuesDescriptorID: getField(generated.COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID),
		})
	}
	return rowCount, fields, nil
}

// float32ColumnSlab resolves and bounds-checks a float32 column's value slab.
//
// Every float32 reader goes through here so the bound cannot drift between
// them: a reader that validated differently from ReadFloat32Column would be a
// second, untested parse of the same attacker-controlled descriptor.
func float32ColumnSlab(arena *Arena, field ColumnarField) ([]byte, error) {
	slab, err := arena.Slab(field.ValuesDescriptorID)
	if err != nil {
		return nil, err
	}
	// Widened to uint64 before the multiply: field.Length is attacker-controlled
	// once a descriptor round-trips through shared memory, and Length*4 in
	// uint32 wraps for any Length above MaxUint32/4 — which would shrink the
	// requirement to nothing and let a reader walk past the slab.
	wantBytes := uint64(field.Length) * 4
	if uint64(len(slab)) < wantBytes {
		return nil, fmt.Errorf("float32 column %d slab is %d bytes, want %d",
			field.FieldID, len(slab), wantBytes)
	}
	return slab, nil
}

// ReadFloat32Column reads a float32 column back out of the arena.
//
// This copies. When the caller only reduces the column, prefer a reducer such
// as SumFloat32Column, which reads the slab directly and allocates nothing.
func ReadFloat32Column(arena *Arena, field ColumnarField) ([]float32, error) {
	slab, err := float32ColumnSlab(arena, field)
	if err != nil {
		return nil, err
	}
	out := make([]float32, field.Length)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(slab[i*4 : i*4+4]))
	}
	return out, nil
}

// SumFloat32Column reduces a float32 column to its arithmetic sum without
// materializing the column.
//
// Three costs are avoided rather than one. The intermediate []float32 is never
// allocated; the reduction runs four independent accumulators instead of one,
// so the loop does not serialise on floating-point add latency; and because
// nothing is materialized, the working set stays the column itself rather than
// the column plus a copy of it. Measured on a 1M-row column: the naive shape —
// ReadFloat32Column followed by a single-accumulator sum — runs at ~1.0 GB/s
// and allocates 4 MB, this runs at ~12.4 GB/s and allocates nothing, a 12.2x
// difference. The interleaving mirrors sumFloat64sScalar in the hermes columnar
// engine; see docs/optimization_points.md item 60.
//
// The working-set effect is the part worth remembering, because it is larger
// than either of the other two and it is invisible in an allocation count. A
// 1M-row float32 column is exactly 4 MB, which is exactly this machine's L2.
// Allocating a copy doubles the footprint and evicts the source, so the naive
// shape pays DRAM latency on data that was already resident. The gain therefore
// shrinks on columns far below or far above the L2 size — 1K rows measures
// ~13x, but for a reason that will not hold on every target.
//
// A zero-copy view over the slab was measured and declined. It needs
// unsafe.Slice and hands the caller a window whose lifetime is tied to the
// arena mapping, and it measured *slower* than this fused loop (0.92 ms against
// 0.34 ms at 1M rows) because a view still leaves the caller iterating a second
// time. There is no version of that trade worth the hazard.
//
// This sits ahead of its consumer, deliberately. Go is the producer on the
// columnar arena path and ovrt-native is the reader, so nothing in Foundation
// reduces a float32 column today — the compute lane that would is the unbuilt
// half of the bridge described in docs/columnar_null_algebra.md. The reducer is
// here so that lane, or a generated application reading a result column back,
// does not have to rediscover the shape; it is not on any hot path yet.
//
// Floating-point addition is not associative, so this does not return the same
// bits as a left-to-right sum. Four accumulators make it *more* accurate than a
// sequential sum, not less; TestSumFloat32ColumnMatchesExactOracle bounds the
// result against an exact float64 reduction.
func SumFloat32Column(arena *Arena, field ColumnarField) (float32, error) {
	slab, err := float32ColumnSlab(arena, field)
	if err != nil {
		return 0, err
	}
	var s0, s1, s2, s3 float32
	count := int(field.Length)
	i := 0
	for ; i+4 <= count; i += 4 {
		word := slab[i*4 : i*4+16]
		s0 += math.Float32frombits(binary.LittleEndian.Uint32(word[0:4]))
		s1 += math.Float32frombits(binary.LittleEndian.Uint32(word[4:8]))
		s2 += math.Float32frombits(binary.LittleEndian.Uint32(word[8:12]))
		s3 += math.Float32frombits(binary.LittleEndian.Uint32(word[12:16]))
	}
	sum := (s0 + s1) + (s2 + s3)
	// Tail: up to three rows when the count is not a multiple of four.
	for ; i < count; i++ {
		sum += math.Float32frombits(binary.LittleEndian.Uint32(slab[i*4 : i*4+4]))
	}
	return sum, nil
}
