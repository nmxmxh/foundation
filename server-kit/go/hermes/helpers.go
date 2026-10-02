package hermes

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

var emptyRecordKeys = map[string]struct{}{}

func normalizeSpec(spec ProjectionSpec) (ProjectionSpec, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Domain = strings.TrimSpace(spec.Domain)
	spec.Collection = strings.TrimSpace(spec.Collection)
	if spec.Name == "" || spec.Domain == "" || spec.Collection == "" {
		return ProjectionSpec{}, ErrInvalidProjection
	}
	spec.IndexedFields = normalizeIndexedFields(spec.IndexedFields, spec.MaxIndexes)
	spec.RangeIndexedFields = normalizeIndexedFields(spec.RangeIndexedFields, spec.MaxRangeIndexes)
	if spec.MaxRecords <= 0 {
		spec.MaxRecords = defaultMaxRecords
	}
	if spec.MaxBytes <= 0 {
		spec.MaxBytes = defaultMaxBytes
	}
	if spec.MaxTombstones < 0 {
		spec.MaxTombstones = 0
	}
	if spec.MaxTombstones == 0 {
		spec.MaxTombstones = min(spec.MaxRecords, defaultMaxTombstones)
	}
	if spec.MaxAppliedEvents <= 0 {
		spec.MaxAppliedEvents = defaultMaxAppliedEvents
	}
	return spec, nil
}

func normalizeIndexedFields(fields []string, maxIndexes int) []string {
	limit := len(fields)
	if maxIndexes > 0 && maxIndexes < limit {
		limit = maxIndexes
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || field == "organization_id" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		out = append(out, field)
		seen[field] = struct{}{}
		if len(out) == limit {
			break
		}
	}
	return out
}

func normalizeRecord(rec database.DomainRecord) (database.DomainRecord, error) {
	rec.Domain = strings.TrimSpace(rec.Domain)
	rec.Collection = strings.TrimSpace(rec.Collection)
	rec.OrganizationID = strings.TrimSpace(rec.OrganizationID)
	rec.RecordID = strings.TrimSpace(rec.RecordID)
	if rec.Domain == "" || rec.Collection == "" || rec.OrganizationID == "" || rec.RecordID == "" {
		return database.DomainRecord{}, ErrInvalidEvent
	}
	rec.Data = normalizeRecordDataWithOrganization(rec.Data, rec.OrganizationID)
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	return rec, nil
}

func normalizePatchRecord(rec database.DomainRecord) (database.DomainRecord, error) {
	rec.Domain = strings.TrimSpace(rec.Domain)
	rec.Collection = strings.TrimSpace(rec.Collection)
	rec.OrganizationID = strings.TrimSpace(rec.OrganizationID)
	rec.RecordID = strings.TrimSpace(rec.RecordID)
	if rec.Domain == "" || rec.Collection == "" || rec.OrganizationID == "" || rec.RecordID == "" {
		return database.DomainRecord{}, ErrInvalidEvent
	}
	rec.Data = normalizeRecordDataWithOrganization(rec.Data, rec.OrganizationID)
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	return rec, nil
}

func normalizeRecordDataWithOrganization(data database.RecordData, organizationID string) database.RecordData {
	normalized := data.Normalize()
	orgValue := database.StringValue(organizationID)
	idx := sort.Search(len(normalized), func(i int) bool {
		return normalized[i].Name >= "organization_id"
	})
	if idx < len(normalized) && normalized[idx].Name == "organization_id" {
		normalized[idx].Value = orgValue
		return normalized
	}
	normalized = append(normalized, database.RecordField{})
	copy(normalized[idx+1:], normalized[idx:])
	normalized[idx] = database.RecordField{Name: "organization_id", Value: orgValue}
	return normalized
}

func copyRecord(in database.DomainRecord) database.DomainRecord {
	out := in
	out.Data = in.Data.Clone()
	if len(in.Vector) > 0 {
		out.Vector = append([]float32(nil), in.Vector...)
	}
	return out
}

