// Package extension provides a typed open-value container for extension data.
//
// It is intentionally narrower than any/interface{}: callers keep an open
// schema surface without pushing type assertions into hot product paths.
package extension

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Kind uint8

const (
	KindNull Kind = iota
	KindString
	KindBool
	KindInt
	KindUint
	KindFloat
	KindBytes
	KindList
	KindObject
)

type Value struct {
	kind  Kind
	str   string
	i64   int64
	u64   uint64
	f64   float64
	b     bool
	bytes []byte
	list  []Value
	obj   Object
}

type Object map[string]Value

// ObjectFromMap converts an open JSON-like map into a typed extension object.
// Use it at serialization boundaries; hot handlers should prefer constructing
// extension.Object directly when the fields are known.
func ObjectFromMap(input map[string]any) Object {
	if len(input) == 0 {
		return Object{}
	}
	out := make(Object, len(input))
	for key, value := range input {
		typed, err := FromJSON(value)
		if err != nil {
			out[key] = String(fmt.Sprint(value))
			continue
		}
		out[key] = typed
	}
	return out
}

func Null() Value           { return Value{kind: KindNull} }
func String(v string) Value { return Value{kind: KindString, str: v} }
func Bool(v bool) Value     { return Value{kind: KindBool, b: v} }
func Int(v int64) Value     { return Value{kind: KindInt, i64: v} }
func Uint(v uint64) Value   { return Value{kind: KindUint, u64: v} }
func Float(v float64) Value { return Value{kind: KindFloat, f64: v} }
func Bytes(v []byte) Value  { return Value{kind: KindBytes, bytes: append([]byte(nil), v...)} }
func List(v []Value) Value  { return Value{kind: KindList, list: append([]Value(nil), v...)} }
func ObjectValue(v Object) Value {
	return Value{kind: KindObject, obj: v.Clone()}
}

func listValueOwned(v []Value) Value {
	return Value{kind: KindList, list: v}
}

func objectValueOwned(v Object) Value {
	if v == nil {
		v = Object{}
	}
	return Value{kind: KindObject, obj: v}
}

func (v Value) Kind() Kind { return v.kind }

func (v Value) StringValue() (string, bool) { return v.str, v.kind == KindString }
func (v Value) BoolValue() (bool, bool)     { return v.b, v.kind == KindBool }
func (v Value) IntValue() (int64, bool)     { return v.i64, v.kind == KindInt }
func (v Value) UintValue() (uint64, bool)   { return v.u64, v.kind == KindUint }
func (v Value) FloatValue() (float64, bool) { return v.f64, v.kind == KindFloat }

func (v Value) BytesValue() ([]byte, bool) {
	if v.kind != KindBytes {
		return nil, false
	}
	return append([]byte(nil), v.bytes...), true
}

func (v Value) ListValue() ([]Value, bool) {
	if v.kind != KindList {
		return nil, false
	}
	return append([]Value(nil), v.list...), true
}

func (v Value) ObjectValue() (Object, bool) {
	if v.kind != KindObject {
		return nil, false
	}
	return v.obj.Clone(), true
}

// ObjectView returns the object stored in v without cloning. Callers must not
// mutate the returned object unless they own the Value.
func (v Value) ObjectView() (Object, bool) {
	if v.kind != KindObject {
		return nil, false
	}
	if v.obj == nil {
		return Object{}, true
	}
	return v.obj, true
}

func (o Object) Clone() Object {
	if len(o) == 0 {
		return Object{}
	}
	out := make(Object, len(o))
	for key, value := range o {
		out[key] = value.Clone()
	}
	return out
}

func (o Object) InterfaceMap() map[string]any {
	if len(o) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(o))
	for key, value := range o {
		out[key] = value.Interface()
	}
	return out
}

func (v Value) Clone() Value {
	switch v.kind {
	case KindBytes:
		return Bytes(v.bytes)
	case KindList:
		return List(v.list)
	case KindObject:
		return ObjectValue(v.obj)
	default:
		return v
	}
}

func (v Value) Interface() any {
	switch v.kind {
	case KindNull:
		return nil
	case KindString:
		return v.str
	case KindBool:
		return v.b
	case KindInt:
		return v.i64
	case KindUint:
		return v.u64
	case KindFloat:
		return v.f64
	case KindBytes:
		return append([]byte(nil), v.bytes...)
	case KindList:
		if allListItemsAreObjects(v.list) {
			out := make([]map[string]any, 0, len(v.list))
			for _, item := range v.list {
				out = append(out, item.obj.InterfaceMap())
			}
			return out
		}
		out := make([]any, 0, len(v.list))
		for _, item := range v.list {
			out = append(out, item.Interface())
		}
		return out
	case KindObject:
		return v.obj.InterfaceMap()
	default:
		return nil
	}
}

