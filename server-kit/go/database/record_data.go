package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

type RecordValueKind byte

const (
	RecordValueNull RecordValueKind = iota
	RecordValueString
	RecordValueBool
	RecordValueInt
	RecordValueUint
	RecordValueFloat
	RecordValueRaw
)

type RecordValue struct {
	Kind RecordValueKind
	Text string
	Raw  []byte
}

type RecordField struct {
	Name  string
	Value RecordValue
}

type RecordData []RecordField

type RecordFilter struct {
	Field string
	Value RecordValue
}

type RecordQuery struct {
	Filters []RecordFilter
	Limit   int
}

func StringValue(value string) RecordValue {
	return RecordValue{Kind: RecordValueString, Text: value}
}

func BoolValue(value bool) RecordValue {
	if value {
		return RecordValue{Kind: RecordValueBool, Text: "true"}
	}
	return RecordValue{Kind: RecordValueBool, Text: "false"}
}

func IntValue(value int64) RecordValue {
	return RecordValue{Kind: RecordValueInt, Text: strconv.FormatInt(value, 10)}
}

func UintValue(value uint64) RecordValue {
	return RecordValue{Kind: RecordValueUint, Text: strconv.FormatUint(value, 10)}
}

func FloatValue(value float64) RecordValue {
	return RecordValue{Kind: RecordValueFloat, Text: strconv.FormatFloat(value, 'g', -1, 64)}
}

func RawValue(value []byte) RecordValue {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return RecordValue{Kind: RecordValueNull}
	}
	return RecordValue{Kind: RecordValueRaw, Raw: append([]byte(nil), trimmed...)}
}

func RecordValueFromAny(value any) (RecordValue, bool) {
	switch typed := value.(type) {
	case nil:
		return RecordValue{Kind: RecordValueNull}, true
	case RecordValue:
		return typed.Clone(), true
	case string:
		return StringValue(typed), true
	case bool:
		return BoolValue(typed), true
	case int:
		return IntValue(int64(typed)), true
	case int8:
		return IntValue(int64(typed)), true
	case int16:
		return IntValue(int64(typed)), true
	case int32:
		return IntValue(int64(typed)), true
	case int64:
		return IntValue(typed), true
	case uint:
		return UintValue(uint64(typed)), true
	case uint8:
		return UintValue(uint64(typed)), true
	case uint16:
		return UintValue(uint64(typed)), true
	case uint32:
		return UintValue(uint64(typed)), true
	case uint64:
		return UintValue(typed), true
	case float32:
		return FloatValue(float64(typed)), true
	case float64:
		return FloatValue(typed), true
	case json.RawMessage:
		return RawValue(typed), true
	case []byte:
		return RawValue(typed), true
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return RecordValue{}, false
		}
		return RawValue(raw), true
	}
}

func (v RecordValue) Clone() RecordValue {
	out := v
	if len(v.Raw) > 0 {
		out.Raw = append([]byte(nil), v.Raw...)
	}
	return out
}

func (v RecordValue) ScalarIndex() (byte, string, bool) {
	switch v.Kind {
	case RecordValueString:
		return 's', v.Text, true
	case RecordValueBool:
		if strings.EqualFold(v.Text, "true") || v.Text == "1" {
			return 'b', "1", true
		}
		return 'b', "0", true
	case RecordValueInt:
		return 'i', strings.TrimSpace(v.Text), true
	case RecordValueUint:
		return 'u', strings.TrimSpace(v.Text), true
	case RecordValueFloat:
		return 'f', strings.TrimSpace(v.Text), true
	default:
		return 0, "", false
	}
}

func (v RecordValue) Equal(other RecordValue) bool {
	ak, av, aok := v.ScalarIndex()
	bk, bv, bok := other.ScalarIndex()
	if aok && bok {
		return ak == bk && av == bv
	}
	if v.Kind != other.Kind {
		return false
	}
	if v.Kind == RecordValueRaw {
		return bytes.Equal(bytes.TrimSpace(v.Raw), bytes.TrimSpace(other.Raw))
	}
	return strings.TrimSpace(v.Text) == strings.TrimSpace(other.Text)
}

