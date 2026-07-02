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

import (
	"fmt"
	"math/bits"
)

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
type SelectionBitmap struct {
	words []uint64
	n     int
}

// NewSelectionBitmap returns an empty (nothing selected) bitmap for n rows.
func NewSelectionBitmap(n int) SelectionBitmap {
	if n < 0 {
		n = 0
	}
	return SelectionBitmap{words: make([]uint64, (n+63)/64), n: n}
}

// Len returns the row count the bitmap covers.
func (s *SelectionBitmap) Len() int { return s.n }

// Count returns the number of selected rows. bits.OnesCount64 lowers to a
// single POPCNT/CNT instruction per word.
func (s *SelectionBitmap) Count() int {
	total := 0
	for _, w := range s.words {
		total += bits.OnesCount64(w)
	}
	return total
}

// IsSelected reports whether row i is selected. Out-of-range rows are not
// selected.
func (s *SelectionBitmap) IsSelected(i int) bool {
	if i < 0 || i >= s.n {
		return false
	}
	return (s.words[i>>6]>>uint(i&63))&1 == 1
}

// tailMask zeroes any bits above n in the final word so complement-style
// operations cannot select phantom rows.
func (s *SelectionBitmap) tailMask() {
	if s.n == 0 || len(s.words) == 0 {
		return
	}
	rem := uint(s.n & 63)
	if rem != 0 {
		s.words[len(s.words)-1] &= (1 << rem) - 1
	}
}

func (s *SelectionBitmap) sameShape(other *SelectionBitmap) error {
	if s.n != other.n {
		return fmt.Errorf("hermes selection bitmaps cover different row counts: %d vs %d", s.n, other.n)
	}
	return nil
}

// And intersects the receiver with other in place.
func (s *SelectionBitmap) And(other *SelectionBitmap) error {
	if err := s.sameShape(other); err != nil {
		return err
	}
	for i := range s.words {
		s.words[i] &= other.words[i]
	}
	return nil
}

// Or unions the receiver with other in place.
func (s *SelectionBitmap) Or(other *SelectionBitmap) error {
	if err := s.sameShape(other); err != nil {
		return err
	}
	for i := range s.words {
		s.words[i] |= other.words[i]
	}
	return nil
}

// AndNot removes other's selected rows from the receiver in place.
func (s *SelectionBitmap) AndNot(other *SelectionBitmap) error {
	if err := s.sameShape(other); err != nil {
		return err
	}
	for i := range s.words {
		s.words[i] &^= other.words[i]
	}
	return nil
}

// Not complements the selection in place. Rows beyond Len() stay unselected.
func (s *SelectionBitmap) Not() {
	for i := range s.words {
		s.words[i] = ^s.words[i]
	}
	s.tailMask()
}

// ForEachSelected visits selected rows in ascending order until fn returns
// false. Iteration is a word bit-scan: zero words are skipped in one compare,
// and each selected row costs one TrailingZeros64.
func (s *SelectionBitmap) ForEachSelected(fn func(row int) bool) {
	for wi, w := range s.words {
		base := wi << 6
		for w != 0 {
			bit := bits.TrailingZeros64(w)
			if !fn(base + bit) {
				return
			}
			w &= w - 1
		}
	}
}

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
func (s *SelectionBitmap) maskValidity(vec Vector) {
	words := validityWords(vec)
	if words == nil {
		return
	}
	for i := range s.words {
		if i < len(words) {
			s.words[i] &= words[i]
		} else {
			s.words[i] = 0
		}
	}
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
	for i, value := range values {
		if compareMatches(op, value, operand) {
			sel.words[i>>6] |= 1 << uint(i&63)
		}
	}
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
	for i, value := range values {
		if compareMatches(op, value, operand) {
			sel.words[i>>6] |= 1 << uint(i&63)
		}
	}
	sel.maskValidity(vec)
	return sel, nil
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
