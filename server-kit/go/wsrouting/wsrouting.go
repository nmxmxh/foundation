// Package wsrouting provides Redis-backed WebSocket connection routing
// for horizontal scaling. It enables sticky sessions and targeted message
// delivery across multiple server instances.
package wsrouting

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

const (
	// DefaultTTL is the default time-to-live for routing entries.
	DefaultTTL = 24 * time.Hour

	// KeyPrefixDevice is the Redis key prefix for device-to-server mapping.
	KeyPrefixDevice = "wsroute:device"

	// KeyPrefixUser is the Redis key prefix for user-to-servers mapping.
	KeyPrefixUser = "wsroute:user"

	// KeyPrefixServer is the Redis key prefix for server connection lists.
	KeyPrefixServer = "wsroute:server"
)

// Router manages WebSocket connection routing across server instances.
type Router struct {
	client   redis.Client
	serverID string
	ttl      time.Duration

	// Local connection tracking for this server instance
	mu          sync.RWMutex
	connections map[string]*ConnectionInfo
}

// ConnectionInfo holds metadata about a WebSocket connection.
type ConnectionInfo struct {
	ConnectionID string
	DeviceID     string
	UserID       string
	ServerID     string
	ConnectedAt  time.Time
}

// RouterOption configures a Router.
type RouterOption func(*Router)

// WithTTL sets the TTL for routing entries.
func WithTTL(ttl time.Duration) RouterOption {
	return func(r *Router) {
		if ttl > 0 {
			r.ttl = ttl
		}
	}
}

// NewRouter creates a new WebSocket connection router.
func NewRouter(client redis.Client, serverID string, opts ...RouterOption) *Router {
	if serverID == "" {
		serverID = fmt.Sprintf("server_%d", time.Now().UnixNano())
	}

	r := &Router{
		client:      client,
		serverID:    serverID,
		ttl:         DefaultTTL,
		connections: make(map[string]*ConnectionInfo),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// ServerID returns the unique identifier for this server instance.
func (r *Router) ServerID() string {
	return r.serverID
}

// Register registers a new WebSocket connection with the router.
// It updates both Redis routing tables and local connection tracking.
func (r *Router) Register(ctx context.Context, info ConnectionInfo) error {
	if info.ConnectionID == "" {
		return fmt.Errorf("connection_id is required")
	}
	if info.DeviceID == "" {
		info.DeviceID = info.ConnectionID
	}
	if info.ServerID == "" {
		info.ServerID = r.serverID
	}
	if info.ConnectedAt.IsZero() {
		info.ConnectedAt = time.Now().UTC()
	}

	// Store in local tracking
	r.mu.Lock()
	r.connections[info.ConnectionID] = &info
	r.mu.Unlock()

	// Update Redis routing tables
	if r.client == nil {
		return nil
	}

	// Device → Server mapping
	deviceKey := fmt.Sprintf("%s:%s", KeyPrefixDevice, info.DeviceID)
	if _, err := r.client.Incr(ctx, deviceKey); err != nil {
		return fmt.Errorf("failed to set device routing: %w", err)
	}
	// Store the actual server ID by publishing to a routing channel
	routePayload := fmt.Appendf(nil, "%s:%s", info.DeviceID, r.serverID)
	if err := r.client.Publish(ctx, "wsroute:register", routePayload); err != nil {
		return fmt.Errorf("failed to publish route: %w", err)
	}
	if _, err := r.client.Expire(ctx, deviceKey, r.ttl); err != nil {
		return fmt.Errorf("failed to set device TTL: %w", err)
	}

	// User → Servers mapping (if authenticated)
	if info.UserID != "" {
		userKey := fmt.Sprintf("%s:%s", KeyPrefixUser, info.UserID)
		if _, err := r.client.Incr(ctx, userKey); err != nil {
			return fmt.Errorf("failed to add user routing: %w", err)
		}
		if _, err := r.client.Expire(ctx, userKey, r.ttl); err != nil {
			return fmt.Errorf("failed to set user TTL: %w", err)
		}
	}

	return nil
}

// Unregister removes a WebSocket connection from the router.
func (r *Router) Unregister(ctx context.Context, connectionID string) error {
	if connectionID == "" {
		return fmt.Errorf("connection_id is required")
	}

	// Remove from local tracking
	r.mu.Lock()
	info, exists := r.connections[connectionID]
	delete(r.connections, connectionID)
	r.mu.Unlock()

	if !exists || r.client == nil {
		return nil
	}

	// Publish unregister event
	routePayload := fmt.Appendf(nil, "%s:%s:unregister", info.DeviceID, r.serverID)
	if err := r.client.Publish(ctx, "wsroute:unregister", routePayload); err != nil {
		return fmt.Errorf("failed to publish unregister: %w", err)
	}

	return nil
}

// UpdateAuth updates the authentication state of a connection.
// Call this when a WebSocket connection authenticates.
func (r *Router) UpdateAuth(ctx context.Context, connectionID, userID string) error {
	if connectionID == "" {
		return fmt.Errorf("connection_id is required")
	}

	r.mu.Lock()
	info, exists := r.connections[connectionID]
	if exists && info != nil {
		info.UserID = userID
	}
	r.mu.Unlock()

	if !exists || r.client == nil || userID == "" {
		return nil
	}

	// Add user → server mapping
	userKey := fmt.Sprintf("%s:%s", KeyPrefixUser, userID)
	if _, err := r.client.Incr(ctx, userKey); err != nil {
		return fmt.Errorf("failed to add user routing: %w", err)
	}
	if _, err := r.client.Expire(ctx, userKey, r.ttl); err != nil {
		return fmt.Errorf("failed to set user TTL: %w", err)
	}

	return nil
}

// GetLocalConnection returns connection info for a local connection.
func (r *Router) GetLocalConnection(connectionID string) (*ConnectionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.connections[connectionID]
	if ok && info != nil {
		copy := *info
		return &copy, true
	}
	return nil, false
}

// GetLocalConnectionByDevice returns connection info by device ID.
func (r *Router) GetLocalConnectionByDevice(deviceID string) (*ConnectionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, info := range r.connections {
		if info != nil && info.DeviceID == deviceID {
			copy := *info
			return &copy, true
		}
	}
	return nil, false
}

// GetLocalConnectionsByUser returns all local connections for a user.
func (r *Router) GetLocalConnectionsByUser(userID string) []*ConnectionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ConnectionInfo
	for _, info := range r.connections {
		if info != nil && info.UserID == userID {
			copy := *info
			result = append(result, &copy)
		}
	}
	return result
}

// LocalConnectionCount returns the number of connections on this server.
func (r *Router) LocalConnectionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.connections)
}

