package extension

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestValueRoundTripPreservesIntegerShape(t *testing.T) {
	raw := []byte(`{"amount_minor":1200,"symbol":"OVS","active":true}`)
	var value Value
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal extension value: %v", err)
	}

	obj, ok := value.ObjectValue()
	if !ok {
		t.Fatalf("expected object value")
	}
	amount, ok := obj["amount_minor"].IntValue()
	if !ok || amount != 1200 {
		t.Fatalf("amount_minor = %v, %v; want 1200, true", amount, ok)
	}
	symbol, ok := obj.GetString("symbol")
	if !ok || symbol != "OVS" {
		t.Fatalf("symbol = %q, %v; want OVS, true", symbol, ok)
	}
}

func TestObjectFromJSONDecodesNestedValuesWithoutAnyStaging(t *testing.T) {
	obj, err := ObjectFromJSON([]byte(`{"amount":1200,"ratio":1.25,"active":true,"tags":["a","b"],"nested":{"id":"n1"},"empty":null}`))
	if err != nil {
		t.Fatalf("ObjectFromJSON() error = %v", err)
	}
	if amount, ok := obj["amount"].IntValue(); !ok || amount != 1200 {
		t.Fatalf("amount = %d, %v; want 1200, true", amount, ok)
	}
	if ratio, ok := obj["ratio"].FloatValue(); !ok || ratio != 1.25 {
		t.Fatalf("ratio = %v, %v; want 1.25, true", ratio, ok)
	}
	if active, ok := obj["active"].BoolValue(); !ok || !active {
		t.Fatalf("active = %v, %v; want true, true", active, ok)
	}
	tags, ok := obj["tags"].ListValue()
	if !ok || len(tags) != 2 {
		t.Fatalf("tags len = %d, %v; want 2, true", len(tags), ok)
	}
	nested, ok := obj["nested"].ObjectValue()
	if !ok {
		t.Fatalf("expected nested object")
	}
	if id, ok := nested.GetString("id"); !ok || id != "n1" {
		t.Fatalf("nested.id = %q, %v; want n1, true", id, ok)
	}
	if obj["empty"].Kind() != KindNull {
		t.Fatalf("empty kind = %v; want null", obj["empty"].Kind())
	}
}

func TestObjectFromMapPreservesApplicationPayloadShapes(t *testing.T) {
	priority := int64(7)
	when := time.Date(2026, 5, 28, 21, 31, 28, 123, time.FixedZone("WAT", 3600))
	obj := ObjectFromMap(map[string]any{
		"metadata": map[string]any{
			"scope":    "docs",
			"priority": &priority,
		},
		"items": []map[string]any{
			{"id": "first", "active": true},
			{"id": "second", "score": float32(0.75)},
		},
		"created_at": when,
		"nil_ptr":    (*string)(nil),
		"blob":       []byte("ovasabi"),
	})

	metadata, ok := obj.GetObject("metadata")
	if !ok {
		t.Fatalf("expected metadata object, got %v", obj["metadata"].Kind())
	}
	if scope, ok := metadata.GetString("scope"); !ok || scope != "docs" {
		t.Fatalf("metadata.scope = %q, %v; want docs, true", scope, ok)
	}
	if got, ok := metadata.GetInt("priority"); !ok || got != 7 {
		t.Fatalf("metadata.priority = %d, %v; want 7, true", got, ok)
	}

	items, ok := obj.GetList("items")
	if !ok || len(items) != 2 {
		t.Fatalf("items len = %d, %v; want 2, true", len(items), ok)
	}
	first, ok := items[0].ObjectValue()
	if !ok {
		t.Fatalf("expected first item object")
	}
	if id, ok := first.GetString("id"); !ok || id != "first" {
		t.Fatalf("items[0].id = %q, %v; want first, true", id, ok)
	}

	if created, ok := obj.GetString("created_at"); !ok || created != "2026-05-28T20:31:28.000000123Z" {
		t.Fatalf("created_at = %q, %v; want UTC RFC3339Nano timestamp, true", created, ok)
	}
	if obj["nil_ptr"].Kind() != KindNull {
		t.Fatalf("nil_ptr kind = %v; want null", obj["nil_ptr"].Kind())
	}
	if blob, ok := obj.GetBytes("blob"); !ok || string(blob) != "ovasabi" {
		t.Fatalf("blob = %q, %v; want ovasabi, true", string(blob), ok)
	}
}

