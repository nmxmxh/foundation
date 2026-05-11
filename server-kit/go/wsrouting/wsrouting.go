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

	// DefaultTargetBatchSize is the default batch size for chunked fanout.
	DefaultTargetBatchSize = 1024

	// MaxTargetBatchSize caps borrowed batch slices so callbacks stay bounded.
	MaxTargetBatchSize = 16384

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
	order       []string
	orderIndex  map[string]int
	byDevice    map[string]string
	byUser      map[string]map[string]struct{}
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
		order:       make([]string, 0),
		orderIndex:  make(map[string]int),
		byDevice:    make(map[string]string),
		byUser:      make(map[string]map[string]struct{}),
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
	if existing := r.connections[info.ConnectionID]; existing != nil {
		r.removeIndexesLocked(existing)
	} else {
		r.addConnectionOrderLocked(info.ConnectionID)
	}
	r.connections[info.ConnectionID] = &info
	r.addIndexesLocked(&info)
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
	if exists {
		r.removeIndexesLocked(info)
		r.removeConnectionOrderLocked(connectionID)
	}
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
		r.removeIndexesLocked(info)
		info.UserID = userID
		r.addIndexesLocked(info)
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
	connectionID := r.byDevice[deviceID]
	info := r.connections[connectionID]
	if info != nil {
		copy := *info
		return &copy, true
	}
	return nil, false
}

// GetLocalConnectionsByUser returns all local connections for a user.
func (r *Router) GetLocalConnectionsByUser(userID string) []*ConnectionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	userConnections := r.byUser[userID]
	result := make([]*ConnectionInfo, 0, len(userConnections))
	for connectionID := range userConnections {
		if info := r.connections[connectionID]; info != nil {
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
	return r.ResolveTargetsInto(ctx, target, nil)
}

// ResolveTargetsInto appends matching connection IDs into dst and returns the
// resulting slice. Callers that fan out frequently can reuse dst to avoid
// per-send allocation churn while preserving ResolveTargets semantics.
func (r *Router) ResolveTargetsInto(ctx context.Context, target TargetedDelivery, dst []string) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return dst, err
	}
	connectionIDs := dst

	switch strings.ToLower(target.TargetType) {
	case "connection":
		r.mu.RLock()
		if r.connections[target.TargetID] != nil {
			connectionIDs = append(connectionIDs, target.TargetID)
		}
		r.mu.RUnlock()

	case "device":
		r.mu.RLock()
		connectionID := r.byDevice[target.TargetID]
		if r.connections[connectionID] != nil {
			connectionIDs = append(connectionIDs, connectionID)
		}
		r.mu.RUnlock()

	case "user":
		r.mu.RLock()
		userConnections := r.byUser[target.TargetID]
		connectionIDs = ensureStringCapacity(connectionIDs, len(userConnections))
		for connectionID := range userConnections {
			if r.connections[connectionID] != nil {
				connectionIDs = append(connectionIDs, connectionID)
			}
		}
		r.mu.RUnlock()

	case "broadcast":
		r.mu.RLock()
		connectionIDs = ensureStringCapacity(connectionIDs, len(r.order))
		connectionIDs = append(connectionIDs, r.order...)
		if err := contextError(ctx); err != nil {
			r.mu.RUnlock()
			return connectionIDs, err
		}
		r.mu.RUnlock()

	default:
		return nil, fmt.Errorf("unknown target type: %s", target.TargetType)
	}

	return connectionIDs, nil
}

// ForEachTarget invokes fn for every matching local connection ID without
// materializing a result slice. The callback runs while the router read lock is
// held, so it must stay non-blocking and must not call back into Router.
func (r *Router) ForEachTarget(ctx context.Context, target TargetedDelivery, fn func(connectionID string) bool) (int, error) {
	if fn == nil {
		return 0, nil
	}
	if err := contextError(ctx); err != nil {
		return 0, err
	}

	count := 0
	r.mu.RLock()
	defer r.mu.RUnlock()

	visitIndexed := func(connectionID string) bool {
		count++
		if count&1023 == 0 {
			if err := contextError(ctx); err != nil {
				return false
			}
		}
		return fn(connectionID)
	}

	switch strings.ToLower(target.TargetType) {
	case "connection":
		if r.connections[target.TargetID] != nil {
			visitIndexed(target.TargetID)
		}
	case "device":
		if connectionID := r.byDevice[target.TargetID]; connectionID != "" {
			visitIndexed(connectionID)
		}
	case "user":
		for connectionID := range r.byUser[target.TargetID] {
			if !visitIndexed(connectionID) {
				return count, contextError(ctx)
			}
		}
	case "broadcast":
		for _, connectionID := range r.order {
			if !visitIndexed(connectionID) {
				return count, contextError(ctx)
			}
		}
	default:
		return 0, fmt.Errorf("unknown target type: %s", target.TargetType)
	}
	return count, contextError(ctx)
}