func allListItemsAreObjects(values []Value) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value.kind != KindObject {
			return false
		}
	}
	return true
}

// MarshalJSON emits canonical JSON (object keys sorted). Use it for drift,
// signing, and stable logs. It writes through a single growing buffer via the
// internal append-down path so a nested document costs one backing allocation
// (plus the sorted-key slice per object) instead of one buffer per node.
func (v Value) MarshalJSON() ([]byte, error) {
	return v.appendJSON(make([]byte, 0, 64))
}

func (v Value) appendJSON(dst []byte) ([]byte, error) {
	switch v.kind {
	case KindNull:
		return append(dst, "null"...), nil
	case KindString:
		return strconv.AppendQuote(dst, v.str), nil
	case KindBool:
		return strconv.AppendBool(dst, v.b), nil
	case KindInt:
		return strconv.AppendInt(dst, v.i64, 10), nil
	case KindUint:
		return strconv.AppendUint(dst, v.u64, 10), nil
	case KindFloat:
		return strconv.AppendFloat(dst, v.f64, 'g', -1, 64), nil
	case KindBytes:
		return strconv.AppendQuote(dst, base64.StdEncoding.EncodeToString(v.bytes)), nil
	case KindList:
		return appendValueList(dst, v.list)
	case KindObject:
		return v.obj.appendJSON(dst)
	default:
		return nil, fmt.Errorf("unknown extension value kind %d", v.kind)
	}
}

func (o Object) MarshalJSON() ([]byte, error) {
	return o.appendJSON(make([]byte, 0, len(o)*24+2))
}