func TestInterfaceMapPreservesObjectListsAsMapSlices(t *testing.T) {
	obj := ObjectFromMap(map[string]any{
		"items": []map[string]any{
			{"id": "one"},
			{"id": "two"},
		},
	})

	items, ok := obj.InterfaceMap()["items"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %T len %d; want []map[string]any len 2", obj.InterfaceMap()["items"], len(items))
	}
	if items[0]["id"] != "one" || items[1]["id"] != "two" {
		t.Fatalf("items = %#v; want ids one/two", items)
	}
}

func TestObjectFromJSONRejectsNonObjectAndTrailingData(t *testing.T) {
	if _, err := ObjectFromJSON([]byte(`[1,2]`)); err == nil {
		t.Fatalf("expected non-object rejection")
	}
	if _, err := ObjectFromJSON([]byte(`{"ok":true} false`)); err == nil {
		t.Fatalf("expected trailing data rejection")
	}
}

func TestObjectCloneDoesNotShareNestedState(t *testing.T) {
	original := Object{
		"nested": ObjectValue(Object{"k": String("v")}),
	}
	clone := original.Clone()
	nested, ok := clone["nested"].ObjectValue()
	if !ok {
		t.Fatalf("expected nested object")
	}
	nested["k"] = String("changed")

	originalNested, ok := original["nested"].ObjectValue()
	if !ok {
		t.Fatalf("expected original nested object")
	}
	got, _ := originalNested.GetString("k")
	if got != "v" {
		t.Fatalf("original nested value changed to %q", got)
	}
}

func TestObjectValidateKeysRejectsUnknownExtensionField(t *testing.T) {
	obj := Object{"known": String("ok"), "unknown": Bool(true)}
	err := obj.ValidateKeys(map[string]struct{}{"known": {}})
	if err == nil {
		t.Fatalf("expected unknown field rejection")
	}
}
func TestMarshalPropagatesNestedInvalidKindErrors(t *testing.T) {
	bad := Value{kind: Kind(200)}
	if _, err := List([]Value{bad}).MarshalJSON(); err == nil {
		t.Fatal("list marshal should propagate element error")
	}
	if _, err := ObjectValue(Object{"k": bad}).MarshalJSON(); err == nil {
		t.Fatal("object marshal should propagate value error")
	}
	if _, err := (Object{"k": bad}).MarshalJSONFast(); err == nil {
		t.Fatal("fast object marshal should propagate value error")
	}
	if _, err := List([]Value{bad}).appendJSONFast(nil); err == nil {
		t.Fatal("fast list marshal should propagate element error")
	}
}

func TestParserRejectsErrorsInsideContainers(t *testing.T) {
	if _, err := ObjectFromJSON([]byte(`{"a":[}`)); err == nil {
		t.Fatal("invalid value inside list should error")
	}
	var v Value
	if err := v.UnmarshalJSON([]byte("@")); err == nil {
		t.Fatal("invalid leading byte should error")
	}
}

func TestParserAcceptsEmptyListAndExponentNumbers(t *testing.T) {
	o, err := ObjectFromJSON([]byte(`{"empty":[], "exp":1e3, "negfrac":-0.5}`))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if list, ok := o.GetList("empty"); !ok || len(list) != 0 {
		t.Fatalf("empty list = (%v,%v)", list, ok)
	}
	if o["exp"].Kind() != KindFloat {
		t.Fatalf("exponent kind = %d want KindFloat", o["exp"].Kind())
	}
	if f, ok := o.GetFloat("exp"); !ok || f != 1000 {
		t.Fatalf("exp = (%v,%v) want 1000", f, ok)
	}
	if f, ok := o.GetFloat("negfrac"); !ok || f != -0.5 {
		t.Fatalf("negfrac = (%v,%v)", f, ok)
	}
}

func TestObjectGettersReturnFalseForMissingKeys(t *testing.T) {
	o := Object{}
	if _, ok := o.GetUint("x"); ok {
		t.Fatal("GetUint missing")
	}
	if _, ok := o.GetBytes("x"); ok {
		t.Fatal("GetBytes missing")
	}
	if _, ok := o.GetObject("x"); ok {
		t.Fatal("GetObject missing")
	}
	if _, ok := o.GetList("x"); ok {
		t.Fatal("GetList missing")
	}
}

func TestEmptyObjectCloneAndInterfaceMap(t *testing.T) {
	if got := (Object{}).Clone(); len(got) != 0 {
		t.Fatalf("empty Clone = %v", got)
	}
	if got := (Object{}).InterfaceMap(); len(got) != 0 {
		t.Fatalf("empty InterfaceMap = %v", got)
	}
}

func TestObjectValueOwnedNormalisesNilToEmptyObject(t *testing.T) {
	v := objectValueOwned(nil)
	if v.Kind() != KindObject {
		t.Fatalf("kind = %d want KindObject", v.Kind())
	}
	got, ok := v.ObjectView()
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("ObjectView = (%v,%v) want (empty,true)", got, ok)
	}
}

