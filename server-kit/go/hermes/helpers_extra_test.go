package hermes

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// TestNewQueryFilterValidation covers the constructor's accept/reject contract
// (TE-04 boundaries): supported scalar values build a filter; an empty field name
// or an unsupported value type is rejected.
func TestNewQueryFilterValidation(t *testing.T) {
	if _, ok := NewQueryFilter("symbol", "OVS"); !ok {
		t.Fatal("string filter should be accepted")
	}
	if _, ok := NewQueryFilter("  ", "OVS"); ok {
		t.Fatal("blank field must be rejected")
	}
	if _, ok := NewQueryFilter("x", []string{"not", "scalar"}); ok {
		t.Fatal("non-scalar value must be rejected")
	}
}

// TestQueryFilterValueRoundTrip is the filter-codec invariant (TE-31): a value
// encoded into a QueryFilter by NewQueryFilter decodes back, via queryFilterValue,
// to a RecordValue equal to the canonical RecordValueFromAny of the input. The
// round-trip must hold across every supported scalar kind.
func TestQueryFilterValueRoundTrip(t *testing.T) {
	cases := map[string]any{
		"string": "OVS",
		"int":    int64(-42),
		"uint":   uint64(42),
		"bool":   true,
		"float":  3.5,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			filter, ok := NewQueryFilter("field", in)
			if !ok {
				t.Fatalf("NewQueryFilter(%v) not ok", in)
			}
			got := queryFilterValue(filter)
			want, ok := database.RecordValueFromAny(in)
			if !ok {
				t.Fatalf("RecordValueFromAny(%v) not ok", in)
			}
			gk, gv, _ := got.ScalarIndex()
			wk, wv, _ := want.ScalarIndex()
			if gk != wk || gv != wv {
				t.Fatalf("round trip drift: got (%c,%q) want (%c,%q)", gk, gv, wk, wv)
			}
		})
	}

	// Malformed encoded numerics fall back to a string value rather than erroring.
	for _, kind := range []byte{'i', 'u', 'f'} {
		v := queryFilterValue(QueryFilter{Field: "f", Kind: kind, Value: "not-a-number"})
		if v.Kind != database.RecordValueString {
			t.Fatalf("malformed %c value = kind %v, want string fallback", kind, v.Kind)
		}
	}
}

// TestQueryWithFiltersPlanShape covers the query planner's partitioning by filter
// count (TE-04 zero/one/many) and its normalization: blank-field filters are
// dropped and multi-filter plans are sorted by field for a stable plan shape.
func TestQueryWithFiltersPlanShape(t *testing.T) {
	sym, _ := NewQueryFilter("symbol", "OVS")
	region, _ := NewQueryFilter("region", "us")
	blank := QueryFilter{Field: "  ", Kind: 's', Value: "x"}

	if q := QueryWithFilters("org_1", 10); q.Plan.count != 0 {
		t.Fatalf("zero filters -> count %d, want 0", q.Plan.count)
	}
	if q := QueryWithFilters("org_1", 10, sym); q.Plan.count != 1 || q.Plan.first.Field != "symbol" {
		t.Fatalf("one filter plan = %+v", q.Plan)
	}

	q := QueryWithFilters("org_1", 10, sym, region, blank)
	if q.Plan.count != 2 {
		t.Fatalf("two valid filters (one blank dropped) -> count %d, want 2", q.Plan.count)
	}
	if q.Plan.filters[0].Field != "region" || q.Plan.filters[1].Field != "symbol" {
		t.Fatalf("filters not sorted by field: %+v", q.Plan.filters)
	}

	// RecordFilters is the inverse projection; it must reproduce both fields.
	rf := q.Plan.RecordFilters()
	if len(rf) != 2 {
		t.Fatalf("RecordFilters len = %d, want 2", len(rf))
	}
}

// TestQueryFromRecordQuery covers conversion from the database query shape,
// including the skip rules: blank-field and non-indexable filters are dropped.
func TestQueryFromRecordQuery(t *testing.T) {
	// Single valid filter -> count 1.
	single := database.RecordQuery{Limit: 5, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
	}}
	if q := QueryFromRecordQuery("org_1", single); q.Plan.count != 1 || q.Limit != 5 {
		t.Fatalf("single = %+v", q)
	}

	// Single blank-field filter -> dropped, empty plan.
	blank := database.RecordQuery{Limit: 1, Filters: []database.RecordFilter{
		{Field: "  ", Value: database.StringValue("x")},
	}}
	if q := QueryFromRecordQuery("org_1", blank); q.Plan.count != 0 {
		t.Fatalf("blank single -> count %d, want 0", q.Plan.count)
	}

	// Many filters with one blank dropped -> count 2.
	many := database.RecordQuery{Limit: 9, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
		{Field: "", Value: database.StringValue("skip")},
		{Field: "region", Value: database.StringValue("us")},
	}}
	if q := QueryFromRecordQuery("org_1", many); q.Plan.count != 2 {
		t.Fatalf("many -> count %d, want 2", q.Plan.count)
	}
}

// TestRecordMatchesPlannedFilters covers the in-memory match predicate (TE-04):
// a record is matched only when domain/collection/tenant and every planned field
// filter agree; a differing field value excludes it.
func TestRecordMatchesPlannedFilters(t *testing.T) {
	spec := driftSpec()
	rec := testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "OVS"})

	// No planned filters: tenant-scoped match.
	if !recordMatches(rec, spec, Query{OrganizationID: "org_1"}) {
		t.Fatal("record should match its own tenant with no filters")
	}
	// Wrong tenant: excluded.
	if recordMatches(rec, spec, Query{OrganizationID: "org_other"}) {
		t.Fatal("record must not match a different tenant")
	}
	// Planned filter that matches.
	match := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "OVS"))
	if !recordMatches(rec, spec, match) {
		t.Fatal("record should match an equal planned filter")
	}
	// Planned filter that does not match.
	noMatch := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "NOPE"))
	if recordMatches(rec, spec, noMatch) {
		t.Fatal("record must not match a differing planned filter")
	}
}

func mustFilter(t *testing.T, field string, value any) QueryFilter {
	t.Helper()
	f, ok := NewQueryFilter(field, value)
	if !ok {
		t.Fatalf("NewQueryFilter(%q,%v) not ok", field, value)
	}
	return f
}
