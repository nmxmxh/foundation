package wsrouting

import (
	"errors"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// ErrRouteCapacity rejects a new route once the cache holds its bound (CP-02).
var ErrRouteCapacity = errors.New("wsrouting: route cache capacity reached")

// ErrRouteNoCapability rejects a route with a zero capability mask.
// A zero mask authorizes every actor. A caller that ignores the ok result of
// CapabilityMaskFor gets zero, so zero must be a deliberate public route.
var ErrRouteNoCapability = errors.New("wsrouting: route requires a capability; use SetPublicRoute for open routes")

// routeCacheBound returns the entry limit for a cache sized for expected entries.
func routeCacheBound(expected int) int {
	if expected <= 0 {
		expected = 1024
	}
	return expected * 2
}

// RouteMaplet is an in-memory edge cache mapping DeviceID or UserID to a
// server shard ID (0..15). The name is kept for API stability; it is exact.
// An approximate maplet overwrote one entity's shard with another's on a
// fingerprint alias, which misrouted that entity's traffic.
type RouteMaplet struct {
	mu     sync.RWMutex
	routes map[string]uint8
	limit  int
}

// NewRouteMaplet creates an edge routing cache sized for expectedConnections.
func NewRouteMaplet(expectedConnections int) *RouteMaplet {
	return &RouteMaplet{routes: make(map[string]uint8), limit: routeCacheBound(expectedConnections)}
}

// SetRoute records the server shard ID for an entity.
func (rm *RouteMaplet) SetRoute(entityID string, serverShardID uint8) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if _, exists := rm.routes[entityID]; !exists && len(rm.routes) >= rm.limit {
		return ErrRouteCapacity
	}
	rm.routes[entityID] = serverShardID
	return nil
}

// DeleteRoute removes an entity's cached route.
func (rm *RouteMaplet) DeleteRoute(entityID string) {
	rm.mu.Lock()
	delete(rm.routes, entityID)
	rm.mu.Unlock()
}

// GetRoute returns (shardID, true) if the entity is cached, or (0, false).
func (rm *RouteMaplet) GetRoute(entityID string) (uint8, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	shard, ok := rm.routes[entityID]
	return shard, ok
}

// MemoryBytes estimates the cache footprint: key bytes plus about 48 bytes of
// map overhead per entry. It is an estimate, not an exact measurement.
func (rm *RouteMaplet) MemoryBytes() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	total := 0
	for key := range rm.routes {
		total += len(key) + 48
	}
	return total
}

// Count returns the number of cached routes.
func (rm *RouteMaplet) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.routes)
}

// SecureRouteEntry bundles the destination server shard and the required capability mask.
type SecureRouteEntry struct {
	ShardID      uint8
	RequiredMask security.CapabilityMask
	Public       bool
}

// SecureRouteMaplet combines shard lookup with RBAC capability mask checks.
// Every decision reads one exact map, so GetRoute and RouteAndAuthorize agree.
type SecureRouteMaplet struct {
	mu     sync.RWMutex
	routes map[string]SecureRouteEntry
	limit  int
}

// NewSecureRouteMaplet creates an edge routing cache sized for expectedConnections.
func NewSecureRouteMaplet(expectedConnections int) *SecureRouteMaplet {
	return &SecureRouteMaplet{routes: make(map[string]SecureRouteEntry), limit: routeCacheBound(expectedConnections)}
}

// SetRoute records the shard and the capability mask a route requires.
// A zero mask is rejected with ErrRouteNoCapability.
func (rm *SecureRouteMaplet) SetRoute(routeKey string, serverShardID uint8, requiredMask security.CapabilityMask) error {
	if requiredMask == security.CapabilityMaskNone {
		return ErrRouteNoCapability
	}
	return rm.set(routeKey, SecureRouteEntry{ShardID: serverShardID, RequiredMask: requiredMask})
}

// SetPublicRoute records a route that any actor may use.
func (rm *SecureRouteMaplet) SetPublicRoute(routeKey string, serverShardID uint8) error {
	return rm.set(routeKey, SecureRouteEntry{ShardID: serverShardID, Public: true})
}

func (rm *SecureRouteMaplet) set(routeKey string, entry SecureRouteEntry) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if _, exists := rm.routes[routeKey]; !exists && len(rm.routes) >= rm.limit {
		return ErrRouteCapacity
	}
	rm.routes[routeKey] = entry
	return nil
}

// RouteAndAuthorize performs a route lookup and a bitwise capability check.
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
	if entry.Public {
		return entry.ShardID, true
	}
	if entry.RequiredMask == security.CapabilityMaskNone || (actorMask&entry.RequiredMask) != entry.RequiredMask {
		return entry.ShardID, false
	}
	return entry.ShardID, true
}

// GetRoute returns the shard for a route without an auth check.
func (rm *SecureRouteMaplet) GetRoute(routeKey string) (uint8, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	entry, ok := rm.routes[routeKey]
	return entry.ShardID, ok
}

// Count returns the number of cached routes.
func (rm *SecureRouteMaplet) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.routes)
}
