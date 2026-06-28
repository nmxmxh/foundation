package hermes

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type DataType int

const (
	TypeInt64 DataType = iota
	TypeFloat64
	TypeString
	TypeBinary
	TypeTimestamp
)

// validityBitmap is a bit-packed Arrow-style validity bitmap.
// Bit i is 1 (valid) when (words[i>>6]>>(i&63))&1 == 1.
// This is 64× more memory-efficient than []bool and enables
// future SIMD-accelerated null counting via bits.OnesCount64.
type validityBitmap struct {
	words []uint64
	n     int // total row count
}

func newValidityBitmap(n int) validityBitmap {
	words := (n + 63) / 64
	return validityBitmap{words: make([]uint64, words), n: n}
}

func (b *validityBitmap) set(i int) {
	b.words[i>>6] |= 1 << uint(i&63)
}

func (b *validityBitmap) isValid(i int) bool {
	return (b.words[i>>6]>>uint(i&63))&1 == 1
}

// nullCount returns the number of null (invalid) entries.
// bits.OnesCount64 is recognized by the Go compiler and maps to
// POPCNT on x86 and CNT on ARM — no aspirational claims; this is
// a guaranteed single-instruction operation on both platforms.
func (b *validityBitmap) nullCount() int {
	ones := 0
	for _, w := range b.words {
		ones += bits.OnesCount64(w)
	}
	return b.n - ones
}

// Vector is the columnar data interface. StringVector intentionally
// does NOT expose StringValues() []string at this level because
// materializing a []string heap-allocates. Callers that need zero-copy
// string access must assert to *StringVector and use Offsets()/Bytes().
type Vector interface {
	Type() DataType
	Len() int
	NullCount() int
	IsValid(i int) bool

	Int64Values() []int64
	Float64Values() []float64
	// StringValues returns a materialized []string. This allocates.
	// For zero-copy access use (*StringVector).Offsets() and (*StringVector).Bytes().
	StringValues() []string
	BytesValues() [][]byte
}

type Column struct {
	Name string
	Data Vector
}

type RecordBatch struct {
	Columns []Column
	Rows    int
}

// ---------------------------------------------------------------------------
// Int64Vector
// ---------------------------------------------------------------------------

type Int64Vector struct {
	values   []int64
	validity validityBitmap
}

func newInt64Vector(n int) *Int64Vector {
	return &Int64Vector{values: make([]int64, n), validity: newValidityBitmap(n)}
}

func (v *Int64Vector) Type() DataType           { return TypeInt64 }
func (v *Int64Vector) Len() int                 { return len(v.values) }
func (v *Int64Vector) NullCount() int           { return v.validity.nullCount() }
func (v *Int64Vector) IsValid(i int) bool       { return v.validity.isValid(i) }
func (v *Int64Vector) Int64Values() []int64     { return v.values }
func (v *Int64Vector) Float64Values() []float64 { return nil }
func (v *Int64Vector) StringValues() []string   { return nil }
func (v *Int64Vector) BytesValues() [][]byte    { return nil }

// ---------------------------------------------------------------------------
// Float64Vector
// ---------------------------------------------------------------------------

type Float64Vector struct {
	values   []float64
	validity validityBitmap
}

func newFloat64Vector(n int) *Float64Vector {
	return &Float64Vector{values: make([]float64, n), validity: newValidityBitmap(n)}
}

func (v *Float64Vector) Type() DataType           { return TypeFloat64 }
func (v *Float64Vector) Len() int                 { return len(v.values) }
func (v *Float64Vector) NullCount() int           { return v.validity.nullCount() }
func (v *Float64Vector) IsValid(i int) bool       { return v.validity.isValid(i) }
func (v *Float64Vector) Int64Values() []int64     { return nil }
func (v *Float64Vector) Float64Values() []float64 { return v.values }
func (v *Float64Vector) StringValues() []string   { return nil }
func (v *Float64Vector) BytesValues() [][]byte    { return nil }

// ---------------------------------------------------------------------------
// StringVector — Arrow-style offset+bytes layout
//
// Memory layout (matches Apache Arrow binary/string specification):
//
//   offsets: [o0, o1, o2, ..., oN]   (N+1 int32 values)
//   buf:     [s0_bytes | s1_bytes | ...]
//
// String i occupies buf[offsets[i]:offsets[i+1]].
// Accessing a single string is a bounds check + slice header construction —
// zero heap allocation. StringValues() materializes []string and DOES allocate;
// callers on hot paths should use Offsets()+Bytes() directly.
// ---------------------------------------------------------------------------

