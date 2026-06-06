package extension

import (
	"testing"
	"time"
)

func BenchmarkObjectFromMapApplicationPayload(b *testing.B) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{
		"correlation_id": "corr-bench",
		"actor_id":       int64(42),
		"workspace_id":   uint64(7),
		"confidence":     0.97,
		"active":         true,
		"observed_at":    now,
		"metadata": map[string]any{
			"source": "handler",
			"tags":   []string{"strict", "typed", "hotplane"},
		},
		"records": []map[string]any{
			{"id": "a", "status": "active", "score": 1.2},
			{"id": "b", "status": "queued", "score": 0.4},
		},
	}

	b.ReportAllocs()
	for b.Loop() {
		obj := ObjectFromMap(payload)
		if id, ok := obj.GetInt("actor_id"); !ok || id != 42 {
			b.Fatal("typed integer not preserved")
		}
	}
}

func BenchmarkObjectInterfaceMapObjectList(b *testing.B) {
	obj := Object{
		"items": List([]Value{
			ObjectValue(Object{"id": String("a"), "score": Float(1.2)}),
			ObjectValue(Object{"id": String("b"), "score": Float(0.4)}),
			ObjectValue(Object{"id": String("c"), "score": Float(0.8)}),
		}),
		"total": Int(3),
	}

	b.ReportAllocs()
	for b.Loop() {
		values := obj.InterfaceMap()
		items, ok := values["items"].([]map[string]any)
		if !ok || len(items) != 3 {
			b.Fatal("object list projection not preserved")
		}
	}
}
