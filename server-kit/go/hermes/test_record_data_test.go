package hermes

import "github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"

func testRecordData(values map[string]any) database.RecordData {
	if len(values) == 0 {
		return nil
	}
	fields := make(database.RecordData, 0, len(values))
	for name, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		if !ok {
			panic("unsupported record field " + name)
		}
		fields = append(fields, database.RecordField{Name: name, Value: value})
	}
	return fields.Normalize()
}

func testRecordQuery(limit int, values map[string]any) database.RecordQuery {
	query := database.RecordQuery{Limit: limit}
	if len(values) == 0 {
		return query
	}
	query.Filters = make([]database.RecordFilter, 0, len(values))
	for field, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		if !ok {
			panic("unsupported record filter " + field)
		}
		query.Filters = append(query.Filters, database.RecordFilter{Field: field, Value: value})
	}
	return query.Normalize()
}

func recordDataStringEquals(data database.RecordData, field string, want string) bool {
	value, ok := data.Get(field)
	return ok && value.Kind == database.RecordValueString && value.Text == want
}

func recordDataIntEquals(data database.RecordData, field string, want int64) bool {
	value, ok := data.Get(field)
	_, text, scalar := value.ScalarIndex()
	return ok && scalar && text == database.IntValue(want).Text
}
