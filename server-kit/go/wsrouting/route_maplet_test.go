package wsrouting

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
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
	for i := 0; b.Loop(); i++ {
		k := keys[i&(n-1)]
		shard, _ := rm.GetRoute(k)
		_ = shard
	}
}

func TestSecureRouteMaplet_RouteAndAuthorize(t *testing.T) {
	sm := NewSecureRouteMaplet(100)

	capRead := security.CapabilityMask(1 << 0)
	capWrite := security.CapabilityMask(1 << 1)
	capAdmin := security.CapabilityMask(1 << 2)

	_ = sm.SetRoute("orders.read", 1, capRead)
	_ = sm.SetRoute("orders.write", 2, capRead|capWrite)
	_ = sm.SetRoute("admin.ops", 3, capAdmin)

	// User with read-only
	userRead := capRead
	shard, auth := sm.RouteAndAuthorize("orders.read", userRead)
	if !auth || shard != 1 {
		t.Fatalf("expected read-only authorized on orders.read with shard 1, got auth=%v shard=%d", auth, shard)
	}

	shard, auth = sm.RouteAndAuthorize("orders.write", userRead)
	if auth || shard != 2 {
		t.Fatalf("expected read-only denied on orders.write, got auth=%v shard=%d", auth, shard)
	}

	// User with read-write
	userWrite := capRead | capWrite
	shard, auth = sm.RouteAndAuthorize("orders.write", userWrite)
	if !auth || shard != 2 {
		t.Fatalf("expected write authorized on orders.write with shard 2, got auth=%v shard=%d", auth, shard)
	}

	shard, auth = sm.RouteAndAuthorize("admin.ops", userWrite)
	if auth || shard != 3 {
		t.Fatalf("expected write denied on admin.ops, got auth=%v shard=%d", auth, shard)
	}

	// Unknown route
	shard, auth = sm.RouteAndAuthorize("unknown.route", security.CapabilityMaskAll)
	if auth || shard != 0 {
		t.Fatalf("expected unknown route to return auth=false, shard=0")
	}

	// Fast-path GetRoute lookup
	if s, ok := sm.GetRoute("orders.read"); !ok || s != 1 {
		t.Fatalf("expected GetRoute orders.read to return 1, true; got %d, %v", s, ok)
	}
	if _, ok := sm.GetRoute("unknown.route"); ok {
		t.Fatal("expected GetRoute unknown.route to return ok=false")
	}

	if sm.Count() != 3 {
		t.Fatalf("expected count 3, got %d", sm.Count())
	}
}

func BenchmarkSecureRouteMaplet_RouteAndAuthorize(b *testing.B) {
	const n = 1000
	sm := NewSecureRouteMaplet(n)
	capRead := security.CapabilityMask(1 << 0)
	capWrite := security.CapabilityMask(1 << 1)

	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("route_topic_%d", i)
		_ = sm.SetRoute(keys[i], uint8(i%16), capRead|capWrite)
	}

	actorMask := capRead | capWrite // authorized actor

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		k := keys[i&(n-1)]
		shard, auth := sm.RouteAndAuthorize(k, actorMask)
		_ = shard
		_ = auth
	}
}

// TestSecureRouteRejectsZeroMask guards the fail-open case: a zero mask
// authorizes everyone, so only SetPublicRoute may create an open route.
func TestSecureRouteRejectsZeroMask(t *testing.T) {
	sm := NewSecureRouteMaplet(8)
	if err := sm.SetRoute("orders.read", 1, security.CapabilityMaskNone); !errors.Is(err, ErrRouteNoCapability) {
		t.Fatalf("zero mask: got %v, want ErrRouteNoCapability", err)
	}
	if _, ok := sm.RouteAndAuthorize("orders.read", security.CapabilityMaskNone); ok {
		t.Fatal("rejected route must not authorize")
	}
	if err := sm.SetPublicRoute("health", 2); err != nil {
		t.Fatalf("public route: %v", err)
	}
	if shard, ok := sm.RouteAndAuthorize("health", security.CapabilityMaskNone); !ok || shard != 2 {
		t.Fatalf("public route: shard=%d ok=%v", shard, ok)
	}
}

// TestRouteMapletExactAndBounded guards exactness: no route answers for a key
// that was never set, and no stored route is overwritten by another key.
func TestRouteMapletExactAndBounded(t *testing.T) {
	rm := NewRouteMaplet(1000)
	for i := range 2000 {
		if err := rm.SetRoute(fmt.Sprintf("device-%d", i), uint8(i%16)); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}
	for i := range 2000 {
		if shard, ok := rm.GetRoute(fmt.Sprintf("device-%d", i)); !ok || shard != uint8(i%16) {
			t.Fatalf("device-%d: shard=%d ok=%v", i, shard, ok)
		}
	}
	for i := range 20000 {
		if _, ok := rm.GetRoute(fmt.Sprintf("absent-%d", i)); ok {
			t.Fatalf("absent-%d returned a route", i)
		}
	}
	if err := rm.SetRoute("one-more", 1); !errors.Is(err, ErrRouteCapacity) {
		t.Fatalf("over bound: got %v, want ErrRouteCapacity", err)
	}
	if err := rm.SetRoute("device-1", 9); err != nil {
		t.Fatalf("update at bound must succeed: %v", err)
	}
}