func TestReflectionHandlesNilPointerFloatAndNamedSlice(t *testing.T) {
	var p *int
	if v, _ := FromJSON(p); v.Kind() != KindNull {
		t.Fatalf("nil pointer -> %d want KindNull", v.Kind())
	}
	v, _ := FromJSON(map[string]float64{"k": 1.5})
	obj, _ := v.ObjectView()
	if f, ok := obj.GetFloat("k"); !ok || f != 1.5 {
		t.Fatalf("reflected float = (%v,%v)", f, ok)
	}
	type ints []int
	if lv, _ := FromJSON(ints{1, 2}); lv.Kind() != KindList {
		t.Fatalf("named int slice -> %d want KindList", lv.Kind())
	}
}
func TestFromJSONConvertsScalarAndTimeTypes(t *testing.T) {
	if v, err := FromJSON(nil); err != nil || v.Kind() != KindNull {
		t.Fatalf("nil -> (%d,%v)", v.Kind(), err)
	}

	if v, _ := FromJSON(String("x")); func() bool { s, ok := v.StringValue(); return ok && s == "x" }() == false {
		t.Fatal("Value passthrough lost payload")
	}
	if v, _ := FromJSON(Object{"k": Int(1)}); v.Kind() != KindObject {
		t.Fatalf("Object -> %d want KindObject", v.Kind())
	}
	if v, _ := FromJSON(time.Time{}); func() bool { s, ok := v.StringValue(); return ok && s == "" }() == false {
		t.Fatal("zero time should map to empty string")
	}
	tm := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	if v, _ := FromJSON(tm); func() bool { s, _ := v.StringValue(); return s == tm.UTC().Format(time.RFC3339Nano) }() == false {
		t.Fatal("non-zero time should map to RFC3339Nano")
	}
	if v, _ := FromJSON("s"); v.Kind() != KindString {
		t.Fatalf("string -> %d", v.Kind())
	}
	if v, _ := FromJSON(true); v.Kind() != KindBool {
		t.Fatalf("bool -> %d", v.Kind())
	}
	if v, _ := FromJSON(json.Number("7")); func() bool { i, ok := v.IntValue(); return ok && i == 7 }() == false {
		t.Fatal("json.Number integer -> int")
	}
	if v, _ := FromJSON(json.Number("7.5")); func() bool { f, ok := v.FloatValue(); return ok && f == 7.5 }() == false {
		t.Fatal("json.Number float -> float")
	}
	if _, err := FromJSON(json.Number("not-a-number")); err == nil {
		t.Fatal("malformed json.Number should error")
	}
}

func TestFromJSONConvertsNumericWidths(t *testing.T) {
	ints := []any{float64(1), float32(1), int(1), int8(1), int16(1), int32(1), int64(1)}
	for _, in := range ints {
		v, err := FromJSON(in)
		if err != nil {
			t.Fatalf("%T -> error %v", in, err)
		}
		switch in.(type) {
		case float64, float32:
			if v.Kind() != KindFloat {
				t.Fatalf("%T -> %d want KindFloat", in, v.Kind())
			}
		default:
			if v.Kind() != KindInt {
				t.Fatalf("%T -> %d want KindInt", in, v.Kind())
			}
		}
	}
	uints := []any{uint(2), uint8(2), uint16(2), uint32(2), uint64(2)}
	for _, in := range uints {
		v, _ := FromJSON(in)
		if v.Kind() != KindUint {
			t.Fatalf("%T -> %d want KindUint", in, v.Kind())
		}
	}
}

func TestFromJSONConvertsSliceAndMapTypes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		kind Kind
	}{
		{"[]byte", []byte("hi"), KindBytes},
		{"[]Value", []Value{Int(1)}, KindList},
		{"[]any", []any{1, "x"}, KindList},
		{"[]string", []string{"a"}, KindList},
		{"[]int", []int{1, 2}, KindList},
		{"[]int64", []int64{1}, KindList},
		{"[]uint64", []uint64{1}, KindList},
		{"[]float64", []float64{1.5}, KindList},
		{"[]bool", []bool{true}, KindList},
		{"map[string]any", map[string]any{"k": 1}, KindObject},
		{"map[string]string", map[string]string{"k": "v"}, KindObject},
		{"map[string]Value", map[string]Value{"k": Int(1)}, KindObject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := FromJSON(tc.in)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if v.Kind() != tc.kind {
				t.Fatalf("kind = %d want %d", v.Kind(), tc.kind)
			}
		})
	}

	if _, err := FromJSON([]any{make(chan int)}); err == nil {
		t.Fatal("unconvertible slice element should error")
	}
	if _, err := FromJSON(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("unconvertible map value should error")
	}
}