func recordFromView(view RecordView) database.DomainRecord {
	return copyRecord(database.DomainRecord{
		Domain:         view.Domain,
		Collection:     view.Collection,
		OrganizationID: view.OrganizationID,
		RecordID:       view.RecordID,
		Data:           view.Data,
		Vector:         view.Vector,
		CreatedAt:      view.CreatedAt,
		UpdatedAt:      view.UpdatedAt,
	})
}

// recordKey builds the partition key for a record.
//
// Components are escaped so the join is injective. Without escaping,
// organization "org_1" with record "a|b" and organization "org_1|a" with record
// "b" both produce "signals|ticks|org_1|a|b", which lets one tenant overwrite
// another tenant's record on a projection shared by many organizations. The
// separator is escaped before the escape character itself, so the encoding
// round-trips.
func recordKey(domain, collection, organizationID, recordID string) string {
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)
	recordID = strings.TrimSpace(recordID)
	// Test for the separator before building, not inside the concatenation.
	// Calling a helper in the operand list pushes the result past the
	// compiler's stack buffer, which cost two extra allocations per key.
	if hasKeySeparator(domain) {
		domain = escapeKeyComponent(domain)
	}
	if hasKeySeparator(collection) {
		collection = escapeKeyComponent(collection)
	}
	if hasKeySeparator(organizationID) {
		organizationID = escapeKeyComponent(organizationID)
	}
	if hasKeySeparator(recordID) {
		recordID = escapeKeyComponent(recordID)
	}
	return recordKeyFromEscaped(domain, collection, organizationID, recordID)
}

func hasKeySeparator(value string) bool {
	for i := range len(value) {
		if value[i] == keySeparator || value[i] == keyEscape {
			return true
		}
	}
	return false
}

