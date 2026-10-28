package wsrouting

import (
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// RouteMaplet is an in-memory edge cache mapping DeviceID or UserID to
// Server Shard ID (0..15). It allows WebSocket routing decisions to resolve
// in nanoseconds locally, eliminating repetitive Redis wsroute polls.
type RouteMaplet struct {
	mu     sync.RWMutex
	maplet *metadata.WideMaplet
}

// NewRouteMaplet creates an edge routing cache sized for expectedConnections.
func NewRouteMaplet(expectedConnections int) *RouteMaplet {
	return &RouteMaplet{
		maplet: metadata.NewWideMaplet(expectedConnections),
	}
}

// SetRoute records the server shard ID (0..15) for an entity.
func (rm *RouteMaplet) SetRoute(entityID string, serverShardID uint8) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.maplet.Insert(entityID, serverShardID)
}

// GetRoute performs a fast-path lookup for the server shard ID.
// Returns (shardID, true) if cached locally, or (0, false) if absent from cache.
func (rm *RouteMaplet) GetRoute(entityID string) (uint8, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.maplet.Lookup(entityID)
}

// MemoryBytes returns the raw memory in bytes of the backing maplet slab.
func (rm *RouteMaplet) MemoryBytes() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.maplet.MemoryBytes()
}

// Count returns the number of cached routes.
func (rm *RouteMaplet) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.maplet.Count()
}

// SecureRouteEntry bundles the destination server shard and the required capability mask.
type SecureRouteEntry struct {
	ShardID      uint8
	RequiredMask security.CapabilityMask
}

// SecureRouteMaplet combines high-speed shard lookup with zero-allocation RBAC
// capability bitmask verification in a single cache-efficient step.
type SecureRouteMaplet struct {
	mu     sync.RWMutex
	maplet *metadata.WideMaplet
	routes map[string]SecureRouteEntry
}

// NewSecureRouteMaplet creates an edge routing cache sized for expectedConnections.
func NewSecureRouteMaplet(expectedConnections int) *SecureRouteMaplet {
	return &SecureRouteMaplet{
		maplet: metadata.NewWideMaplet(expectedConnections),
		routes: make(map[string]SecureRouteEntry, expectedConnections),
	}
}

// SetRoute records the server shard ID (0..15) and required capability mask for a route.
func (rm *SecureRouteMaplet) SetRoute(routeKey string, serverShardID uint8, requiredMask security.CapabilityMask) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if err := rm.maplet.Insert(routeKey, serverShardID); err != nil {
		return err
	}
	rm.routes[routeKey] = SecureRouteEntry{
		ShardID:      serverShardID,
		RequiredMask: requiredMask,
	}
	return nil
}

// RouteAndAuthorize performs an atomic route lookup and bitwise capability check.
// Returns (shardID, true) if the route exists and actorMask satisfies RequiredMask.
// Returns (shardID, false) if the route exists but actor lacks required capabilities.
// Returns (0, false) if the route does not exist.
func (rm *SecureRouteMaplet) RouteAndAuthorize(routeKey string, actorMask security.CapabilityMask) (uint8, bool) {
	rm.mu.RLock()
	entry, ok := rm.routes[routeKey]
	rm.mu.RUnlock()
	if !ok {
		return 0, false
	}
	if (actorMask & entry.RequiredMask) != entry.RequiredMask {
		return entry.ShardID, false
	}
	return entry.ShardID, true
}

// GetRoute performs a fast-path lookup for the server shard ID without auth check.
func (rm *SecureRouteMaplet) GetRoute(routeKey string) (uint8, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.maplet.Lookup(routeKey)
}

// Count returns the number of cached routes.
func (rm *SecureRouteMaplet) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.maplet.Count()
}
