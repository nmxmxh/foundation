package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
)

type Value struct {
	raw any
}

type Object map[string]Value

func String(value string) Value { return Value{raw: value} }
func Bool(value bool) Value     { return Value{raw: value} }
func Int(value int64) Value     { return Value{raw: value} }
func Float(value float64) Value { return Value{raw: value} }
func List(value []Value) Value {
	out := make([]any, 0, len(value))
	for _, item := range value {
		out = append(out, item.Interface())
	}
	return Value{raw: out}
}
func ObjectValue(value Object) Value { return Value{raw: value.InterfaceMap()} }

func ObjectFromMap(input map[string]any) Object {
	if len(input) == 0 {
		return Object{}
	}
	out := make(Object, len(input))
	for key, value := range input {
		out[key] = valueFromAny(value)
	}
	return out
}

func (o Object) InterfaceMap() map[string]any {
	out := make(map[string]any, len(o))
	for key, value := range o {
		out[key] = value.Interface()
	}
	return out
}

func (v Value) Interface() any {
	switch typed := v.raw.(type) {
	case Value:
		return typed.Interface()
	case Object:
		return typed.InterfaceMap()
	default:
		return typed
	}
}

func (v Value) StringValue() (string, bool) {
	switch typed := v.raw.(type) {
	case string:
		return typed, true
	case fmt.Stringer:
		return typed.String(), true
	default:
		return "", false
	}
}

func (v Value) BoolValue() (bool, bool) {
	typed, ok := v.raw.(bool)
	return typed, ok
}

func (v Value) Int64Value() (int64, bool) {
	switch typed := v.raw.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) > uint64(math.MaxInt64) {
			return 0, false
		}
		return int64(typed), true
	case uint64:
		if typed > uint64(math.MaxInt64) {
			return 0, false
		}
		return int64(typed), true
	case float64:
		if math.Trunc(typed) != typed {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		value, err := typed.Int64()
		return value, err == nil
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value, err == nil
	default:
		return 0, false
	}
}

func (v Value) ObjectValue() (Object, bool) {
	switch typed := v.raw.(type) {
	case Object:
		return typed.Clone(), true
	case map[string]any:
		return ObjectFromMap(typed), true
	default:
		return nil, false
	}
}

func (v Value) ListValue() ([]Value, bool) {
	switch typed := v.raw.(type) {
	case []Value:
		return append([]Value(nil), typed...), true
	case []any:
		out := make([]Value, 0, len(typed))
		for _, item := range typed {
			out = append(out, valueFromAny(item))
		}
		return out, true
	default:
		return nil, false
	}
}

func (o Object) Clone() Object {
	if len(o) == 0 {
		return Object{}
	}
	out := make(Object, len(o))
	maps.Copy(out, o)
	return out
}

func (o Object) GetString(key string) (string, bool) {
	value, ok := o[key]
	if !ok {
		return "", false
	}
	return value.StringValue()
}

func (v Value) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Interface())
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	*v = valueFromAny(raw)
	return nil
}

func valueFromAny(input any) Value {
	switch typed := input.(type) {
	case Value:
		return typed
	case Object:
		return ObjectValue(typed)
	case map[string]any:
		return ObjectValue(ObjectFromMap(typed))
	case []any:
		out := make([]Value, 0, len(typed))
		for _, item := range typed {
			out = append(out, valueFromAny(item))
		}
		return List(out)
	default:
		return Value{raw: typed}
	}
}
