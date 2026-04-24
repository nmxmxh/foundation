# WebSocket Scaling Guide

This document covers the WebSocket scaling capabilities in the Ovasabi Foundation, including CPU-aware auto-tuning, connection routing, and metrics collection.

## Overview

The foundation provides three modules for WebSocket scaling:

1. **scaling** - CPU-aware auto-tuning for server resources
2. **wsrouting** - Redis-backed connection routing for horizontal scaling
3. **wsmetrics** - Connection and message metrics collection

## CPU-Aware Auto-Tuning

The `scaling` module automatically configures server resources based on available CPU cores.

### Scaling Tiers

| Tier | CPU Cores | Max WS Connections | Dispatch Concurrency | DB Connections |
|------|-----------|-------------------|---------------------|----------------|
| Development | 1-4 | 5,000 | 64 | 20 |
| Mid-Range | 5-8 | 20,000 | 128 | 40 |
| Production | 9-16 | 50,000 | 256 | 80 |
| Hyperscale | 16+ | 100,000 | 512 | 120 |

### Usage

```go
import "github.com/ovasabi/foundation/server-kit/go/scaling"

// Auto-detect CPU count and get tuned config
cfg := scaling.AutoTune()

// Or tune for a specific core count
cfg := scaling.AutoTuneForCores(16)

// Access configuration values
maxConns := cfg.WSMaxConnections
dispatchConcurrency := cfg.DispatchMaxConcurrent

// Scale workers based on tier
workers := cfg.ScaleWorkers(4) // Returns 4, 8, 16, or 24 depending on tier
```

### Configuration Fields

```go
type Config struct {
    Tier                    Tier
    CPUCount                int

    // WebSocket
    WSMaxConnections        int
    WSWriteQueueDepth       int
    WSReadLimitBytes        int64
    WSPingInterval          time.Duration
    WSGuestIdleTimeout      time.Duration
    WSGuestRateLimit        int
    WSGuestRateBurst        int

    // Dispatch
    DispatchMaxConcurrent   int
    DispatchAcquireTimeout  time.Duration

    // Database
    DBMaxConnections        int
    DBMinConnections        int

    // Workers
    QueueWorkersDefault     int
    QueueWorkersEventTransport int
    QueueWorkersHeavy       int

    // API
    APIRateLimitPerMinute   int
    APIRateLimitBurst       int
}
```

## WebSocket Connection Routing

The `wsrouting` module enables horizontal scaling by tracking connections across server instances via Redis.

### Features

- Device-to-server mapping for sticky sessions
- User-to-servers mapping for targeted delivery
- Local connection tracking for fast lookups
- Graceful degradation without Redis

### Usage

```go
import (
    "github.com/ovasabi/foundation/server-kit/go/redis"
    "github.com/ovasabi/foundation/server-kit/go/wsrouting"
)

// Create a Redis client
redisClient, _ := redis.Connect(redisURL, "myapp", "redis")

// Create a router
router := wsrouting.NewRouter(redisClient, "server-1",
    wsrouting.WithTTL(24 * time.Hour),
)

// Register a connection
router.Register(ctx, wsrouting.ConnectionInfo{
    ConnectionID: "conn-123",
    DeviceID:     "device-456",
    UserID:       "user-789",
})

// Update auth state when user authenticates
router.UpdateAuth(ctx, "conn-123", "user-789")

// Find local connections for a user
connections := router.GetLocalConnectionsByUser("user-789")

// Resolve delivery targets
targets, _ := router.ResolveTargets(ctx, wsrouting.TargetedDelivery{
    TargetType: "user",
    TargetID:   "user-789",
})

// Unregister on disconnect
router.Unregister(ctx, "conn-123")

// Health check
health := router.Health()
fmt.Printf("Server %s has %d connections\n", health.ServerID, health.LocalConnections)
```

### Target Types

| Type | Description |
|------|-------------|
| `connection` | Specific connection by ID |
| `device` | All connections for a device |
| `user` | All connections for a user |
| `broadcast` | All local connections |

## WebSocket Metrics

The `wsmetrics` module collects connection, message, and latency metrics.