type convStruct struct {
	Named   string `json:"renamed"`
	Skipped int    `json:"-"`
	Plain   bool
	hidden  int //nolint:unused // exercises the unexported-field skip branch
}

func TestFromJSONReflectionFallback(t *testing.T) {

	n := 5
	if v, _ := FromJSON(&n); func() bool { i, ok := v.IntValue(); return ok && i == 5 }() == false {
		t.Fatal("pointer not dereferenced to int")
	}

	if v, _ := FromJSON(map[string]int{"k": 1}); v.Kind() != KindObject {
		t.Fatalf("map[string]int -> %d want KindObject", v.Kind())
	}

	s := convStruct{Named: "n", Skipped: 9, Plain: true, hidden: 1}
	v, err := FromJSON(s)
	if err != nil {
		t.Fatalf("struct error: %v", err)
	}
	obj, ok := v.ObjectView()
	if !ok {
		t.Fatal("struct did not become object")
	}
	if got, ok := obj.GetString("renamed"); !ok || got != "n" {
		t.Fatalf("renamed = (%q,%v)", got, ok)
	}
	if obj.Has("Skipped") {
		t.Fatal(`json:"-" field should be skipped`)
	}
	if !obj.Has("Plain") {
		t.Fatal("exported field should be present")
	}
	if obj.Has("hidden") {
		t.Fatal("unexported field should be dropped")
	}

	if v, _ := FromJSON([2]int{1, 2}); v.Kind() != KindList {
		t.Fatalf("array -> %d want KindList", v.Kind())
	}
	// Named byte slice maps to bytes via reflection.
	type byteSlice []byte
	if v, _ := FromJSON(byteSlice("hi")); v.Kind() != KindBytes {
		t.Fatalf("named byte slice -> %d want KindBytes", v.Kind())
	}

	if _, err := FromJSON(make(chan int)); err == nil {
		t.Fatal("channel should error")
	}

	if _, err := FromJSON(map[int]int{1: 1}); err == nil {
		t.Fatal("non-string map key should error")
	}
}

func TestValueCloneCopiesBytesAndListIndependently(t *testing.T) {
	b := Bytes([]byte{1, 2})
	cb := b.Clone()
	if raw, _ := cb.BytesValue(); len(raw) != 2 {
		t.Fatalf("bytes clone lost data: %v", raw)
	}
	l := List([]Value{Int(1)})
	cl := l.Clone()
	got, _ := cl.ListValue()
	if len(got) != 1 {
		t.Fatalf("list clone lost data: %v", got)
	}
	if first, _ := got[0].IntValue(); first != 1 {
		t.Fatalf("list clone element = %d", first)
	}
}

func TestValueInterfaceCoversScalarKinds(t *testing.T) {
	if String("x").Interface() != "x" {
		t.Fatal("string Interface")
	}
	if Bool(true).Interface() != true {
		t.Fatal("bool Interface")
	}
	if Int(3).Interface() != int64(3) {
		t.Fatal("int Interface")
	}
	if Uint(4).Interface() != uint64(4) {
		t.Fatal("uint Interface")
	}
	if Float(1.5).Interface() != 1.5 {
		t.Fatal("float Interface")
	}

	if _, ok := List(nil).Interface().([]any); !ok {
		t.Fatalf("empty list Interface type = %T want []any", List(nil).Interface())
	}
}

func TestObjectGettersRejectWrongTypes(t *testing.T) {
	o := Object{"s": String("x")}
	if _, ok := o.GetInt("s"); ok {
		t.Fatal("GetInt on string returned ok")
	}
	if _, ok := o.GetBytes("s"); ok {
		t.Fatal("GetBytes on string returned ok")
	}
	if _, ok := o.GetObject("s"); ok {
		t.Fatal("GetObject on string returned ok")
	}
	if _, ok := o.GetList("s"); ok {
		t.Fatal("GetList on string returned ok")
	}
	if _, ok := o.GetInt("missing"); ok {
		t.Fatal("GetInt on missing returned ok")
	}
}

func TestValidateKeysRequiresAllowSetAndAcceptsKnownKeys(t *testing.T) {
	if err := (Object{"k": Int(1)}).ValidateKeys(nil); err == nil {
		t.Fatal("nil allow set should error")
	}
	if err := (Object{"k": Int(1)}).ValidateKeys(map[string]struct{}{"k": {}}); err != nil {
		t.Fatalf("known key rejected: %v", err)
	}
}