func (o Object) appendJSON(dst []byte) ([]byte, error) {
	if len(o) == 0 {
		return append(dst, '{', '}'), nil
	}
	dst = append(dst, '{')
	keys := o.Keys()
	for i, key := range keys {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendQuote(dst, key)
		dst = append(dst, ':')
		var err error
		if dst, err = o[key].appendJSON(dst); err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

func appendValueList(dst []byte, values []Value) ([]byte, error) {
	if len(values) == 0 {
		return append(dst, '[', ']'), nil
	}
	dst = append(dst, '[')
	for i := range values {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, err = values[i].appendJSON(dst); err != nil {
			return nil, err
		}
	}
	return append(dst, ']'), nil
}

// MarshalJSONFast emits valid JSON without sorting object keys. Use it for
// non-canonical compatibility responses where stable byte order is unnecessary.
// It shares the same single-buffer append-down path as MarshalJSON.
func (o Object) MarshalJSONFast() ([]byte, error) {
	return o.appendJSONFast(make([]byte, 0, len(o)*24+2))
}

func (o Object) appendJSONFast(dst []byte) ([]byte, error) {
	if len(o) == 0 {
		return append(dst, '{', '}'), nil
	}
	dst = append(dst, '{')
	i := 0
	for key, value := range o {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendQuote(dst, key)
		dst = append(dst, ':')
		var err error
		if dst, err = value.appendJSONFast(dst); err != nil {
			return nil, err
		}
		i++
	}
	return append(dst, '}'), nil
}

func (v Value) appendJSONFast(dst []byte) ([]byte, error) {
	switch v.kind {
	case KindObject:
		return v.obj.appendJSONFast(dst)
	case KindList:
		return appendValueListFast(dst, v.list)
	default:
		return v.appendJSON(dst)
	}
}

func appendValueListFast(dst []byte, values []Value) ([]byte, error) {
	if len(values) == 0 {
		return append(dst, '[', ']'), nil
	}
	dst = append(dst, '[')
	for i := range values {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, err = values[i].appendJSONFast(dst); err != nil {
			return nil, err
		}
	}
	return append(dst, ']'), nil
}

func (v *Value) UnmarshalJSON(data []byte) error {
	parsed, err := decodeValueBytes(data)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

func (o *Object) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*o = Object{}
		return nil
	}
	parsed, err := decodeObjectBytes(trimmed)
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

func ObjectFromJSON(data []byte) (Object, error) {
	var out Object
	if err := out.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeValueBytes(data []byte) (Value, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Null(), io.EOF
	}
	parser := jsonParser{data: trimmed}
	value, err := parser.parseValue()
	if err != nil {
		return Null(), err
	}
	parser.skipSpace()
	if parser.pos != len(parser.data) {
		return Null(), errors.New("extension JSON contains trailing data")
	}
	return value, nil
}

func decodeObjectBytes(data []byte) (Object, error) {
	parser := jsonParser{data: bytes.TrimSpace(data)}
	out, err := parser.parseObject()
	if err != nil {
		return nil, err
	}
	parser.skipSpace()
	if parser.pos != len(parser.data) {
		return nil, errors.New("extension JSON contains trailing data")
	}
	return out, nil
}

type jsonParser struct {
	data []byte
	pos  int
}

func (p *jsonParser) parseValue() (Value, error) {
	p.skipSpace()
	if p.pos >= len(p.data) {
		return Null(), io.EOF
	}
	switch p.data[p.pos] {
	case 'n':
		if p.consumeLiteral("null") {
			return Null(), nil
		}
	case 't':
		if p.consumeLiteral("true") {
			return Bool(true), nil
		}
	case 'f':
		if p.consumeLiteral("false") {
			return Bool(false), nil
		}
	case '"':
		text, err := p.parseString()
		if err != nil {
			return Null(), err
		}
		return String(text), nil
	case '{':
		obj, err := p.parseObject()
		if err != nil {
			return Null(), err
		}
		return Value{kind: KindObject, obj: obj}, nil
	case '[':
		list, err := p.parseList()
		if err != nil {
			return Null(), err
		}
		return Value{kind: KindList, list: list}, nil
	default:
		number, err := p.parseNumber()
		if err != nil {
			return Null(), err
		}
		return valueFromJSONNumber(json.Number(number))
	}
	return Null(), fmt.Errorf("invalid JSON value at byte %d", p.pos)
}

func (p *jsonParser) parseObject() (Object, error) {
	p.skipSpace()
	if !p.consumeByte('{') {
		return nil, errors.New("extension object must be a json object")
	}
	out := Object{}
	p.skipSpace()
	if p.consumeByte('}') {
		return out, nil
	}
	for {
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if !p.consumeByte(':') {
			return nil, errors.New("extension object key must be followed by ':'")
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out[key] = value
		p.skipSpace()
		if p.consumeByte('}') {
			return out, nil
		}
		if !p.consumeByte(',') {
			return nil, errors.New("extension object entries must be separated by ','")
		}
		p.skipSpace()
	}
}

func (p *jsonParser) parseList() ([]Value, error) {
	p.skipSpace()
	if !p.consumeByte('[') {
		return nil, errors.New("extension list must be a json array")
	}
	out := []Value{}
	p.skipSpace()
	if p.consumeByte(']') {
		return out, nil
	}
	for {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
		p.skipSpace()
		if p.consumeByte(']') {
			return out, nil
		}
		if !p.consumeByte(',') {
			return nil, errors.New("extension list entries must be separated by ','")
		}
		p.skipSpace()
	}
}

func (p *jsonParser) parseString() (string, error) {
	p.skipSpace()
	if p.pos >= len(p.data) || p.data[p.pos] != '"' {
		return "", errors.New("extension JSON string must start with quote")
	}
	p.pos++ // consume '"'
	start := p.pos
	escaped := false
	for p.pos < len(p.data) {
		ch := p.data[p.pos]
		if ch == '\\' {
			escaped = true
			p.pos += 2 // skip escape character and the next character
			continue
		}
		if ch == '"' {
			strData := p.data[start:p.pos]
			p.pos++ // consume '"'
			if escaped {
				return strconv.Unquote(string(p.data[start-1 : p.pos]))
			}
			return string(strData), nil
		}
		if ch < 0x20 {
			return "", errors.New("extension JSON string contains control character")
		}
		p.pos++
	}
	return "", io.ErrUnexpectedEOF
}

func (p *jsonParser) parseNumber() (string, error) {
	start := p.pos
	if p.pos < len(p.data) && p.data[p.pos] == '-' {
		p.pos++
	}
	if p.pos >= len(p.data) {
		return "", io.ErrUnexpectedEOF
	}
	if p.data[p.pos] == '0' {
		p.pos++
	} else if isDigitOneToNine(p.data[p.pos]) {
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
	} else {
		return "", errors.New("extension JSON number has invalid integer part")
	}
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		fracStart := p.pos
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
		if p.pos == fracStart {
			return "", errors.New("extension JSON number has invalid fraction")
		}
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		expStart := p.pos
		for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
			p.pos++
		}
		if p.pos == expStart {
			return "", errors.New("extension JSON number has invalid exponent")
		}
	}
	return string(p.data[start:p.pos]), nil
}

func (p *jsonParser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\n', '\r', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) consumeByte(ch byte) bool {
	if p.pos >= len(p.data) || p.data[p.pos] != ch {
		return false
	}
	p.pos++
	return true
}

func (p *jsonParser) consumeLiteral(literal string) bool {
	if len(p.data)-p.pos < len(literal) || string(p.data[p.pos:p.pos+len(literal)]) != literal {
		return false
	}
	p.pos += len(literal)
	return true
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isDigitOneToNine(ch byte) bool {
	return ch >= '1' && ch <= '9'
}

func valueFromJSONNumber(number json.Number) (Value, error) {
	if i, err := number.Int64(); err == nil {
		return Int(i), nil
	}
	f, err := number.Float64()
	if err != nil {
		return Null(), err
	}
	return Float(f), nil
}

func fromJSONAnySlice(typed []any) (Value, error) {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		value, err := FromJSON(item)
		if err != nil {
			return Null(), err
		}
		out = append(out, value)
	}
	return listValueOwned(out), nil
}

func fromJSONStringSlice(typed []string) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, String(item))
	}
	return listValueOwned(out)
}