func (v RecordValue) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case RecordValueNull:
		return []byte("null"), nil
	case RecordValueString:
		return strconv.AppendQuote(nil, v.Text), nil
	case RecordValueBool:
		return []byte(strconv.FormatBool(strings.EqualFold(v.Text, "true") || v.Text == "1")), nil
	case RecordValueInt:
		if _, err := strconv.ParseInt(strings.TrimSpace(v.Text), 10, 64); err != nil {
			return nil, err
		}
		return []byte(strings.TrimSpace(v.Text)), nil
	case RecordValueUint:
		if _, err := strconv.ParseUint(strings.TrimSpace(v.Text), 10, 64); err != nil {
			return nil, err
		}
		return []byte(strings.TrimSpace(v.Text)), nil
	case RecordValueFloat:
		if _, err := strconv.ParseFloat(strings.TrimSpace(v.Text), 64); err != nil {
			return nil, err
		}
		return []byte(strings.TrimSpace(v.Text)), nil
	case RecordValueRaw:
		if len(bytes.TrimSpace(v.Raw)) == 0 {
			return []byte("null"), nil
		}
		return append([]byte(nil), bytes.TrimSpace(v.Raw)...), nil
	default:
		return nil, fmt.Errorf("unsupported record value kind %d", v.Kind)
	}
}

func (v *RecordValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*v = RecordValue{Kind: RecordValueNull}
		return nil
	}
	switch trimmed[0] {
	case '"':
		text, err := strconv.Unquote(string(trimmed))
		if err != nil {
			return err
		}
		*v = StringValue(text)
	case 't', 'f':
		b, err := strconv.ParseBool(string(trimmed))
		if err != nil {
			return err
		}
		*v = BoolValue(b)
	case '{', '[':
		*v = RawValue(trimmed)
	default:
		text := string(trimmed)
		if strings.ContainsAny(text, ".eE") {
			*v = RecordValue{Kind: RecordValueFloat, Text: text}
		} else if strings.HasPrefix(text, "-") {
			*v = RecordValue{Kind: RecordValueInt, Text: text}
		} else {
			*v = RecordValue{Kind: RecordValueInt, Text: text}
		}
	}
	return nil
}

func RecordDataFromPairs(pairs ...RecordField) RecordData {
	if len(pairs) == 0 {
		return nil
	}
	out := make(RecordData, 0, len(pairs))
	for _, field := range pairs {
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" {
			continue
		}
		field.Value = field.Value.Clone()
		out = append(out, field)
	}
	return out.Normalize()
}