func TestValueUnmarshalJSONRejectsTrailingData(t *testing.T) {
	var v Value
	if err := v.UnmarshalJSON([]byte("1 2")); err == nil {
		t.Fatal("trailing data after scalar should error")
	}
}
func TestScalarConstructorsSetKindAndGateTypedAccessors(t *testing.T) {
	if got := Null().Kind(); got != KindNull {
		t.Fatalf("Null().Kind()=%d want KindNull", got)
	}
	if s, ok := String("hello").StringValue(); !ok || s != "hello" {
		t.Fatalf("String accessor = (%q,%v) want (hello,true)", s, ok)
	}
	if s, ok := String("").StringValue(); !ok || s != "" {
		t.Fatalf("empty String accessor = (%q,%v) want (\"\",true)", s, ok)
	}
	if b, ok := Bool(true).BoolValue(); !ok || !b {
		t.Fatalf("Bool accessor = (%v,%v) want (true,true)", b, ok)
	}

	if i, ok := Int(math.MinInt64).IntValue(); !ok || i != math.MinInt64 {
		t.Fatalf("Int(min) accessor = (%d,%v)", i, ok)
	}
	if i, ok := Int(math.MaxInt64).IntValue(); !ok || i != math.MaxInt64 {
		t.Fatalf("Int(max) accessor = (%d,%v)", i, ok)
	}
	if u, ok := Uint(math.MaxUint64).UintValue(); !ok || u != math.MaxUint64 {
		t.Fatalf("Uint(max) accessor = (%d,%v)", u, ok)
	}
	if f, ok := Float(-3.5).FloatValue(); !ok || f != -3.5 {
		t.Fatalf("Float accessor = (%v,%v)", f, ok)
	}
}

func TestTypedAccessorsRejectMismatchedKinds(t *testing.T) {
	s := String("x")
	if _, ok := s.IntValue(); ok {
		t.Fatal("StringValue returned int ok")
	}
	if _, ok := s.BoolValue(); ok {
		t.Fatal("StringValue returned bool ok")
	}
	if _, ok := s.BytesValue(); ok {
		t.Fatal("StringValue returned bytes ok")
	}
	if _, ok := s.ListValue(); ok {
		t.Fatal("StringValue returned list ok")
	}
	if _, ok := s.ObjectValue(); ok {
		t.Fatal("StringValue returned object ok")
	}
	if _, ok := s.ObjectView(); ok {
		t.Fatal("StringValue returned object view ok")
	}
	if _, ok := Float(1).IntValue(); ok {
		t.Fatal("FloatValue returned int ok")
	}
}

func TestBytesConstructorAndAccessorDoNotAliasCallerMemory(t *testing.T) {
	src := []byte{1, 2, 3}
	v := Bytes(src)
	src[0] = 9
	got, ok := v.BytesValue()
	if !ok || !reflect.DeepEqual(got, []byte{1, 2, 3}) {
		t.Fatalf("constructor aliased caller memory: got %v ok=%v", got, ok)
	}
	got[1] = 8
	again, _ := v.BytesValue()
	if !reflect.DeepEqual(again, []byte{1, 2, 3}) {
		t.Fatalf("accessor returned aliased slice: %v", again)
	}
	if bs, ok := Bytes(nil).BytesValue(); !ok || len(bs) != 0 {
		t.Fatalf("Bytes(nil) accessor = (%v,%v) want (empty,true)", bs, ok)
	}
}

func TestListConstructorCopiesAndAccessorReturnsCopy(t *testing.T) {
	src := []Value{Int(1), Int(2)}
	v := List(src)
	src[0] = Int(99)
	got, ok := v.ListValue()
	if !ok || len(got) != 2 {
		t.Fatalf("ListValue = (%v,%v)", got, ok)
	}
	if first, _ := got[0].IntValue(); first != 1 {
		t.Fatalf("constructor aliased caller list: first=%d", first)
	}
	got[0] = Int(7)
	again, _ := v.ListValue()
	if first, _ := again[0].IntValue(); first != 1 {
		t.Fatalf("accessor returned aliased list: first=%d", first)
	}
}

func TestObjectValueClonesWhileObjectViewShares(t *testing.T) {
	src := Object{"k": Int(1)}
	v := ObjectValue(src)
	src["k"] = Int(2)
	got, ok := v.ObjectValue()
	if !ok {
		t.Fatal("ObjectValue accessor not ok")
	}
	if iv, _ := got["k"].IntValue(); iv != 1 {
		t.Fatalf("constructor aliased caller object: k=%d", iv)
	}

	view, ok := v.ObjectView()
	if !ok {
		t.Fatal("ObjectView not ok")
	}
	view["k"] = Int(7)
	after, _ := v.ObjectValue()
	if iv, _ := after["k"].IntValue(); iv != 7 {
		t.Fatalf("ObjectView did not share internal object: k=%d", iv)
	}
}

