package hermes

import (
	"context"
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

type Vector interface {
	Type() DataType
	Len() int
	NullCount() int
	IsValid(i int) bool

	Int64Values() []int64
	Float64Values() []float64
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

type Int64Vector struct {
	values []int64
	valid  []bool
}

func (v *Int64Vector) Type() DataType { return TypeInt64 }
func (v *Int64Vector) Len() int { return len(v.values) }
func (v *Int64Vector) NullCount() int {
	c := 0
	for _, b := range v.valid {
		if !b {
			c++
		}
	}
	return c
}
func (v *Int64Vector) IsValid(i int) bool { return v.valid[i] }
func (v *Int64Vector) Int64Values() []int64 { return v.values }
func (v *Int64Vector) Float64Values() []float64 { return nil }
func (v *Int64Vector) StringValues() []string { return nil }
func (v *Int64Vector) BytesValues() [][]byte { return nil }

type Float64Vector struct {
	values []float64
	valid  []bool
}

func (v *Float64Vector) Type() DataType { return TypeFloat64 }
func (v *Float64Vector) Len() int { return len(v.values) }
func (v *Float64Vector) NullCount() int {
	c := 0
	for _, b := range v.valid {
		if !b {
			c++
		}
	}
	return c
}
func (v *Float64Vector) IsValid(i int) bool { return v.valid[i] }
func (v *Float64Vector) Int64Values() []int64 { return nil }
func (v *Float64Vector) Float64Values() []float64 { return v.values }
func (v *Float64Vector) StringValues() []string { return nil }
func (v *Float64Vector) BytesValues() [][]byte { return nil }

type StringVector struct {
	values []string
	valid  []bool
}

func (v *StringVector) Type() DataType { return TypeString }
func (v *StringVector) Len() int { return len(v.values) }
func (v *StringVector) NullCount() int {
	c := 0
	for _, b := range v.valid {
		if !b {
			c++
		}
	}
	return c
}
func (v *StringVector) IsValid(i int) bool { return v.valid[i] }
func (v *StringVector) Int64Values() []int64 { return nil }
func (v *StringVector) Float64Values() []float64 { return nil }
func (v *StringVector) StringValues() []string { return v.values }
func (v *StringVector) BytesValues() [][]byte { return nil }

type TimestampVector struct {
	values []time.Time
	valid  []bool
}

func (v *TimestampVector) Type() DataType { return TypeTimestamp }
func (v *TimestampVector) Len() int { return len(v.values) }
func (v *TimestampVector) NullCount() int {
	c := 0
	for _, b := range v.valid {
		if !b {
			c++
		}
	}
	return c
}
func (v *TimestampVector) IsValid(i int) bool { return v.valid[i] }
func (v *TimestampVector) Int64Values() []int64 {
	out := make([]int64, len(v.values))
	for i, t := range v.values {
		out[i] = t.UnixNano()
	}
	return out
}
func (v *TimestampVector) Float64Values() []float64 { return nil }
func (v *TimestampVector) StringValues() []string { return nil }
func (v *TimestampVector) BytesValues() [][]byte { return nil }

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

	if query.Limit <= 0 || len(entries) > 1 {
		sort.Slice(entries, func(i int, j int) bool {
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
		var vec Vector
		switch field {
		case "_record":
			vals := make([]database.DomainRecord, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				vals[i] = entry.record
				valid[i] = true
			}
			vec = &DomainRecordVector{values: vals, valid: valid}
		case "record_id":
			vals := make([]string, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				vals[i] = entry.record.RecordID
				valid[i] = true
			}
			vec = &StringVector{values: vals, valid: valid}
		case "organization_id":
			vals := make([]string, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				vals[i] = entry.record.OrganizationID
				valid[i] = true
			}
			vec = &StringVector{values: vals, valid: valid}
		case "created_at":
			vals := make([]time.Time, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				vals[i] = entry.record.CreatedAt
				valid[i] = true
			}
			vec = &TimestampVector{values: vals, valid: valid}
		case "updated_at":
			vals := make([]time.Time, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				vals[i] = entry.record.UpdatedAt
				valid[i] = true
			}
			vec = &TimestampVector{values: vals, valid: valid}
		case "version":
			vals := make([]int64, rows)
			valid := make([]bool, rows)
			for i, entry := range entries {
				// #nosec G115
				vals[i] = int64(entry.version)
				valid[i] = true
			}
			vec = &Int64Vector{values: vals, valid: valid}
		default:
			// Try to extract from RecordData
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
				vals := make([]string, rows)
				valid := make([]bool, rows)
				vec = &StringVector{values: vals, valid: valid}
			} else {
				switch kind {
				case 'i', 'u':
					vals := make([]int64, rows)
					valid := make([]bool, rows)
					for i, entry := range entries {
						if val, ok := entry.record.Data.Get(field); ok {
							if kind, idxVal, ok := val.ScalarIndex(); ok && (kind == 'i' || kind == 'u') {
								if iv, err := strconv.ParseInt(idxVal, 10, 64); err == nil {
									vals[i] = iv
									valid[i] = true
								}
							}
						}
					}
					vec = &Int64Vector{values: vals, valid: valid}
				case 'f':
					vals := make([]float64, rows)
					valid := make([]bool, rows)
					for i, entry := range entries {
						if val, ok := entry.record.Data.Get(field); ok {
							if kind, idxVal, ok := val.ScalarIndex(); ok && kind == 'f' {
								if fv, err := strconv.ParseFloat(idxVal, 64); err == nil {
									vals[i] = fv
									valid[i] = true
								}
							}
						}
					}
					vec = &Float64Vector{values: vals, valid: valid}
				case 'b':
					vals := make([]int64, rows)
					valid := make([]bool, rows)
					for i, entry := range entries {
						if val, ok := entry.record.Data.Get(field); ok {
							if kind, idxVal, ok := val.ScalarIndex(); ok && kind == 'b' {
								if idxVal == "1" {
									vals[i] = 1
								} else {
									vals[i] = 0
								}
								valid[i] = true
							}
						}
					}
					vec = &Int64Vector{values: vals, valid: valid}
				default:
					vals := make([]string, rows)
					valid := make([]bool, rows)
					for i, entry := range entries {
						if val, ok := entry.record.Data.Get(field); ok {
							if _, idxVal, ok := val.ScalarIndex(); ok {
								vals[i] = idxVal
								valid[i] = true
							} else {
								vals[i] = val.Text
								valid[i] = val.Kind != database.RecordValueNull
							}
						}
					}
					vec = &StringVector{values: vals, valid: valid}
				}
			}
		}

		columns = append(columns, Column{Name: field, Data: vec})
	}

	return &RecordBatch{
		Columns: columns,
		Rows:    rows,
	}, nil
}

type DomainRecordVector struct {
	values []database.DomainRecord
	valid  []bool
}

func (v *DomainRecordVector) Type() DataType { return TypeBinary }
func (v *DomainRecordVector) Len() int { return len(v.values) }
func (v *DomainRecordVector) NullCount() int { return 0 }
func (v *DomainRecordVector) IsValid(i int) bool { return v.valid[i] }
func (v *DomainRecordVector) Int64Values() []int64 { return nil }
func (v *DomainRecordVector) Float64Values() []float64 { return nil }
func (v *DomainRecordVector) StringValues() []string { return nil }
func (v *DomainRecordVector) BytesValues() [][]byte { return nil }
func (v *DomainRecordVector) RecordValues() []database.DomainRecord { return v.values }

