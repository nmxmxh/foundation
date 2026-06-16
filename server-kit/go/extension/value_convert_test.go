package extension

// Conversion coverage: FromJSON's typed fast-path switch and the
// reflection fallback (valueFromReflect), plus the remaining accessor and
// Interface branches. Oracles assert the resulting Kind and value (TE-03), and
// inputs span each supported Go type plus the rejected classes (TE-04).

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFromJSONConvertsScalarAndTimeTypes(t *testing.T) {
	if v, err := FromJSON(nil); err != nil || v.Kind() != KindNull {
		t.Fatalf("nil -> (%d,%v)", v.Kind(), err)
	}
	// Value passes through as a clone preserving its scalar payload.
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
	// A slice element that cannot convert propagates the error.
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
	// Pointer is dereferenced.
	n := 5
	if v, _ := FromJSON(&n); func() bool { i, ok := v.IntValue(); return ok && i == 5 }() == false {
		t.Fatal("pointer not dereferenced to int")
	}
	// map[string]int is not in the fast switch; reflection must handle it.
	if v, _ := FromJSON(map[string]int{"k": 1}); v.Kind() != KindObject {
		t.Fatalf("map[string]int -> %d want KindObject", v.Kind())
	}
	// Struct: renamed tag kept, "-" skipped, exported plain kept, unexported dropped.
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
	// Array (not slice) goes through the reflection array branch.
	if v, _ := FromJSON([2]int{1, 2}); v.Kind() != KindList {
		t.Fatalf("array -> %d want KindList", v.Kind())
	}
	// Named byte slice maps to bytes via reflection.
	type byteSlice []byte
	if v, _ := FromJSON(byteSlice("hi")); v.Kind() != KindBytes {
		t.Fatalf("named byte slice -> %d want KindBytes", v.Kind())
	}
	// Unsupported kind errors.
	if _, err := FromJSON(make(chan int)); err == nil {
		t.Fatal("channel should error")
	}
	// Non-string map key errors.
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
	// Empty list is not all-objects; it materialises as []any.
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