// ForEachTargetBatch invokes fn with batches of matching local connection IDs.
// For broadcast targets, batch slices are borrowed from the router's contiguous
// connection index and are valid only until fn returns. The callback runs while
// the router read lock is held, so it must enqueue work without blocking and
// must not call back into Router.
func (r *Router) ForEachTargetBatch(ctx context.Context, target TargetedDelivery, batchSize int, fn func(connectionIDs []string) bool) (int, error) {
	if fn == nil {
		return 0, nil
	}
	if err := contextError(ctx); err != nil {
		return 0, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	estimated := r.estimateTargetCountLocked(target)
	batchSize = normalizeTargetBatchSize(batchSize, estimated)
	count := 0
	emit := func(batch []string) bool {
		if len(batch) == 0 {
			return true
		}
		count += len(batch)
		if err := contextError(ctx); err != nil {
			return false
		}
		return fn(batch)
	}

	switch strings.ToLower(target.TargetType) {
	case "connection":
		if r.connections[target.TargetID] != nil {
			one := [1]string{target.TargetID}
			emit(one[:])
		}
	case "device":
		if connectionID := r.byDevice[target.TargetID]; connectionID != "" {
			one := [1]string{connectionID}
			emit(one[:])
		}
	case "user":
		var stack [DefaultTargetBatchSize]string
		chunk := stack[:0]
		if batchSize > len(stack) {
			chunk = make([]string, 0, batchSize)
		}
		for connectionID := range r.byUser[target.TargetID] {
			chunk = append(chunk, connectionID)
			if len(chunk) == batchSize {
				if !emit(chunk) {
					return count, contextError(ctx)
				}
				chunk = chunk[:0]
			}
		}
		if !emit(chunk) {
			return count, contextError(ctx)
		}
	case "broadcast":
		for start := 0; start < len(r.order); start += batchSize {
			end := start + batchSize
			if end > len(r.order) {
				end = len(r.order)
			}
			if !emit(r.order[start:end]) {
				return count, contextError(ctx)
			}
		}
	default:
		return 0, fmt.Errorf("unknown target type: %s", target.TargetType)
	}
	return count, contextError(ctx)
}

func (r *Router) estimateTargetCountLocked(target TargetedDelivery) int {
	switch strings.ToLower(target.TargetType) {
	case "connection":
		if r.connections[target.TargetID] == nil {
			return 0
		}
		return 1
	case "device":
		if connectionID := r.byDevice[target.TargetID]; connectionID != "" && r.connections[connectionID] != nil {
			return 1
		}
		return 0
	case "user":
		return len(r.byUser[target.TargetID])
	case "broadcast":
		return len(r.order)
	default:
		return 0
	}
}

func normalizeTargetBatchSize(batchSize, estimated int) int {
	if batchSize > 0 {
		if batchSize > MaxTargetBatchSize {
			return MaxTargetBatchSize
		}
		return batchSize
	}
	if estimated <= 0 {
		return DefaultTargetBatchSize
	}
	if estimated < DefaultTargetBatchSize {
		return estimated
	}
	if estimated >= 1_000_000 {
		return 4096
	}
	return DefaultTargetBatchSize
}

func (r *Router) addConnectionOrderLocked(connectionID string) {
	if connectionID == "" {
		return
	}
	if _, ok := r.orderIndex[connectionID]; ok {
		return
	}
	r.orderIndex[connectionID] = len(r.order)
	r.order = append(r.order, connectionID)
}

func (r *Router) removeConnectionOrderLocked(connectionID string) {
	index, ok := r.orderIndex[connectionID]
	if !ok {
		return
	}
	lastIndex := len(r.order) - 1
	lastConnectionID := r.order[lastIndex]
	r.order[index] = lastConnectionID
	r.orderIndex[lastConnectionID] = index
	r.order[lastIndex] = ""
	r.order = r.order[:lastIndex]
	delete(r.orderIndex, connectionID)
}

func ensureStringCapacity(values []string, additional int) []string {
	if additional <= 0 || cap(values)-len(values) >= additional {
		return values
	}
	next := make([]string, len(values), len(values)+additional)
	copy(next, values)
	return next
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (r *Router) addIndexesLocked(info *ConnectionInfo) {
	if info == nil {
		return
	}
	if info.DeviceID != "" {
		r.byDevice[info.DeviceID] = info.ConnectionID
	}
	if info.UserID != "" {
		if r.byUser[info.UserID] == nil {
			r.byUser[info.UserID] = make(map[string]struct{})
		}
		r.byUser[info.UserID][info.ConnectionID] = struct{}{}
	}
}

func (r *Router) removeIndexesLocked(info *ConnectionInfo) {
	if info == nil {
		return
	}
	if info.DeviceID != "" && r.byDevice[info.DeviceID] == info.ConnectionID {
		delete(r.byDevice, info.DeviceID)
	}
	if info.UserID != "" {
		delete(r.byUser[info.UserID], info.ConnectionID)
		if len(r.byUser[info.UserID]) == 0 {
			delete(r.byUser, info.UserID)
		}
	}
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
