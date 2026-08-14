package hermes

// Bitmap-predicate selection over columnar batches.
//
// This is the software form of the processing-using-memory insight (RowClone/
// Ambit lineage, promoted from future_practices_research.md lane 7): evaluate
// multi-filter queries as bulk bitwise AND/OR over packed selection bitmaps
// first, and touch record memory only for the surviving row positions. Each
// predicate scans one contiguous column buffer once; every merge after that is
// word-at-a-time uint64 arithmetic running at memory bandwidth, and Count
// compiles to POPCNT/CNT via bits.OnesCount64 like the validity bitmap does.
//
// Null semantics follow SQL's practical reading: a null cell never matches any
// predicate, so every constructor intersects with the column's validity words.

import "fmt"

// CompareOp is the comparison operator for typed predicate constructors.
type CompareOp int

const (
	CompareEq CompareOp = iota
	CompareNe
	CompareLt
	CompareLe
	CompareGt
	CompareGe
)

func (op CompareOp) String() string {
	switch op {
	case CompareEq:
		return "eq"
	case CompareNe:
		return "ne"
	case CompareLt:
		return "lt"
	case CompareLe:
		return "le"
	case CompareGt:
		return "gt"
	case CompareGe:
		return "ge"
	default:
		return "unknown"
	}
}

// SelectionBitmap is a packed row-selection mask over one RecordBatch. Bit i
// set means row i is selected. Bits at positions >= Len() are always zero
// (tail-word hygiene is preserved by every constructor and merge).
//
// It is the shared columnar bitmap (columnar_bitmap.go) with the selection
// vocabulary on top; validity masks are the same structure, which is what lets
// maskValidity be a single bulk AND.
type SelectionBitmap struct {
	bitmap
}

// NewSelectionBitmap returns an empty (nothing selected) bitmap for n rows.
func NewSelectionBitmap(n int) SelectionBitmap {
	return SelectionBitmap{bitmap: newBitmap(n)}
}

// Len returns the row count the bitmap covers.
func (s *SelectionBitmap) Len() int { return s.n }

// Count returns the number of selected rows. See popcountWords for the
// interleaved POPCNT/CNT kernel.
func (s *SelectionBitmap) Count() int { return s.count() }

// IsSelected reports whether row i is selected. Out-of-range rows are not
// selected.
func (s *SelectionBitmap) IsSelected(i int) bool { return s.get(i) }

// And intersects the receiver with other in place.
func (s *SelectionBitmap) And(other *SelectionBitmap) error { return s.and(&other.bitmap) }

// Or unions the receiver with other in place.
func (s *SelectionBitmap) Or(other *SelectionBitmap) error { return s.or(&other.bitmap) }

// AndNot removes other's selected rows from the receiver in place.
func (s *SelectionBitmap) AndNot(other *SelectionBitmap) error { return s.andNot(&other.bitmap) }

// Not complements the selection in place. Rows beyond Len() stay unselected.
//
// Over a nullable column this re-selects null rows: under two-valued logic
// "did not match" and "had no value" are the same bit, so the complement of a
// validity-masked selection includes the nulls. Re-intersect with validity when
// that matters — see docs/columnar_null_algebra.md ("The complement trap").
func (s *SelectionBitmap) Not() { s.not() }

// ForEachSelected visits selected rows in ascending order until fn returns
// false. Iteration is a word bit-scan: zero words are skipped in one compare,
// and each selected row costs one TrailingZeros64.
func (s *SelectionBitmap) ForEachSelected(fn func(row int) bool) { s.forEachSet(fn) }

// column resolves a named column or fails with the available names.
func (b *RecordBatch) column(name string) (Vector, error) {
	for _, col := range b.Columns {
		if col.Name == name {
			return col.Data, nil
		}
	}
	return nil, fmt.Errorf("hermes columnar batch has no column %q", name)
}

// validityWords exposes the packed validity words for a vector so predicate
// results can be masked in bulk rather than per row.
func validityWords(vec Vector) []uint64 {
	switch v := vec.(type) {
	case *Int64Vector:
		return v.validity.words
	case *Float64Vector:
		return v.validity.words
	case *StringVector:
		return v.validity.words
	case *TimestampVector:
		return v.validity.words
	case *DomainRecordVector:
		return v.validity.words
	default:
		return nil
	}
}

// maskValidity clears selection bits for null cells using one AND per word.
// Selection and validity are the same bitmap structure, so this is a single
// bulk intersection rather than a per-row test.
func (s *SelectionBitmap) maskValidity(vec Vector) {
	s.andClamped(validityWords(vec))
}

