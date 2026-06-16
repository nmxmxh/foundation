package extension

// Branch coverage for error-propagation and defensive paths that the
// behavioural tests do not reach: marshalling a nested invalid Kind, parser
// rejection inside containers, empty-container fast paths, missing-key getters,
// and the reflection nil/float/named-slice branches. These are white-box
// structural tests (TE-02 permits them once the public behaviour is covered);
// each still asserts a concrete outcome (TE-03).

import "testing"

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