### Usage

```go
import "github.com/ovasabi/foundation/server-kit/go/wsmetrics"

// Create a collector
metrics := wsmetrics.NewCollector("server-1")

// Record connection events
metrics.RecordConnectionOpen()
metrics.RecordConnectionClose()
metrics.RecordConnectionFailed()
metrics.RecordConnectionRejected()

// Record messages
metrics.RecordMessageReceived(1024)  // bytes
metrics.RecordMessageSent(512)
metrics.RecordMessageFailed()

// Record subscriptions
metrics.RecordSubscription()
metrics.RecordUnsubscription()

// Record auth events
metrics.RecordAuthSuccess()
metrics.RecordAuthFailure()

// Record latency samples
metrics.RecordLatency(50 * time.Millisecond)

// Get snapshot
snap := metrics.Snapshot()
fmt.Printf("Active: %d, Total: %d, P99: %v\n",
    snap.ConnectionsActive,
    snap.ConnectionsTotal,
    snap.LatencyP99,
)

// Compute rates
rate := snap.MessagesPerSecond(uptime)
successRate := snap.ConnectionSuccessRate()
```

### Metrics Available

**Connections:**
- `ConnectionsActive` - Current open connections
- `ConnectionsTotal` - Lifetime connection count
- `ConnectionsFailed` - Failed connection attempts
- `ConnectionsRejected` - Rejected (rate limit, capacity)

**Messages:**
- `MessagesReceived` / `MessagesSent` - Message counts
- `MessagesFailed` - Failed sends
- `BytesReceived` / `BytesSent` - Byte counts

**Subscriptions:**
- `SubscriptionsActive` - Current subscriptions
- `SubscriptionsTotal` - Lifetime subscription count

**Authentication:**
- `AuthSuccesses` / `AuthFailures` - Auth event counts

**Latency:**
- `LatencyP50` / `LatencyP95` / `LatencyP99` - Percentiles

## Integration with WebSocket Server

The WebSocket template automatically integrates these modules:

```go
// In server.go or websocket.go

// The runtime auto-tunes on creation
ws := newWSRuntime()

// Optionally add router for horizontal scaling
redisClient, _ := redis.Connect(redisURL, "myapp", "redis")
router := wsrouting.NewRouter(redisClient, serverID)
ws.WithRouter(router)

// Get scaling info
cfg := ws.ScalingConfig()
log.Info("running with scaling tier", "tier", cfg.Tier.String())

// Get metrics for health endpoint
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
    snap := s.ws.Metrics()
    json.NewEncoder(w).Encode(snap)
}
```

## Scaling Recommendations

### Single Node (< 50K connections)

Use the default configuration. Auto-tuning handles resource allocation.

### Multi-Node (50K - 200K connections)

1. Deploy multiple server instances behind a load balancer
2. Enable Redis-backed routing for cross-instance messaging
3. Use sticky sessions at the load balancer level

```go
router := wsrouting.NewRouter(redisClient, os.Getenv("SERVER_ID"))
ws.WithRouter(router)
```

### High Scale (200K+ connections)

1. Consider dedicated WebSocket tier
2. Use connection sharding
3. Monitor metrics for bottlenecks
4. Tune system limits (file descriptors, TCP buffers)

```bash
# /etc/sysctl.conf
fs.file-max = 2097152
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
```

## Monitoring

Export metrics to your monitoring system:

```go
// Prometheus-style metrics
func collectMetrics(collector *wsmetrics.Collector) {
    snap := collector.Snapshot()

    wsConnectionsActive.Set(float64(snap.ConnectionsActive))
    wsMessagesTotal.Add(float64(snap.MessagesReceived + snap.MessagesSent))
    wsLatencyP99.Set(float64(snap.LatencyP99.Milliseconds()))
}
```

## Best Practices

1. **Auto-tune first** - Let the scaling module configure defaults
2. **Monitor early** - Enable metrics before scaling issues arise
3. **Test failure modes** - Verify behavior when Redis is unavailable
4. **Use sticky sessions** - Reduces cross-instance message routing
5. **Batch reconnects** - Use exponential backoff on client reconnection