// ForEachLocal iterates over all local connections.
func (r *Router) ForEachLocal(fn func(*ConnectionInfo) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, info := range r.connections {
		if info == nil {
			continue
		}
		copy := *info
		if !fn(&copy) {
			return
		}
	}
}

// TargetedDelivery represents a message delivery target.
type TargetedDelivery struct {
	// TargetType indicates what kind of target this is.
	TargetType string // "device", "user", "connection", "broadcast"

	// TargetID is the specific target identifier.
	TargetID string

	// LocalOnly indicates if delivery should only go to local connections.
	LocalOnly bool
}

// ResolveTargets returns connection IDs that should receive a message.
// For local-only delivery, returns only this server's connections.
// For distributed delivery, publishes to Redis for other servers.
func (r *Router) ResolveTargets(ctx context.Context, target TargetedDelivery) ([]string, error) {
	var connectionIDs []string

	switch strings.ToLower(target.TargetType) {
	case "connection":
		if _, ok := r.GetLocalConnection(target.TargetID); ok {
			connectionIDs = append(connectionIDs, target.TargetID)
		}

	case "device":
		if info, ok := r.GetLocalConnectionByDevice(target.TargetID); ok {
			connectionIDs = append(connectionIDs, info.ConnectionID)
		}

	case "user":
		r.mu.RLock()
		for _, info := range r.connections {
			if info != nil && info.UserID == target.TargetID {
				connectionIDs = append(connectionIDs, info.ConnectionID)
			}
		}
		r.mu.RUnlock()

	case "broadcast":
		r.mu.RLock()
		connectionIDs = make([]string, 0, len(r.connections))
		for _, info := range r.connections {
			if info != nil {
				connectionIDs = append(connectionIDs, info.ConnectionID)
			}
		}
		r.mu.RUnlock()

	default:
		return nil, fmt.Errorf("unknown target type: %s", target.TargetType)
	}

	return connectionIDs, nil
}

// HealthSnapshot returns a snapshot of router health metrics.
type HealthSnapshot struct {
	ServerID         string
	LocalConnections int
	Timestamp        time.Time
}

// Health returns the current health snapshot.
func (r *Router) Health() HealthSnapshot {
	return HealthSnapshot{
		ServerID:         r.serverID,
		LocalConnections: r.LocalConnectionCount(),
		Timestamp:        time.Now().UTC(),
	}
}
