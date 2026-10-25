package wsrouting

import (
	"fmt"
	"testing"
)

func TestRouteMaplet_SetAndGet(t *testing.T) {
	rm := NewRouteMaplet(500)

	// Set routes for devices across 4 server shards
	for i := range 200 {
		devID := fmt.Sprintf("dev_%d", i)
		shardID := uint8(i % 4)
		if err := rm.SetRoute(devID, shardID); err != nil {
			t.Fatalf("failed to set route at %d: %v", i, err)
		}
	}

	// Verify lookups
	for i := range 200 {
		devID := fmt.Sprintf("dev_%d", i)
		expectedShard := uint8(i % 4)
		shard, ok := rm.GetRoute(devID)
		if !ok || shard != expectedShard {
			t.Fatalf("expected shard %d for %s, got %d (found=%v)", expectedShard, devID, shard, ok)
		}
	}

	// Absent entity
	_, ok := rm.GetRoute("unknown_device_999")
	if ok {
		t.Fatalf("expected unknown device to return false")
	}

	// Stats check
	if rm.Count() != 200 {
		t.Fatalf("expected 200 routes, got %d", rm.Count())
	}
	if rm.MemoryBytes() == 0 {
		t.Fatalf("expected non-zero memory bytes")
	}
}

func BenchmarkRouteMaplet_GetRoute(b *testing.B) {
	const n = 10000
	rm := NewRouteMaplet(n)
	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("dev_session_%d", i)
		_ = rm.SetRoute(keys[i], uint8(i%8))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		k := keys[i&(n-1)]
		shard, _ := rm.GetRoute(k)
		_ = shard
	}
}
