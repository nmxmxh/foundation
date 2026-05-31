package hermes

import (
	"context"
	"fmt"
	"maps"
	"math"
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
	rec.Data = copyMap(rec.Data)
	if rec.Data == nil {
		rec.Data = map[string]any{}
	}
	rec.Data["organization_id"] = rec.OrganizationID
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	return rec, nil
}

func copyRecord(in database.DomainRecord) database.DomainRecord {
	out := in
	out.Data = copyMap(in.Data)
	if len(in.Vector) > 0 {
		out.Vector = append([]float32(nil), in.Vector...)
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func recordKey(domain, collection, organizationID, recordID string) string {
	return strings.TrimSpace(domain) + "|" +
		strings.TrimSpace(collection) + "|" +
		strings.TrimSpace(organizationID) + "|" +
		strings.TrimSpace(recordID)
}

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
	if strings.TrimSpace(query.OrganizationID) != "" && rec.OrganizationID != strings.TrimSpace(query.OrganizationID) {
		return false
	}
	return matchesFilter(rec.Data, query.Filters)
}

func matchesFilter(record map[string]any, filters map[string]any) bool {
	for key, expected := range filters {
		actual, ok := record[key]
		if !ok || !equalValue(actual, expected) {
			return false
		}
	}
	return true
}

func equalValue(actual any, expected any) bool {
	as, aok := comparableString(actual)
	es, eok := comparableString(expected)
	if aok && eok {
		return as == es
	}
	return strings.TrimSpace(fmt.Sprintf("%v", actual)) == strings.TrimSpace(fmt.Sprintf("%v", expected))
}

func comparableString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), true
	case int:
		return strconv.Itoa(v), true
	case int8:
		return strconv.FormatInt(int64(v), 10), true
	case int16:
		return strconv.FormatInt(int64(v), 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case uint:
		return strconv.FormatUint(uint64(v), 10), true
	case uint8:
		return strconv.FormatUint(uint64(v), 10), true
	case uint16:
		return strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		if v == math.Trunc(v) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
}

func indexableFieldValue(value any) (byte, string, bool) {
	switch typed := value.(type) {
	case string:
		return 's', typed, true
	case bool:
		if typed {
			return 'b', "1", true
		}
		return 'b', "0", true
	case int:
		return 'i', strconv.Itoa(typed), true
	case int8:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int16:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int32:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int64:
		return 'i', strconv.FormatInt(typed, 10), true
	case uint:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return 'u', strconv.FormatUint(typed, 10), true
	case float64:
		if typed == math.Trunc(typed) {
			return 'i', strconv.FormatInt(int64(typed), 10), true
		}
		return 'f', strconv.FormatFloat(typed, 'f', -1, 64), true
	default:
		return 0, "", false
	}
}

func appendListCandidate(candidates []database.DomainRecord, rec database.DomainRecord, limit int) []database.DomainRecord {
	if limit <= 0 {
		return append(candidates, rec)
	}
	insertAt := sort.Search(len(candidates), func(i int) bool {
		return recordBefore(rec, candidates[i])
	})
	if insertAt >= limit {
		return candidates
	}
	if len(candidates) < limit {
		candidates = append(candidates, database.DomainRecord{})
		copy(candidates[insertAt+1:], candidates[insertAt:])
		candidates[insertAt] = rec
		return candidates
	}
	copy(candidates[insertAt+1:], candidates[insertAt:len(candidates)-1])
	candidates[insertAt] = rec
	return candidates
}

func recordBefore(left, right database.DomainRecord) bool {
	if left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.RecordID < right.RecordID
	}
	return left.UpdatedAt.After(right.UpdatedAt)
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