type StringVector struct {
	offsets  []int32 // len == n+1
	buf      []byte
	validity validityBitmap
	n        int
}

func newStringVectorFromSlice(ss []string, valid []bool) (*StringVector, error) {
	n := len(ss)
	// Compute total byte length for single allocation.
	total := 0
	for _, s := range ss {
		total += len(s)
	}
	// Arrow's standard binary layout uses int32 offsets, capping one string
	// column at 2 GiB. Bound the input explicitly (CP-02) rather than let the
	// int32(len(v.buf)) conversions below overflow silently (CWE-190).
	if total > math.MaxInt32 {
		return nil, fmt.Errorf("hermes: string column bytes %d exceed int32 offset limit", total)
	}
	v := &StringVector{
		offsets:  make([]int32, n+1),
		validity: newValidityBitmap(n),
		n:        n,
	}
	v.buf = make([]byte, 0, total)
	for i, s := range ss {
		// #nosec G115 -- len(v.buf) <= total <= MaxInt32, guarded above.
		v.offsets[i] = int32(len(v.buf))
		v.buf = append(v.buf, s...)
		if valid[i] {
			v.validity.set(i)
		}
	}
	// #nosec G115 -- len(v.buf) == total <= MaxInt32, guarded above.
	v.offsets[n] = int32(len(v.buf))
	return v, nil
}

func (v *StringVector) Type() DataType           { return TypeString }
func (v *StringVector) Len() int                 { return v.n }
func (v *StringVector) NullCount() int           { return v.validity.nullCount() }
func (v *StringVector) IsValid(i int) bool       { return v.validity.isValid(i) }
func (v *StringVector) Int64Values() []int64     { return nil }
func (v *StringVector) Float64Values() []float64 { return nil }
func (v *StringVector) BytesValues() [][]byte    { return nil }

// Offsets returns the raw offset array (length n+1). Use with Bytes() for
// zero-copy string views: buf[offsets[i]:offsets[i+1]] is string i.
func (v *StringVector) Offsets() []int32 { return v.offsets }

// Bytes returns the contiguous backing byte buffer.
func (v *StringVector) Bytes() []byte { return v.buf }

// ValueAt returns the string at index i. The result is a safe, independently
// owned copy of the backing bytes, so callers may retain it freely. On hot
// scan paths where the value is consumed transiently (e.g. length or hashing),
// Go escape analysis elides the copy. For genuine zero-copy access into the
// shared buffer, use Offsets()+Bytes() directly and observe their lifetime
// caveats — Hermes carries tenant projection data, so the unsafe view is kept
// out of this convenience accessor (see CP "no unsafe in handwritten Go").
func (v *StringVector) ValueAt(i int) string {
	return string(v.buf[v.offsets[i]:v.offsets[i+1]])
}

// StringValues materializes a []string heap copy. Use for compatibility
// only — prefer ValueAt or Offsets/Bytes on hot paths.
func (v *StringVector) StringValues() []string {
	out := make([]string, v.n)
	for i := range out {
		out[i] = v.ValueAt(i)
	}
	return out
}

// ---------------------------------------------------------------------------
// TimestampVector
// ---------------------------------------------------------------------------

type TimestampVector struct {
	values   []time.Time
	validity validityBitmap
}

func newTimestampVector(n int) *TimestampVector {
	return &TimestampVector{values: make([]time.Time, n), validity: newValidityBitmap(n)}
}

func (v *TimestampVector) Type() DataType     { return TypeTimestamp }
func (v *TimestampVector) Len() int           { return len(v.values) }
func (v *TimestampVector) NullCount() int     { return v.validity.nullCount() }
func (v *TimestampVector) IsValid(i int) bool { return v.validity.isValid(i) }
func (v *TimestampVector) Int64Values() []int64 {
	out := make([]int64, len(v.values))
	for i, t := range v.values {
		out[i] = t.UnixNano()
	}
	return out
}
func (v *TimestampVector) Float64Values() []float64 { return nil }
func (v *TimestampVector) StringValues() []string   { return nil }
func (v *TimestampVector) BytesValues() [][]byte    { return nil }

// ---------------------------------------------------------------------------
// DomainRecordVector — wraps full DomainRecord values for the _record column.
// ---------------------------------------------------------------------------

type DomainRecordVector struct {
	values   []database.DomainRecord
	validity validityBitmap
}