func fromJSONIntSlice(typed []int) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, Int(int64(item)))
	}
	return listValueOwned(out)
}

func fromJSONInt64Slice(typed []int64) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, Int(item))
	}
	return listValueOwned(out)
}

func fromJSONUint64Slice(typed []uint64) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, Uint(item))
	}
	return listValueOwned(out)
}

func fromJSONFloat64Slice(typed []float64) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, Float(item))
	}
	return listValueOwned(out)
}

func fromJSONBoolSlice(typed []bool) Value {
	out := make([]Value, 0, len(typed))
	for _, item := range typed {
		out = append(out, Bool(item))
	}
	return listValueOwned(out)
}

func fromJSONAnyMap(typed map[string]any) (Value, error) {
	out := make(Object, len(typed))
	for key, item := range typed {
		value, err := FromJSON(item)
		if err != nil {
			return Null(), err
		}
		out[key] = value
	}
	return objectValueOwned(out), nil
}

func fromJSONStringMap(typed map[string]string) Value {
	out := make(Object, len(typed))
	for key, item := range typed {
		out[key] = String(item)
	}
	return objectValueOwned(out)
}

func fromJSONValueMap(typed map[string]Value) Value {
	out := make(Object, len(typed))
	for key, item := range typed {
		out[key] = item.Clone()
	}
	return ObjectValue(out)
}

func FromJSON(raw any) (Value, error) {
	switch typed := raw.(type) {
	case nil:
		return Null(), nil
	case Value:
		return typed.Clone(), nil
	case Object:
		return ObjectValue(typed), nil
	case time.Time:
		if typed.IsZero() {
			return String(""), nil
		}
		return String(typed.UTC().Format(time.RFC3339Nano)), nil
	case string:
		return String(typed), nil
	case bool:
		return Bool(typed), nil
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return Int(i), nil
		}
		f, err := typed.Float64()
		if err != nil {
			return Null(), err
		}
		return Float(f), nil
	case float64:
		return Float(typed), nil
	case float32:
		return Float(float64(typed)), nil
	case int:
		return Int(int64(typed)), nil
	case int8:
		return Int(int64(typed)), nil
	case int16:
		return Int(int64(typed)), nil
	case int32:
		return Int(int64(typed)), nil
	case int64:
		return Int(typed), nil
	case uint:
		return Uint(uint64(typed)), nil
	case uint8:
		return Uint(uint64(typed)), nil
	case uint16:
		return Uint(uint64(typed)), nil
	case uint32:
		return Uint(uint64(typed)), nil
	case uint64:
		return Uint(typed), nil
	case []byte:
		return Bytes(typed), nil
	case []Value:
		return List(typed), nil
	case []any:
		return fromJSONAnySlice(typed)
	case []string:
		return fromJSONStringSlice(typed), nil
	case []int:
		return fromJSONIntSlice(typed), nil
	case []int64:
		return fromJSONInt64Slice(typed), nil
	case []uint64:
		return fromJSONUint64Slice(typed), nil
	case []float64:
		return fromJSONFloat64Slice(typed), nil
	case []bool:
		return fromJSONBoolSlice(typed), nil
	case map[string]any:
		return fromJSONAnyMap(typed)
	case map[string]string:
		return fromJSONStringMap(typed), nil
	case map[string]Value:
		return fromJSONValueMap(typed), nil
	default:
		return valueFromReflect(reflect.ValueOf(raw))
	}
}