// escapeKeyComponent escapes the recordKey separator and the escape character.
// The common case costs one scan and no allocation.
func escapeKeyComponent(value string) string {
	value = strings.TrimSpace(value)
	needsEscape := false
	for i := range len(value) {
		if value[i] == keySeparator || value[i] == keyEscape {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return value
	}
	var b strings.Builder
	b.Grow(len(value) + 8)
	for i := range len(value) {
		switch value[i] {
		case keyEscape:
			b.WriteString(keyEscapeToken)
		case keySeparator:
			b.WriteString(keySeparatorToken)
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// recordKeyFromEscaped joins already-escaped components.
func recordKeyFromEscaped(domain, collection, organizationID, recordID string) string {
	return domain + "|" + collection + "|" + organizationID + "|" + recordID
}

const (
	keySeparator      = '|'
	keyEscape         = '\\'
	keyEscapeToken    = `\\`
	keySeparatorToken = `\|`
)

func scopeKey(domain, collection, organizationID string) recordScope {
	return recordScope{
		domain:         strings.TrimSpace(domain),
		collection:     strings.TrimSpace(collection),
		organizationID: strings.TrimSpace(organizationID),
	}
}

func recordMatches(rec database.DomainRecord, spec ProjectionSpec, query Query) bool {
	if rec.Domain != spec.Domain || rec.Collection != spec.Collection {
		return false
	}
	// Trim the wanted organization once. This runs per candidate record, so the
	// previous two TrimSpace calls per record were the dominant cost of a
	// filter-free columnar scan.
	wantOrg := strings.TrimSpace(query.OrganizationID)
	if wantOrg != "" && rec.OrganizationID != wantOrg {
		return false
	}
	if query.Plan.count > 0 {
		return matchesPlannedFilters(rec.Data, query.Plan)
	}
	return true
}

func matchesPlannedFilters(data database.RecordData, plan QueryPlan) bool {
	return forEachPlannedFilter(plan, func(filter QueryFilter) bool {
		actual, ok := data.Get(filter.Field)
		if !ok {
			return false
		}
		kind, value, ok := actual.ScalarIndex()
		return ok && kind == filter.Kind && value == filter.Value
	})
}

func forEachPlannedFilter(plan QueryPlan, fn func(QueryFilter) bool) bool {
	switch plan.count {
	case 0:
		return true
	case 1:
		return fn(plan.first)
	default:
		for _, filter := range plan.filters {
			if !fn(filter) {
				return false
			}
		}
		return true
	}
}

func normalizeQuery(query Query) Query {
	query.OrganizationID = strings.TrimSpace(query.OrganizationID)
	return query
}

func NewQueryFilter(field string, value any) (QueryFilter, bool) {
	recordValue, ok := database.RecordValueFromAny(value)
	if !ok {
		return QueryFilter{}, false
	}
	kind, encoded, ok := recordValue.ScalarIndex()
	if !ok {
		return QueryFilter{}, false
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return QueryFilter{}, false
	}
	return QueryFilter{Field: field, Kind: kind, Value: encoded}, true
}

func (q Query) RecordQuery() database.RecordQuery {
	return database.RecordQuery{Limit: q.Limit, Filters: q.Plan.RecordFilters()}.Normalize()
}

func QueryWithFilters(organizationID string, limit int, filters ...QueryFilter) Query {
	planned := make([]QueryFilter, 0, len(filters))
	for _, filter := range filters {
		filter.Field = strings.TrimSpace(filter.Field)
		if filter.Field == "" {
			continue
		}
		planned = append(planned, filter)
	}
	sort.Slice(planned, func(i int, j int) bool {
		return planned[i].Field < planned[j].Field
	})
	plan := QueryPlan{}
	if len(planned) == 1 {
		plan = QueryPlan{first: planned[0], count: 1}
	} else if len(planned) > 1 {
		plan = QueryPlan{first: planned[0], filters: planned, count: len(planned)}
	}
	return Query{
		OrganizationID: organizationID,
		Limit:          limit,
		Plan:           plan,
	}
}

func QueryFromRecordQuery(organizationID string, query database.RecordQuery) Query {
	if len(query.Filters) == 1 {
		filter := query.Filters[0]
		filter.Field = strings.TrimSpace(filter.Field)
		if filter.Field == "" {
			return Query{OrganizationID: organizationID, Limit: query.Limit}
		}
		kind, encoded, ok := filter.Value.ScalarIndex()
		if !ok {
			return Query{OrganizationID: organizationID, Limit: query.Limit}
		}
		queryFilter := QueryFilter{Field: filter.Field, Kind: kind, Value: encoded}
		return Query{OrganizationID: organizationID, Limit: query.Limit, Plan: QueryPlan{first: queryFilter, count: 1}}
	}
	filters := make([]QueryFilter, 0, len(query.Filters))
	for _, filter := range query.Filters {
		filter.Field = strings.TrimSpace(filter.Field)
		if filter.Field == "" {
			continue
		}
		kind, encoded, ok := filter.Value.ScalarIndex()
		if !ok {
			continue
		}
		filters = append(filters, QueryFilter{Field: filter.Field, Kind: kind, Value: encoded})
	}
	return QueryWithFilters(organizationID, query.Limit, filters...)
}

func (p QueryPlan) RecordFilters() []database.RecordFilter {
	if p.count == 0 {
		return nil
	}
	filters := make([]database.RecordFilter, 0, p.count)
	forEachPlannedFilter(p, func(filter QueryFilter) bool {
		filters = append(filters, database.RecordFilter{Field: filter.Field, Value: queryFilterValue(filter)})
		return true
	})
	return filters
}

func queryFilterValue(filter QueryFilter) database.RecordValue {
	switch filter.Kind {
	case 's':
		return database.StringValue(filter.Value)
	case 'b':
		return database.BoolValue(filter.Value == "1" || strings.EqualFold(filter.Value, "true"))
	case 'i':
		value, err := strconv.ParseInt(filter.Value, 10, 64)
		if err != nil {
			return database.StringValue(filter.Value)
		}
		return database.IntValue(value)
	case 'u':
		value, err := strconv.ParseUint(filter.Value, 10, 64)
		if err != nil {
			return database.StringValue(filter.Value)
		}
		return database.UintValue(value)
	case 'f':
		value, err := strconv.ParseFloat(filter.Value, 64)
		if err != nil {
			return database.StringValue(filter.Value)
		}
		return database.FloatValue(value)
	default:
		return database.StringValue(filter.Value)
	}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
