package hermes

import (
	"errors"
	"fmt"
)

var (
	// ErrColumnNotFound reports a missing column in the columnar table.
	ErrColumnNotFound = errors.New("hermes: column not found")

	// ErrInvalidPrimaryKey reports that the specified primary key column is not a string vector.
	ErrInvalidPrimaryKey = errors.New("hermes: primary key column must be StringVector")

	// ErrDuplicatePrimaryKey reports two rows with the same primary key.
	// A silent last-row-wins index would hide one of the rows.
	ErrDuplicatePrimaryKey = errors.New("hermes: duplicate primary key in columnar table")

	// ErrNullValue reports a null cell. A typed zero would be indistinguishable from a real 0 or "".
	ErrNullValue = errors.New("hermes: columnar cell is null")

	// ErrColumnType reports an accessor that does not match the column's vector type.
	ErrColumnType = errors.New("hermes: column has a different type")
)

// ColumnarTable provides contiguous (Go-heap) Arrow-style columnar serving with an indexed primary key lookup.
// It eliminates 'any' interface boxing, struct pointer graphs, and sync.Map overhead.
type ColumnarTable struct {
	batch    RecordBatch
	colMap   map[string]int
	pkIndex  map[string]int32
	pkColumn string
}

// NewColumnarTable builds an indexed serving table from an Arrow RecordBatch.
// It indexes the primary key column for O(1) point-lookups.
func NewColumnarTable(batch RecordBatch, pkColumn string) (*ColumnarTable, error) {
	colMap := make(map[string]int, len(batch.Columns))
	pkIdx := -1
	for i, col := range batch.Columns {
		colMap[col.Name] = i
		if col.Name == pkColumn {
			pkIdx = i
		}
	}
	if pkIdx == -1 {
		return nil, fmt.Errorf("%w: %s", ErrColumnNotFound, pkColumn)
	}

	strVec, ok := batch.Columns[pkIdx].Data.(*StringVector)
	if !ok {
		return nil, ErrInvalidPrimaryKey
	}

	pkIndex := make(map[string]int32, batch.Rows)
	for i := 0; i < batch.Rows; i++ {
		if !strVec.IsValid(i) {
			// A null key cannot be looked up, so it is not indexed (it used to collide on "").
			continue
		}
		key := strVec.ValueAt(i)
		if _, dup := pkIndex[key]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicatePrimaryKey, key)
		}
		pkIndex[key] = int32(i) // #nosec G115 -- row count is bounded by the projection limits
	}

	return &ColumnarTable{
		batch:    batch,
		colMap:   colMap,
		pkIndex:  pkIndex,
		pkColumn: pkColumn,
	}, nil
}

// Rows reports the number of rows in the table.
func (t *ColumnarTable) Rows() int {
	return t.batch.Rows
}

// GetView retrieves a zero-copy row view for the given primary key.
func (t *ColumnarTable) GetView(pk string) (ColumnarRowView, bool) {
	row, ok := t.pkIndex[pk]
	if !ok {
		return ColumnarRowView{}, false
	}
	return ColumnarRowView{table: t, row: int(row)}, true
}

// ColumnarRowView provides zero-allocation field accessors over an indexed Arrow row.
type ColumnarRowView struct {
	table *ColumnarTable
	row   int
}

// Row returns the row offset within the underlying RecordBatch.
func (v ColumnarRowView) Row() int {
	return v.row
}

// IsValid reports whether the specified column contains a valid value at this row.
func (v ColumnarRowView) IsValid(colName string) bool {
	idx, ok := v.table.colMap[colName]
	if !ok {
		return false
	}
	return v.table.batch.Columns[idx].Data.IsValid(v.row)
}

// String returns the string value for the specified column.
func (v ColumnarRowView) String(colName string) (string, error) {
	idx, ok := v.table.colMap[colName]
	if !ok {
		return "", ErrColumnNotFound
	}
	vec, ok := v.table.batch.Columns[idx].Data.(*StringVector)
	if !ok {
		return "", fmt.Errorf("%w: want StringVector", ErrColumnType)
	}
	if !vec.IsValid(v.row) {
		return "", ErrNullValue
	}
	return vec.ValueAt(v.row), nil
}

// Bytes returns a zero-copy byte slice view of the string buffer.
// The returned slice aliases vector memory and must not be mutated.
func (v ColumnarRowView) Bytes(colName string) ([]byte, error) {
	idx, ok := v.table.colMap[colName]
	if !ok {
		return nil, ErrColumnNotFound
	}
	vec, ok := v.table.batch.Columns[idx].Data.(*StringVector)
	if !ok {
		return nil, fmt.Errorf("%w: want StringVector", ErrColumnType)
	}
	if !vec.IsValid(v.row) {
		return nil, ErrNullValue
	}
	return vec.BytesSlice(v.row), nil
}

// Int64 returns the int64 value for the specified column.
func (v ColumnarRowView) Int64(colName string) (int64, error) {
	idx, ok := v.table.colMap[colName]
	if !ok {
		return 0, ErrColumnNotFound
	}
	vec, ok := v.table.batch.Columns[idx].Data.(*Int64Vector)
	if !ok {
		return 0, fmt.Errorf("%w: want Int64Vector", ErrColumnType)
	}
	if !vec.IsValid(v.row) {
		return 0, ErrNullValue
	}
	return vec.Int64Values()[v.row], nil
}

// Float64 returns the float64 value for the specified column.
func (v ColumnarRowView) Float64(colName string) (float64, error) {
	idx, ok := v.table.colMap[colName]
	if !ok {
		return 0, ErrColumnNotFound
	}
	vec, ok := v.table.batch.Columns[idx].Data.(*Float64Vector)
	if !ok {
		return 0, fmt.Errorf("%w: want Float64Vector", ErrColumnType)
	}
	if !vec.IsValid(v.row) {
		return 0, ErrNullValue
	}
	return vec.Float64Values()[v.row], nil
}

// MemoryFootprint calculates the total memory footprint of the table.
func (t *ColumnarTable) MemoryFootprint() (bufferBytes int, indexBytes int, totalBytes int, bytesPerRecord float64) {
	for _, col := range t.batch.Columns {
		switch v := col.Data.(type) {
		case *StringVector:
			bufferBytes += len(v.buf) + len(v.offsets)*4 + len(v.validity.words)*8
		case *Int64Vector:
			bufferBytes += len(v.values)*8 + len(v.validity.words)*8
		case *Float64Vector:
			bufferBytes += len(v.values)*8 + len(v.validity.words)*8
		case *TimestampVector:
			bufferBytes += len(v.values)*8 + len(v.validity.words)*8
		}
	}
	indexBytes = len(t.pkIndex) * 24
	totalBytes = bufferBytes + indexBytes
	if t.batch.Rows > 0 {
		bytesPerRecord = float64(totalBytes) / float64(t.batch.Rows)
	}
	return bufferBytes, indexBytes, totalBytes, bytesPerRecord
}
