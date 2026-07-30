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
	byteLen := uint32(len(values)) * 4
	desc, slab, err := arena.Allocate(byteLen, generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES)
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
		Length:             uint32(len(values)),
		ByteWidth:          4,
		ValuesDescriptorID: desc.ID,
	}, nil
}

// WriteUint32Column copies a uint32 column into its own slab.
func WriteUint32Column(arena *Arena, fieldID uint32, values []uint32) (ColumnarField, error) {
	if len(values) == 0 {
		return ColumnarField{}, fmt.Errorf("uint32 column %d is empty", fieldID)
	}
	byteLen := uint32(len(values)) * 4
	desc, slab, err := arena.Allocate(byteLen, generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES)
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
		Length:             uint32(len(values)),
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
	if uint32(len(fields)) > generated.COLUMNAR_BATCH_MAX_COLUMNS {
		return 0, fmt.Errorf("columnar batch has %d columns, above the %d maximum",
			len(fields), generated.COLUMNAR_BATCH_MAX_COLUMNS)
	}

	size := generated.COLUMNAR_BATCH_HEADER_BYTES +
		uint32(len(fields))*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
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
	put(generated.COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT*4, uint32(len(fields)))
	put(generated.COLUMNAR_BATCH_HEADER_IDX_FLAGS*4, 0)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID*4, generated.COLUMNAR_DESCRIPTOR_ID_NONE)
	put(generated.COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID*4, generated.COLUMNAR_DESCRIPTOR_ID_NONE)

	for i, field := range fields {
		base := generated.COLUMNAR_BATCH_HEADER_BYTES +
			uint32(i)*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
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
	if uint32(len(slab)) < generated.COLUMNAR_BATCH_HEADER_BYTES {
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

	need := generated.COLUMNAR_BATCH_HEADER_BYTES + columnCount*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
	if uint32(len(slab)) < need {
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

// ReadFloat32Column reads a float32 column back out of the arena.
func ReadFloat32Column(arena *Arena, field ColumnarField) ([]float32, error) {
	slab, err := arena.Slab(field.ValuesDescriptorID)
	if err != nil {
		return nil, err
	}
	if uint32(len(slab)) < field.Length*4 {
		return nil, fmt.Errorf("float32 column %d slab is %d bytes, want %d",
			field.FieldID, len(slab), field.Length*4)
	}
	out := make([]float32, field.Length)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(slab[i*4 : i*4+4]))
	}
	return out, nil
}
