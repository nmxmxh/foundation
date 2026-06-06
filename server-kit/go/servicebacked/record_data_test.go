//go:build servicebacked

package servicebacked

import (
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func serviceRecordData(values map[string]any) database.RecordData {
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

func serviceRecordQuery(limit int, values map[string]any) database.RecordQuery {
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

func serviceRecordStringEquals(data database.RecordData, field, want string) bool {
	value, ok := data.Get(field)
	return ok && value.Kind == database.RecordValueString && value.Text == want
}

func serviceObject(values map[string]any) extension.Object {
	value, err := extension.FromJSON(values)
	if err != nil {
		panic(err)
	}
	object, ok := value.ObjectValue()
	if !ok {
		panic("service-backed test value is not object")
	}
	return object
}

func serviceRedisValues(values map[string]any) rediskit.Values {
	out := make(rediskit.Values, 0, len(values))
	for field, value := range values {
		out = append(out, rediskit.Field(field, value))
	}
	return out
}