func valueFromReflect(raw reflect.Value) (Value, error) {
	if !raw.IsValid() {
		return Null(), nil
	}
	if raw.Type() == reflect.TypeFor[time.Time]() {
		t := raw.Interface().(time.Time)
		if t.IsZero() {
			return String(""), nil
		}
		return String(t.UTC().Format(time.RFC3339Nano)), nil
	}
	switch raw.Kind() {
	case reflect.Pointer, reflect.Interface:
		if raw.IsNil() {
			return Null(), nil
		}
		return valueFromReflect(raw.Elem())
	case reflect.String:
		return String(raw.String()), nil
	case reflect.Bool:
		return Bool(raw.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Int(raw.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return Uint(raw.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return Float(raw.Convert(reflect.TypeFor[float64]()).Float()), nil
	case reflect.Slice:
		if raw.Type().Elem().Kind() == reflect.Uint8 {
			return Bytes(raw.Bytes()), nil
		}
		fallthrough
	case reflect.Array:
		out := make([]Value, 0, raw.Len())
		for i := 0; i < raw.Len(); i++ {
			value, err := valueFromReflect(raw.Index(i))
			if err != nil {
				return Null(), err
			}
			out = append(out, value)
		}
		return listValueOwned(out), nil
	case reflect.Map:
		if raw.Type().Key().Kind() != reflect.String {
			return Null(), fmt.Errorf("unsupported extension map key type %s", raw.Type().Key())
		}
		out := make(Object, raw.Len())
		for _, key := range raw.MapKeys() {
			value, err := valueFromReflect(raw.MapIndex(key))
			if err != nil {
				return Null(), err
			}
			out[key.String()] = value
		}
		return objectValueOwned(out), nil
	case reflect.Struct:
		t := raw.Type()
		out := make(Object, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Name
			tag := field.Tag.Get("json")
			if tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
			}
			value, err := valueFromReflect(raw.Field(i))
			if err != nil {
				return Null(), err
			}
			out[name] = value
		}
		return objectValueOwned(out), nil
	default:
		return Null(), fmt.Errorf("unsupported extension JSON value %s", raw.Type())
	}
}

func (o Object) Keys() []string {
	keys := make([]string, 0, len(o))
	for key := range o {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (o Object) GetString(key string) (string, bool) {
	value, ok := o[key]
	if !ok {
		return "", false
	}
	return value.StringValue()
}

func (o Object) GetBool(key string) (bool, bool) {
	value, ok := o[key]
	if !ok {
		return false, false
	}
	return value.BoolValue()
}

func (o Object) GetInt(key string) (int64, bool) {
	value, ok := o[key]
	if !ok {
		return 0, false
	}
	return value.IntValue()
}

func (o Object) GetUint(key string) (uint64, bool) {
	value, ok := o[key]
	if !ok {
		return 0, false
	}
	return value.UintValue()
}

func (o Object) GetFloat(key string) (float64, bool) {
	value, ok := o[key]
	if !ok {
		return 0, false
	}
	return value.FloatValue()
}

func (o Object) GetBytes(key string) ([]byte, bool) {
	value, ok := o[key]
	if !ok {
		return nil, false
	}
	return value.BytesValue()
}

func (o Object) GetObject(key string) (Object, bool) {
	value, ok := o[key]
	if !ok {
		return nil, false
	}
	return value.ObjectValue()
}

func (o Object) GetObjectView(key string) (Object, bool) {
	value, ok := o[key]
	if !ok {
		return nil, false
	}
	return value.ObjectView()
}

func (o Object) GetList(key string) ([]Value, bool) {
	value, ok := o[key]
	if !ok {
		return nil, false
	}
	return value.ListValue()
}

func (o Object) GetInterfaceMap(key string) (map[string]any, bool) {
	value, ok := o.GetObject(key)
	if !ok {
		return nil, false
	}
	return value.InterfaceMap(), true
}

func (o Object) GetStringList(key string) ([]string, bool) {
	values, ok := o.GetList(key)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.StringValue()
		if !ok {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

func (o Object) Has(key string) bool {
	_, ok := o[key]
	return ok
}

func (o Object) RequireString(key string) (string, error) {
	value, ok := o.GetString(key)
	if !ok {
		return "", fmt.Errorf("extension field %q must be a string", key)
	}
	return value, nil
}

func (o Object) ValidateKeys(allowed map[string]struct{}) error {
	if allowed == nil {
		return errors.New("allowed keys are required")
	}
	for key := range o {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("extension field %q is not allowed", key)
		}
	}
	return nil
}
