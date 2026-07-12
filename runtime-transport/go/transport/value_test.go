package transport

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
)

type testStringer string

func (s testStringer) String() string { return string(s) }

func TestValueScalarAccessors(t *testing.T) {
	if got, ok := String("value").StringValue(); !ok || got != "value" {
		t.Fatalf("StringValue = %q, %v", got, ok)
	}
	if got, ok := (Value{raw: testStringer("stringer")}).StringValue(); !ok || got != "stringer" {
		t.Fatalf("Stringer value = %q, %v", got, ok)
	}
	if _, ok := Int(1).StringValue(); ok {
		t.Fatal("integer unexpectedly converted to string")
	}
	if got, ok := Bool(true).BoolValue(); !ok || !got {
		t.Fatalf("BoolValue = %v, %v", got, ok)
	}
	if _, ok := String("true").BoolValue(); ok {
		t.Fatal("string unexpectedly converted to bool")
	}
	if got := Float(1.5).Interface(); got != 1.5 {
		t.Fatalf("Float interface = %v", got)
	}
}

func TestValueInt64Conversions(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int64
		ok    bool
	}{
		{name: "int", value: int(1), want: 1, ok: true},
		{name: "int32", value: int32(2), want: 2, ok: true},
		{name: "int64", value: int64(3), want: 3, ok: true},
		{name: "uint", value: uint(4), want: 4, ok: true},
		{name: "uint overflow", value: uint64(math.MaxInt64) + 1},
		{name: "uint64", value: uint64(5), want: 5, ok: true},
		{name: "float integer", value: float64(6), want: 6, ok: true},
		{name: "float fraction", value: 6.5},
		{name: "number", value: json.Number("7"), want: 7, ok: true},
		{name: "bad number", value: json.Number("7.5")},
		{name: "string", value: " 8 ", want: 8, ok: true},
		{name: "bad string", value: "eight"},
		{name: "unsupported", value: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := (Value{raw: tc.value}).Int64Value()
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Int64Value = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestValueContainersCloneAndAccess(t *testing.T) {
	original := ObjectFromMap(map[string]any{
		"name":   "foundation",
		"nested": map[string]any{"enabled": true},
		"items":  []any{json.Number("1"), "two"},
	})
	if name, ok := original.GetString("name"); !ok || name != "foundation" {
		t.Fatalf("GetString = %q, %v", name, ok)
	}
	if _, ok := original.GetString("missing"); ok {
		t.Fatal("missing key unexpectedly found")
	}
	if got := ObjectFromMap(nil); len(got) != 0 {
		t.Fatalf("empty object = %#v", got)
	}

	objectValue := ObjectValue(original)
	object, ok := objectValue.ObjectValue()
	if !ok || !reflect.DeepEqual(object.InterfaceMap(), original.InterfaceMap()) {
		t.Fatalf("ObjectValue = %#v, %v", object, ok)
	}
	object["name"] = String("changed")
	if name, _ := original.GetString("name"); name != "foundation" {
		t.Fatal("object clone changed the source")
	}
	if _, ok := String("not object").ObjectValue(); ok {
		t.Fatal("scalar unexpectedly converted to object")
	}
	clone := original.Clone()
	clone["name"] = String("clone")
	if name, _ := original.GetString("name"); name != "foundation" {
		t.Fatal("Clone changed the source")
	}
	if object, ok := (Value{raw: map[string]any{"id": "1"}}).ObjectValue(); !ok || len(object) != 1 {
		t.Fatalf("map ObjectValue = %#v, %v", object, ok)
	}

	list := List([]Value{Int(1), String("two")})
	items, ok := list.ListValue()
	if !ok || len(items) != 2 {
		t.Fatalf("ListValue = %#v, %v", items, ok)
	}
	if direct, ok := (Value{raw: []Value{Bool(true)}}).ListValue(); !ok || len(direct) != 1 {
		t.Fatalf("direct ListValue = %#v, %v", direct, ok)
	}
	if _, ok := Bool(false).ListValue(); ok {
		t.Fatal("bool unexpectedly converted to list")
	}
	if clone := (Object{}).Clone(); clone == nil || len(clone) != 0 {
		t.Fatalf("empty clone = %#v", clone)
	}
}

func TestValueJSONRoundTripAndErrors(t *testing.T) {
	value := Value{raw: Value{raw: Object{"name": String("foundation")}}}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != `{"name":"foundation"}` {
		t.Fatalf("MarshalJSON = %s, %v", encoded, err)
	}
	var decoded Value
	if err := json.Unmarshal([]byte(`{"items":[1,true,null]}`), &decoded); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	object, ok := decoded.ObjectValue()
	if !ok {
		t.Fatal("decoded JSON is not an object")
	}
	items, ok := object["items"].ListValue()
	if !ok || len(items) != 3 {
		t.Fatalf("decoded list = %#v, %v", items, ok)
	}
	if err := json.Unmarshal([]byte(`{"broken":`), &decoded); err == nil {
		t.Fatal("malformed JSON unexpectedly accepted")
	}
	if got := valueFromAny(String("kept")); got.Interface() != "kept" {
		t.Fatalf("Value conversion = %#v", got)
	}
	if got := valueFromAny(Object{"id": Int(9)}); got.Interface().(map[string]any)["id"] != int64(9) {
		t.Fatalf("Object conversion = %#v", got)
	}
	marshaled, err := json.Marshal(Value{raw: func() {}})
	if err == nil {
		t.Fatal("unsupported JSON value unexpectedly marshaled")
	}
	if marshaled != nil {
		t.Fatalf("failed marshal returned bytes: %q", marshaled)
	}
	if got := fmt.Sprint(decoded.Interface()); got == "" {
		t.Fatal("decoded interface unexpectedly empty")
	}
}
