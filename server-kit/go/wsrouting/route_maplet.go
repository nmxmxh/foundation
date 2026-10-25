package wsrouting

import (
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
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