func (d RecordData) Normalize() RecordData {
	if len(d) == 0 {
		return nil
	}
	if d.isSortedUnique() {
		out := make(RecordData, len(d))
		for i, field := range d {
			field.Name = strings.TrimSpace(field.Name)
			field.Value = field.Value.Clone()
			out[i] = field
		}
		return out
	}
	out := make(RecordData, 0, len(d))
	seen := make(map[string]int, len(d))
	for _, field := range d {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		field.Name = name
		field.Value = field.Value.Clone()
		if idx, ok := seen[name]; ok {
			out[idx] = field
			continue
		}
		seen[name] = len(out)
		out = append(out, field)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (d RecordData) isSortedUnique() bool {
	previous := ""
	for _, field := range d {
		name := strings.TrimSpace(field.Name)
		if name == "" || name <= previous {
			return false
		}
		previous = name
	}
	return true
}

func (d RecordData) Clone() RecordData {
	if len(d) == 0 {
		return nil
	}
	out := make(RecordData, len(d))
	for i, field := range d {
		out[i] = RecordField{Name: field.Name, Value: field.Value.Clone()}
	}
	return out
}

func (d RecordData) Get(name string) (RecordValue, bool) {
	name = strings.TrimSpace(name)
	for _, field := range d {
		if field.Name == name {
			return field.Value, true
		}
	}
	return RecordValue{}, false
}

func (d RecordData) With(name string, value RecordValue) RecordData {
	out := d.Clone()
	out = append(out, RecordField{Name: name, Value: value.Clone()})
	return out.Normalize()
}

func (d RecordData) Merge(patch RecordData) RecordData {
	base := d.Normalize()
	overrides := patch.Normalize()
	if len(base) == 0 {
		return overrides
	}
	if len(overrides) == 0 {
		return base
	}
	out := make(RecordData, 0, len(base)+len(overrides))
	i, j := 0, 0
	for i < len(base) && j < len(overrides) {
		left := base[i]
		right := overrides[j]
		switch {
		case left.Name < right.Name:
			out = append(out, RecordField{Name: left.Name, Value: left.Value.Clone()})
			i++
		case left.Name > right.Name:
			out = append(out, RecordField{Name: right.Name, Value: right.Value.Clone()})
			j++
		default:
			out = append(out, RecordField{Name: right.Name, Value: right.Value.Clone()})
			i++
			j++
		}
	}
	for ; i < len(base); i++ {
		field := base[i]
		out = append(out, RecordField{Name: field.Name, Value: field.Value.Clone()})
	}
	for ; j < len(overrides); j++ {
		field := overrides[j]
		out = append(out, RecordField{Name: field.Name, Value: field.Value.Clone()})
	}
	return out
}

func (d RecordData) Matches(filters []RecordFilter) bool {
	for _, filter := range filters {
		actual, ok := d.Get(filter.Field)
		if !ok || !actual.Equal(filter.Value) {
			return false
		}
	}
	return true
}

func (d RecordData) MarshalJSON() ([]byte, error) {
	if len(d) == 0 {
		return []byte("{}"), nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, field := range d.Normalize() {
		if i > 0 {
			b.WriteByte(',')
		}
		value, err := field.Value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(strconv.AppendQuote(nil, field.Name))
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (d *RecordData) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*d = nil
		return nil
	}
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return errors.New("record data must be a json object")
	}
	raw, err := extension.ObjectFromJSON(trimmed)
	if err != nil {
		return err
	}
	fields := make(RecordData, 0, len(raw))
	for name, payload := range raw {
		value, err := recordValueFromExtension(payload)
		if err != nil {
			return err
		}
		fields = append(fields, RecordField{Name: name, Value: value})
	}
	*d = fields.Normalize()
	return nil
}

func recordValueFromExtension(value extension.Value) (RecordValue, error) {
	switch value.Kind() {
	case extension.KindNull:
		return RecordValue{Kind: RecordValueNull}, nil
	case extension.KindString:
		text, _ := value.StringValue()
		return StringValue(text), nil
	case extension.KindBool:
		b, _ := value.BoolValue()
		return BoolValue(b), nil
	case extension.KindInt:
		i, _ := value.IntValue()
		return IntValue(i), nil
	case extension.KindUint:
		u, _ := value.UintValue()
		return UintValue(u), nil
	case extension.KindFloat:
		f, _ := value.FloatValue()
		return FloatValue(f), nil
	default:
		raw, err := value.MarshalJSON()
		if err != nil {
			return RecordValue{}, err
		}
		return RawValue(raw), nil
	}
}

func (q RecordQuery) Normalize() RecordQuery {
	q.Filters = normalizeRecordFilters(q.Filters)
	return q
}

func normalizeRecordFilters(filters []RecordFilter) []RecordFilter {
	if len(filters) == 0 {
		return nil
	}
	if len(filters) == 1 {
		field := strings.TrimSpace(filters[0].Field)
		if field == "" {
			return nil
		}
		filter := RecordFilter{Field: field, Value: filters[0].Value.Clone()}
		return []RecordFilter{filter}
	}
	out := make([]RecordFilter, 0, len(filters))
	seen := make(map[string]int, len(filters))
	for _, filter := range filters {
		field := strings.TrimSpace(filter.Field)
		if field == "" {
			continue
		}
		filter.Field = field
		filter.Value = filter.Value.Clone()
		if idx, ok := seen[field]; ok {
			out[idx] = filter
			continue
		}
		seen[field] = len(out)
		out = append(out, filter)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}