func TestObjectViewOnNilObjectReturnsEmptyNonNil(t *testing.T) {
	v := Value{kind: KindObject}
	got, ok := v.ObjectView()
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("ObjectView(nil) = (%v,%v) want (empty,true)", got, ok)
	}
}

func TestValueCloneDeepCopiesContainersAndPassesScalars(t *testing.T) {
	orig := ObjectValue(Object{"k": Int(1)})
	clone := orig.Clone()
	view, _ := clone.ObjectView()
	view["k"] = Int(42)
	got, _ := orig.ObjectValue()
	if iv, _ := got["k"].IntValue(); iv != 1 {
		t.Fatalf("Clone shared nested object state: k=%d", iv)
	}
	scalar := Int(5).Clone()
	if scalar.Kind() != KindInt {
		t.Fatalf("scalar Clone kind=%d want KindInt", scalar.Kind())
	}
	if iv, _ := scalar.IntValue(); iv != 5 {
		t.Fatalf("scalar Clone value=%d want 5", iv)
	}
}

func TestValueMarshalJSONEmitsCanonicalScalarEncoding(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"null", Null(), "null"},
		{"string with quote", String(`a"b`), `"a\"b"`},
		{"bool true", Bool(true), "true"},
		{"bool false", Bool(false), "false"},
		{"negative int", Int(-7), "-7"},
		{"uint", Uint(42), "42"},
		{"float", Float(1.5), "1.5"},
		{"bytes as base64", Bytes([]byte("hi")), `"aGk="`},
		{"empty list", List(nil), "[]"},
		{"scalar list", List([]Value{Int(1), String("x")}), `[1,"x"]`},
		{"object", ObjectValue(Object{"k": Bool(false)}), `{"k":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.v.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("MarshalJSON = %s want %s", got, tc.want)
			}
		})
	}
}

func TestValueMarshalJSONRejectsUnknownKind(t *testing.T) {
	v := Value{kind: Kind(200)}
	if _, err := v.MarshalJSON(); err == nil {
		t.Fatal("expected error marshalling unknown kind")
	}
}

func TestObjectMarshalJSONSortsKeysAndEmptyIsBraces(t *testing.T) {
	o := Object{"b": Int(2), "a": Int(1), "c": Null()}
	got, err := o.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if want := `{"a":1,"b":2,"c":null}`; string(got) != want {
		t.Fatalf("MarshalJSON = %s want %s", got, want)
	}
	if empty, _ := (Object{}).MarshalJSON(); string(empty) != "{}" {
		t.Fatalf("empty Object MarshalJSON = %s want {}", empty)
	}
}

func TestMarshalJSONFastProducesParseableEquivalentJSON(t *testing.T) {
	o := Object{
		"a":      Int(1),
		"nested": ObjectValue(Object{"n": Bool(true)}),
		"list":   List([]Value{String("x"), Int(2)}),
	}
	fast, err := o.MarshalJSONFast()
	if err != nil {
		t.Fatalf("MarshalJSONFast error: %v", err)
	}

	back, err := ObjectFromJSON(fast)
	if err != nil {
		t.Fatalf("re-parsing fast JSON failed: %v\njson=%s", err, fast)
	}
	if !reflect.DeepEqual(back, o) {
		t.Fatalf("fast round trip mismatch:\n got=%#v\nwant=%#v", back, o)
	}
	if empty, _ := (Object{}).MarshalJSONFast(); string(empty) != "{}" {
		t.Fatalf("empty fast = %s want {}", empty)
	}
}

func TestObjectFromJSONParsesAllScalarKindsAndNesting(t *testing.T) {

	src := []byte(`  { "s":"v" , "b":true, "n":null, "i":12, "f":1.25, "list":[1,2,3], "obj":{"x":-4} }  `)
	o, err := ObjectFromJSON(src)
	if err != nil {
		t.Fatalf("ObjectFromJSON error: %v", err)
	}
	if s, ok := o.GetString("s"); !ok || s != "v" {
		t.Fatalf("s = (%q,%v)", s, ok)
	}
	if b, ok := o.GetBool("b"); !ok || !b {
		t.Fatalf("b = (%v,%v)", b, ok)
	}
	if o["n"].Kind() != KindNull {
		t.Fatalf("n kind = %d want KindNull", o["n"].Kind())
	}
	if i, ok := o.GetInt("i"); !ok || i != 12 {
		t.Fatalf("i = (%d,%v)", i, ok)
	}
	if f, ok := o.GetFloat("f"); !ok || f != 1.25 {
		t.Fatalf("f = (%v,%v)", f, ok)
	}
	list, ok := o.GetList("list")
	if !ok || len(list) != 3 {
		t.Fatalf("list = (%v,%v)", list, ok)
	}
	if first, _ := list[0].IntValue(); first != 1 {
		t.Fatalf("list[0] = %d want 1", first)
	}
	obj, ok := o.GetObject("obj")
	if !ok {
		t.Fatal("obj missing")
	}
	if x, ok := obj.GetInt("x"); !ok || x != -4 {
		t.Fatalf("obj.x = (%d,%v)", x, ok)
	}
}

func TestObjectFromJSONNullOrBlankYieldsEmptyObject(t *testing.T) {
	for _, in := range []string{"null", "   ", "", "\t\n"} {
		o, err := ObjectFromJSON([]byte(in))
		if err != nil {
			t.Fatalf("ObjectFromJSON(%q) error: %v", in, err)
		}
		if len(o) != 0 {
			t.Fatalf("ObjectFromJSON(%q) = %v want empty", in, o)
		}
	}
}

func TestObjectFromJSONLargeIntegerBecomesFloat(t *testing.T) {

	o, err := ObjectFromJSON([]byte(`{"big": 99999999999999999999}`))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if k := o["big"].Kind(); k != KindFloat {
		t.Fatalf("big kind = %d want KindFloat", k)
	}
}

func TestObjectFromJSONDecodesEscapedStrings(t *testing.T) {
	o, err := ObjectFromJSON([]byte(`{"k":"lineA\t\"q\""}`))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	got, _ := o.GetString("k")
	if want := "lineA\t\"q\""; got != want {
		t.Fatalf("escaped string = %q want %q", got, want)
	}
}

func TestValueUnmarshalJSONParsesScalarAndReportsEmptyAsError(t *testing.T) {
	var v Value
	if err := v.UnmarshalJSON([]byte(" 42 ")); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if i, ok := v.IntValue(); !ok || i != 42 {
		t.Fatalf("parsed value = (%d,%v) want (42,true)", i, ok)
	}
	var empty Value
	if err := empty.UnmarshalJSON([]byte("   ")); err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestObjectFromJSONRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"non-object top level", `[1,2]`},
		{"trailing data", `{"a":1} extra`},
		{"unterminated string", `{"a":"x`},
		{"control char in string", "{\"a\":\"\x01\"}"},
		{"missing colon", `{"a" 1}`},
		{"missing comma between entries", `{"a":1 "b":2}`},
		{"missing comma in list", `{"a":[1 2]}`},
		{"unterminated list", `{"a":[1`},
		{"bad integer with leading zero", `{"a":01}`},
		{"invalid fraction", `{"a":1.}`},
		{"invalid exponent", `{"a":1e}`},
		{"non-string key", `{1:2}`},
		{"missing value after key", `{"a":`},
		{"partial true literal", `{"a":tru}`},
		{"partial null literal", `{"a":nul}`},
		{"bare minus", `{"a":-}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ObjectFromJSON([]byte(tc.in)); err == nil {
				t.Fatalf("expected error for %q", tc.in)
			}
		})
	}
}

func TestObjectTypedGettersHandlePresentWrongTypeAndMissing(t *testing.T) {
	o := Object{
		"s":         String("x"),
		"b":         Bool(true),
		"u":         Uint(5),
		"f":         Float(2.5),
		"bytes":     Bytes([]byte{1}),
		"obj":       ObjectValue(Object{"k": Int(1)}),
		"list":      List([]Value{String("a"), String("b")}),
		"mixedlist": List([]Value{String("a"), Int(2)}),
	}

	if v, ok := o.GetBool("b"); !ok || !v {
		t.Fatalf("GetBool present = (%v,%v)", v, ok)
	}
	if _, ok := o.GetBool("s"); ok {
		t.Fatal("GetBool wrong type returned ok")
	}
	if _, ok := o.GetBool("missing"); ok {
		t.Fatal("GetBool missing returned ok")
	}
	if v, ok := o.GetUint("u"); !ok || v != 5 {
		t.Fatalf("GetUint = (%d,%v)", v, ok)
	}
	if _, ok := o.GetUint("s"); ok {
		t.Fatal("GetUint wrong type returned ok")
	}
	if v, ok := o.GetFloat("f"); !ok || v != 2.5 {
		t.Fatalf("GetFloat = (%v,%v)", v, ok)
	}
	if _, ok := o.GetFloat("missing"); ok {
		t.Fatal("GetFloat missing returned ok")
	}
	if v, ok := o.GetBytes("bytes"); !ok || len(v) != 1 {
		t.Fatalf("GetBytes = (%v,%v)", v, ok)
	}
	if view, ok := o.GetObjectView("obj"); !ok || !view.Has("k") {
		t.Fatalf("GetObjectView = (%v,%v)", view, ok)
	}
	if _, ok := o.GetObjectView("missing"); ok {
		t.Fatal("GetObjectView missing returned ok")
	}
	if m, ok := o.GetInterfaceMap("obj"); !ok || m["k"] != int64(1) {
		t.Fatalf("GetInterfaceMap = (%v,%v)", m, ok)
	}
	if _, ok := o.GetInterfaceMap("s"); ok {
		t.Fatal("GetInterfaceMap wrong type returned ok")
	}
	if sl, ok := o.GetStringList("list"); !ok || !reflect.DeepEqual(sl, []string{"a", "b"}) {
		t.Fatalf("GetStringList = (%v,%v)", sl, ok)
	}
	if _, ok := o.GetStringList("mixedlist"); ok {
		t.Fatal("GetStringList with non-string element returned ok")
	}
	if _, ok := o.GetStringList("s"); ok {
		t.Fatal("GetStringList on non-list returned ok")
	}
	if !o.Has("s") || o.Has("missing") {
		t.Fatal("Has gave wrong membership")
	}
	if s, err := o.RequireString("s"); err != nil || s != "x" {
		t.Fatalf("RequireString present = (%q,%v)", s, err)
	}
	if _, err := o.RequireString("missing"); err == nil {
		t.Fatal("RequireString missing returned nil error")
	}
	if _, err := o.RequireString("b"); err == nil {
		t.Fatal("RequireString wrong type returned nil error")
	}
}

func TestObjectKeysAreSortedAndEmptyForEmptyObject(t *testing.T) {
	o := Object{"c": Null(), "a": Null(), "b": Null()}
	if got := o.Keys(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("Keys = %v want [a b c]", got)
	}
	if got := (Object{}).Keys(); len(got) != 0 {
		t.Fatalf("empty Keys = %v want []", got)
	}
}

func TestObjectFromMapEmptyAndUnsupportedValueFallsBackToString(t *testing.T) {
	if got := ObjectFromMap(nil); len(got) != 0 {
		t.Fatalf("ObjectFromMap(nil) = %v want empty", got)
	}
	if got := ObjectFromMap(map[string]any{}); len(got) != 0 {
		t.Fatalf("ObjectFromMap(empty) = %v want empty", got)
	}

	ch := make(chan int)
	got := ObjectFromMap(map[string]any{"ok": "v", "bad": ch})
	if s, ok := got.GetString("ok"); !ok || s != "v" {
		t.Fatalf("ok field = (%q,%v)", s, ok)
	}
	if _, ok := got.GetString("bad"); !ok {
		t.Fatal("unsupported value did not fall back to string")
	}
}

func TestValueInterfaceConvertsListsByElementShape(t *testing.T) {

	lo := List([]Value{ObjectValue(Object{"a": Int(1)}), ObjectValue(Object{"b": Int(2)})})
	maps, ok := lo.Interface().([]map[string]any)
	if !ok || len(maps) != 2 || maps[0]["a"] != int64(1) {
		t.Fatalf("object list Interface = %#v", lo.Interface())
	}

	if _, ok := List([]Value{Int(1), String("x")}).Interface().([]any); !ok {
		t.Fatalf("mixed list Interface type = %T want []any", List([]Value{Int(1), String("x")}).Interface())
	}
	if Null().Interface() != nil {
		t.Fatal("Null().Interface() != nil")
	}
	if bs, ok := Bytes([]byte("hi")).Interface().([]byte); !ok || string(bs) != "hi" {
		t.Fatalf("Bytes Interface = %#v", Bytes([]byte("hi")).Interface())
	}
}

func TestObjectJSONRoundTripPreservesValue(t *testing.T) {

	fixtures := []Object{
		{"s": String("hello"), "b": Bool(false), "i": Int(-42), "f": Float(3.25), "n": Null()},
		{
			"nested": ObjectValue(Object{"x": Int(1), "y": String("v")}),
			"list":   List([]Value{Int(1), Int(2), String("z")}),
		},
	}
	for i, o := range fixtures {
		data, err := o.MarshalJSON()
		if err != nil {
			t.Fatalf("fixture %d marshal error: %v", i, err)
		}
		back, err := ObjectFromJSON(data)
		if err != nil {
			t.Fatalf("fixture %d parse error: %v\njson=%s", i, err, data)
		}
		if !reflect.DeepEqual(back, o) {
			t.Fatalf("fixture %d round trip mismatch:\n got=%#v\nwant=%#v", i, back, o)
		}
	}
}