func (v *DomainRecordVector) Type() DataType                        { return TypeBinary }
func (v *DomainRecordVector) Len() int                              { return len(v.values) }
func (v *DomainRecordVector) NullCount() int                        { return v.validity.nullCount() }
func (v *DomainRecordVector) IsValid(i int) bool                    { return v.validity.isValid(i) }
func (v *DomainRecordVector) Int64Values() []int64                  { return nil }
func (v *DomainRecordVector) Float64Values() []float64              { return nil }
func (v *DomainRecordVector) StringValues() []string                { return nil }
func (v *DomainRecordVector) BytesValues() [][]byte                 { return nil }
func (v *DomainRecordVector) RecordValues() []database.DomainRecord { return v.values }

// ---------------------------------------------------------------------------
// GetColumnarBatch — public entry point
// ---------------------------------------------------------------------------

func (s *Store) GetColumnarBatch(ctx context.Context, projection string, query Query, fields []string, fence Fence) (*RecordBatch, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return nil, err
	}
	return part.getColumnarBatch(ctx, query, fields, fence)
}

// ---------------------------------------------------------------------------
// collectRecordEntries — shared candidate collection used by getColumnarBatch.
// ---------------------------------------------------------------------------

func (p *partition) collectRecordEntries(ctx context.Context, registry *partitionRegistry, query Query) ([]recordEntry, error) {
	ordered := p.orderedCandidateIndex(registry, query)
	index := p.candidateIndex(registry, query)
	capacity := int(p.records.Load())
	if query.Limit > 0 && ordered.len() > 0 {
		capacity = query.Limit
	} else if index != nil {
		capacity = index.len()
	}
	if query.Limit > 0 && capacity > query.Limit {
		capacity = query.Limit
	}
	candidates := make([]recordEntry, 0, capacity)

	if query.Limit > 0 && ordered.len() > 0 {
		var err error
		ordered.forEachOrderDesc(func(order recordOrderEntry) bool {
			if len(candidates) >= query.Limit {
				return false
			}
			entry, ok := p.recordForOrderEntry(registry, order)
			if !ok {
				return true
			}
			if err = ctxErr(ctx); err != nil {
				return false
			}
			if recordMatches(entry.record, p.spec, query) {
				candidates = append(candidates, entry)
			}
			return true
		})
		return candidates, err
	}

	if index != nil {
		var err error
		index.forEachKey(func(key string) bool {
			entry, ok := p.recordEntry(registry, key)
			if !ok {
				return true
			}
			if err = ctxErr(ctx); err != nil {
				return false
			}
			if recordMatches(entry.record, p.spec, query) {
				candidates = append(candidates, entry)
			}
			return true
		})
		return candidates, err
	}

	var err error
	registry.records.Range(func(_ any, value any) bool {
		entry, ok := recordEntryFromCell(value)
		if !ok || !recordMatches(entry.record, p.spec, query) {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		candidates = append(candidates, entry)
		return true
	})
	return candidates, err
}

// ---------------------------------------------------------------------------
// getColumnarBatch — builds a RecordBatch from the active partition registry.
// ---------------------------------------------------------------------------

func (p *partition) getColumnarBatch(ctx context.Context, query Query, fields []string, fence Fence) (*RecordBatch, error) {
	if err := p.waitForStable(ctx); err != nil {
		return nil, err
	}
	if err := p.checkFence(fence); err != nil {
		return nil, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return nil, ErrInvalidEvent
	}

	entries, err := p.collectRecordEntries(ctx, p.activeRegistry(), query)
	if err != nil {
		return nil, err
	}

	// Sort: descending UpdatedAt → descending version → ascending RecordID.
	if query.Limit <= 0 || len(entries) > 1 {
		sort.Slice(entries, func(i, j int) bool {
			if !entries[i].record.UpdatedAt.Equal(entries[j].record.UpdatedAt) {
				return entries[i].record.UpdatedAt.After(entries[j].record.UpdatedAt)
			}
			if entries[i].version != entries[j].version {
				return entries[i].version > entries[j].version
			}
			return entries[i].record.RecordID < entries[j].record.RecordID
		})
	}
	if query.Limit > 0 && len(entries) > query.Limit {
		entries = entries[:query.Limit]
	}

	rows := len(entries)
	columns := make([]Column, 0, len(fields))

	for _, field := range fields {
		vec, err := buildFieldVector(field, entries, rows)
		if err != nil {
			return nil, err
		}
		columns = append(columns, Column{Name: field, Data: vec})
	}

	return &RecordBatch{
		Columns: columns,
		Rows:    rows,
	}, nil
}

// buildFieldVector builds the Vector for a single column. Reserved field names
// map to fixed record attributes; any other field is resolved from the record's
// data map, with the column type inferred from the first valid scalar value.
// Zero-copy/borrowed-view semantics and allocation shape are preserved exactly.
func buildFieldVector(field string, entries []recordEntry, rows int) (Vector, error) {
	switch field {
	case "_record":
		vals := make([]database.DomainRecord, rows)
		dv := &DomainRecordVector{
			values:   vals,
			validity: newValidityBitmap(rows),
		}
		for i, entry := range entries {
			dv.values[i] = entry.record
			dv.validity.set(i)
		}
		return dv, nil

	case "record_id":
		ss := make([]string, rows)
		vv := make([]bool, rows)
		for i, entry := range entries {
			ss[i] = entry.record.RecordID
			vv[i] = true
		}
		return newStringVectorFromSlice(ss, vv)

	case "organization_id":
		ss := make([]string, rows)
		vv := make([]bool, rows)
		for i, entry := range entries {
			ss[i] = entry.record.OrganizationID
			vv[i] = true
		}
		return newStringVectorFromSlice(ss, vv)

	case "created_at":
		tv := newTimestampVector(rows)
		for i, entry := range entries {
			tv.values[i] = entry.record.CreatedAt
			tv.validity.set(i)
		}
		return tv, nil

	case "updated_at":
		tv := newTimestampVector(rows)
		for i, entry := range entries {
			tv.values[i] = entry.record.UpdatedAt
			tv.validity.set(i)
		}
		return tv, nil

	case "version":
		iv := newInt64Vector(rows)
		for i, entry := range entries {
			// #nosec G115
			iv.values[i] = int64(entry.version)
			iv.validity.set(i)
		}
		return iv, nil

	default:
		return buildDataFieldVector(field, entries, rows)
	}
}

// buildDataFieldVector resolves a non-reserved field from the record data map.
// The column type is determined from the first valid scalar entry; an empty
// string column is produced when no entry carries the field.
func buildDataFieldVector(field string, entries []recordEntry, rows int) (Vector, error) {
	// Determine column type from the first valid entry.
	var kind byte
	found := false
	for _, entry := range entries {
		if val, ok := entry.record.Data.Get(field); ok {
			k, _, ok := val.ScalarIndex()
			if ok {
				kind = k
				found = true
				break
			}
		}
	}

	if !found {
		ss := make([]string, rows)
		vv := make([]bool, rows)
		return newStringVectorFromSlice(ss, vv)
	}

	switch kind {
	case 'i', 'u':
		iv := newInt64Vector(rows)
		for i, entry := range entries {
			if val, ok := entry.record.Data.Get(field); ok {
				if k, idxVal, ok := val.ScalarIndex(); ok && (k == 'i' || k == 'u') {
					parsed, err2 := strconv.ParseInt(idxVal, 10, 64)
					if err2 != nil {
						return nil, fmt.Errorf("hermes: failed to parse integer field %q value %q: %w", field, idxVal, err2)
					}
					iv.values[i] = parsed
					iv.validity.set(i)
				}
			}
		}
		return iv, nil
	case 'f':
		fv := newFloat64Vector(rows)
		for i, entry := range entries {
			if val, ok := entry.record.Data.Get(field); ok {
				if k, idxVal, ok := val.ScalarIndex(); ok && k == 'f' {
					parsed, err2 := strconv.ParseFloat(idxVal, 64)
					if err2 != nil {
						return nil, fmt.Errorf("hermes: failed to parse float field %q value %q: %w", field, idxVal, err2)
					}
					fv.values[i] = parsed
					fv.validity.set(i)
				}
			}
		}
		return fv, nil
	case 'b':
		iv := newInt64Vector(rows)
		for i, entry := range entries {
			if val, ok := entry.record.Data.Get(field); ok {
				if k, idxVal, ok := val.ScalarIndex(); ok && k == 'b' {
					if idxVal == "1" {
						iv.values[i] = 1
					}
					iv.validity.set(i)
				}
			}
		}
		return iv, nil
	default:
		ss := make([]string, rows)
		vv := make([]bool, rows)
		for i, entry := range entries {
			if val, ok := entry.record.Data.Get(field); ok {
				if _, idxVal, ok := val.ScalarIndex(); ok {
					ss[i] = idxVal
					vv[i] = true
				} else {
					ss[i] = val.Text
					vv[i] = val.Kind != database.RecordValueNull
				}
			}
		}
		return newStringVectorFromSlice(ss, vv)
	}
}
