package extension

import (
	"encoding/json"
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