func compareMatches[T int64 | float64 | string](op CompareOp, value, operand T) bool {
	switch op {
	case CompareEq:
		return value == operand
	case CompareNe:
		return value != operand
	case CompareLt:
		return value < operand
	case CompareLe:
		return value <= operand
	case CompareGt:
		return value > operand
	case CompareGe:
		return value >= operand
	default:
		return false
	}
}

// SelectInt64 builds a selection bitmap from one comparison over an int64 (or
// timestamp) column. Null cells never match.
func (b *RecordBatch) SelectInt64(name string, op CompareOp, operand int64) (SelectionBitmap, error) {
	vec, err := b.column(name)
	if err != nil {
		return SelectionBitmap{}, err
	}
	values := vec.Int64Values()
	if values == nil {
		return SelectionBitmap{}, fmt.Errorf("hermes column %q is not int64-comparable (%d)", name, vec.Type())
	}
	sel := NewSelectionBitmap(b.Rows)
	selectInt64Kernel(sel.words, values, op, operand)
	sel.maskValidity(vec)
	return sel, nil
}

// SelectFloat64 builds a selection bitmap from one comparison over a float64
// column. Null cells never match.
func (b *RecordBatch) SelectFloat64(name string, op CompareOp, operand float64) (SelectionBitmap, error) {
	vec, err := b.column(name)
	if err != nil {
		return SelectionBitmap{}, err
	}
	values := vec.Float64Values()
	if values == nil {
		return SelectionBitmap{}, fmt.Errorf("hermes column %q is not float64-comparable (%d)", name, vec.Type())
	}
	sel := NewSelectionBitmap(b.Rows)
	selectFloat64Kernel(sel.words, values, op, operand)
	sel.maskValidity(vec)
	return sel, nil
}

func selectFloat64Scalar(words []uint64, values []float64, op CompareOp, operand float64) {
	n := len(values)
	for w := range words {
		base := w << 6
		end := min(base+64, n)
		var word uint64
		for i := base; i < end; i++ {
			if compareMatches(op, values[i], operand) {
				word |= 1 << uint(i-base)
			}
		}
		words[w] = word
	}
}

func selectInt64Scalar(words []uint64, values []int64, op CompareOp, operand int64) {
	n := len(values)
	for w := range words {
		base := w << 6
		end := min(base+64, n)
		var word uint64
		for i := base; i < end; i++ {
			if compareMatches(op, values[i], operand) {
				word |= 1 << uint(i-base)
			}
		}
		words[w] = word
	}
}

// SelectString builds a selection bitmap from one comparison over a string
// column. It scans the contiguous offsets/bytes layout through ValueAt, whose
// transient copies are elided by escape analysis. Null cells never match.
func (b *RecordBatch) SelectString(name string, op CompareOp, operand string) (SelectionBitmap, error) {
	vec, err := b.column(name)
	if err != nil {
		return SelectionBitmap{}, err
	}
	stringVec, ok := vec.(*StringVector)
	if !ok {
		return SelectionBitmap{}, fmt.Errorf("hermes column %q is not string-comparable (%d)", name, vec.Type())
	}
	sel := NewSelectionBitmap(b.Rows)
	for i := 0; i < stringVec.Len(); i++ {
		if compareMatches(op, stringVec.ValueAt(i), operand) {
			sel.words[i>>6] |= 1 << uint(i&63)
		}
	}
	sel.maskValidity(vec)
	return sel, nil
}

// SumFloat64Selected reduces a float64 column over the selected rows only,
// visiting values by bit-scan so unselected memory is never touched. Null
// handling is inherited from the constructors (null rows are never selected);
// rows selected by other columns but null here contribute their zero value,
// matching Float64Vector.Sum's documented posture.
func (b *RecordBatch) SumFloat64Selected(name string, sel *SelectionBitmap) (float64, error) {
	vec, err := b.column(name)
	if err != nil {
		return 0, err
	}
	values := vec.Float64Values()
	if values == nil {
		return 0, fmt.Errorf("hermes column %q is not float64-summable (%d)", name, vec.Type())
	}
	if sel.Len() != len(values) {
		return 0, fmt.Errorf("hermes selection covers %d rows but column %q has %d", sel.Len(), name, len(values))
	}
	var sum float64
	sel.ForEachSelected(func(row int) bool {
		sum += values[row]
		return true
	})
	return sum, nil
}
