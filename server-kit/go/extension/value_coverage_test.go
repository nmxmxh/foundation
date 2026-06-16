package extension

// These tests target the typed-value contract of extension.Value/Object:
// constructor → accessor round-trips, defensive copy boundaries, canonical and
// fast JSON marshalling, the hand-rolled JSON parser (happy paths, whitespace,
// and every rejection branch), reflection conversion fallbacks, and the typed
// Object getters. Oracles assert visible behaviour (exact bytes, exact values,
// kind, error presence) rather than "no error" (TE-03), cases are chosen by
// equivalence class and boundary (TE-04), and inputs are fixed for determinism
// (TE-27).

import (
	"math"
	"reflect"
	"testing"
)

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
	// Integer boundaries: min and max int64 must survive intact.
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
	src[0] = 9 // mutate caller slice after construction
	got, ok := v.BytesValue()
	if !ok || !reflect.DeepEqual(got, []byte{1, 2, 3}) {
		t.Fatalf("constructor aliased caller memory: got %v ok=%v", got, ok)
	}
	got[1] = 8 // mutate returned slice
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
	src[0] = Int(99) // mutate caller slice after construction
	got, ok := v.ListValue()
	if !ok || len(got) != 2 {
		t.Fatalf("ListValue = (%v,%v)", got, ok)
	}
	if first, _ := got[0].IntValue(); first != 1 {
		t.Fatalf("constructor aliased caller list: first=%d", first)
	}
	got[0] = Int(7) // mutate returned slice
	again, _ := v.ListValue()
	if first, _ := again[0].IntValue(); first != 1 {
		t.Fatalf("accessor returned aliased list: first=%d", first)
	}
}

func TestObjectValueClonesWhileObjectViewShares(t *testing.T) {
	src := Object{"k": Int(1)}
	v := ObjectValue(src)
	src["k"] = Int(2) // mutate caller object after construction
	got, ok := v.ObjectValue()
	if !ok {
		t.Fatal("ObjectValue accessor not ok")
	}
	if iv, _ := got["k"].IntValue(); iv != 1 {
		t.Fatalf("constructor aliased caller object: k=%d", iv)
	}
	// ObjectView shares the internal object; mutation is visible through it.
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
	v := Value{kind: KindObject} // obj is nil
	got, ok := v.ObjectView()
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("ObjectView(nil) = (%v,%v) want (empty,true)", got, ok)
	}
}

func TestValueCloneDeepCopiesContainersAndPassesScalars(t *testing.T) {
	orig := ObjectValue(Object{"k": Int(1)})
	clone := orig.Clone()
	view, _ := clone.ObjectView()
	view["k"] = Int(42) // mutate the clone
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
	// Fast marshalling does not sort keys; the oracle is semantic equality after
	// a parse round-trip, not byte equality.
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
	// Leading/trailing/intra whitespace exercises skipSpace.
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
	// Beyond int64 range; the number must fall back to float, not overflow.
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
	// A channel cannot be represented; ObjectFromMap must fall back to a string
	// rather than drop the field or panic.
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
	// Homogeneous list of objects materialises as []map[string]any.
	lo := List([]Value{ObjectValue(Object{"a": Int(1)}), ObjectValue(Object{"b": Int(2)})})
	maps, ok := lo.Interface().([]map[string]any)
	if !ok || len(maps) != 2 || maps[0]["a"] != int64(1) {
		t.Fatalf("object list Interface = %#v", lo.Interface())
	}
	// Mixed list materialises as []any.
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
	// Bounded, fixed fixtures (TE-31): canonical marshal then parse must yield
	// a deeply equal object. JSON-native kinds only — bytes have no JSON type.
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
