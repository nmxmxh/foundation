package hermes

// Predicate pushdown into columnar batch construction.
//
// SelectionBitmap (columnar_select.go) filters a batch after vectors exist.
// This file moves the same predicates upstream: entries are filtered right
// after candidate collection, before sorting, before the limit, and before any
// vector is built — so unselected rows never pay sort comparisons or column
// materialization. Predicates combine with AND semantics; OR compositions
// should run SelectionBitmap merges over the (now smaller) resulting batch.
//
// Null semantics match SelectionBitmap: a missing field, a non-scalar cell, or
// a kind mismatch never matches any predicate.

import (
	"context"
	"strconv"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type predicateKind int

const (
	predicateInt64 predicateKind = iota
	predicateFloat64
	predicateString
)

// ColumnPredicate is one typed comparison pushed into batch construction.
// Build values with PredicateInt64, PredicateFloat64, or PredicateString.
type ColumnPredicate struct {
	Field string
	Op    CompareOp

	kind          predicateKind
	int64Operand  int64
	floatOperand  float64
	stringOperand string
}

// PredicateInt64 compares an integer data field (or the reserved "version"
// attribute) against operand.
func PredicateInt64(field string, op CompareOp, operand int64) ColumnPredicate {
	return ColumnPredicate{Field: field, Op: op, kind: predicateInt64, int64Operand: operand}
}

// PredicateFloat64 compares a float data field against operand.
func PredicateFloat64(field string, op CompareOp, operand float64) ColumnPredicate {
	return ColumnPredicate{Field: field, Op: op, kind: predicateFloat64, floatOperand: operand}
}

// PredicateString compares a string data field (or the reserved "record_id" /
// "organization_id" attributes) against operand.
func PredicateString(field string, op CompareOp, operand string) ColumnPredicate {
	return ColumnPredicate{Field: field, Op: op, kind: predicateString, stringOperand: operand}
}

// matches evaluates the predicate against one candidate entry.
func (p ColumnPredicate) matches(entry recordEntry) bool {
	switch p.Field {
	case "record_id":
		return p.kind == predicateString && compareMatches(p.Op, entry.record.RecordID, p.stringOperand)
	case "organization_id":
		return p.kind == predicateString && compareMatches(p.Op, entry.record.OrganizationID, p.stringOperand)
	case "version":
		// #nosec G115 -- version is a monotonically assigned record counter.
		return p.kind == predicateInt64 && compareMatches(p.Op, int64(entry.version), p.int64Operand)
	default:
		return p.matchesDataField(entry.record.Data)
	}
}

// matchesDataField resolves a non-reserved field with the same kind rules as
// buildDataFieldVector, so pushdown selection agrees with the vectors a batch
// would have built.
func (p ColumnPredicate) matchesDataField(data database.RecordData) bool {
	val, ok := data.Get(p.Field)
	if !ok {
		return false
	}
	kind, idxVal, scalar := val.ScalarIndex()
	switch p.kind {
	case predicateInt64:
		if !scalar || (kind != 'i' && kind != 'u') {
			return false
		}
		parsed, err := strconv.ParseInt(idxVal, 10, 64)
		return err == nil && compareMatches(p.Op, parsed, p.int64Operand)
	case predicateFloat64:
		if !scalar || kind != 'f' {
			return false
		}
		parsed, err := strconv.ParseFloat(idxVal, 64)
		return err == nil && compareMatches(p.Op, parsed, p.floatOperand)
	case predicateString:
		if scalar {
			return compareMatches(p.Op, idxVal, p.stringOperand)
		}
		if val.Kind == database.RecordValueNull {
			return false
		}
		return compareMatches(p.Op, val.Text, p.stringOperand)
	default:
		return false
	}
}

// filterEntriesInPlace keeps only entries matching every predicate, reusing
// the candidate slice's storage.
func filterEntriesInPlace(entries []recordEntry, predicates []ColumnPredicate) []recordEntry {
	if len(predicates) == 0 {
		return entries
	}
	kept := entries[:0]
	for _, entry := range entries {
		matched := true
		for _, predicate := range predicates {
			if !predicate.matches(entry) {
				matched = false
				break
			}
		}
		if matched {
			kept = append(kept, entry)
		}
	}
	return kept
}

// GetColumnarBatchWhere builds a columnar batch with predicates applied during
// construction: rows failing any predicate are dropped before sorting, before
// the query limit, and before any vector is materialized. The query limit
// therefore applies to *filtered* rows (SQL WHERE-then-LIMIT semantics), which
// also makes this the correct API for "top N matching" reads — a post-hoc
// SelectionBitmap over a limited batch cannot promise N surviving rows.
func (s *Store) GetColumnarBatchWhere(
	ctx context.Context,
	projection string,
	query Query,
	fields []string,
	predicates []ColumnPredicate,
	fence Fence,
) (*RecordBatch, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return nil, err
	}
	return part.getColumnarBatchWhere(ctx, query, fields, predicates, fence)
}
