// Package wsmetrics provides metrics collection for WebSocket servers.
// It tracks connection counts, message rates, and latency for monitoring
// and alerting purposes.
package wsmetrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector aggregates WebSocket metrics for a server instance.
type Collector struct {
	serverID string

	// Connection metrics
	connectionsActive   atomic.Int64
	connectionsTotal    atomic.Int64
	connectionsFailed   atomic.Int64
	connectionsRejected atomic.Int64

	// Message metrics
	messagesReceived atomic.Int64
	messagesSent     atomic.Int64
	messagesFailed   atomic.Int64
	bytesReceived    atomic.Int64
	bytesSent        atomic.Int64

	// Subscription metrics
	subscriptionsActive atomic.Int64
	subscriptionsTotal  atomic.Int64

	// Authentication metrics
	authSuccesses atomic.Int64
	authFailures  atomic.Int64

	// Latency tracking (protected by mutex)
	mu                sync.RWMutex
	latencySamples    []time.Duration
	maxLatencySamples int
}

// NewCollector creates a new metrics collector.
func NewCollector(serverID string) *Collector {
	return &Collector{
		serverID:          serverID,
		latencySamples:    make([]time.Duration, 0, 1000),
		maxLatencySamples: 1000,
	}
}

// ServerID returns the server identifier.
func (c *Collector) ServerID() string {
	return c.serverID
}

// RecordConnectionOpen records a new connection being established.
func (c *Collector) RecordConnectionOpen() {
	c.connectionsActive.Add(1)
	c.connectionsTotal.Add(1)
}

// RecordConnectionClose records a connection being closed.
func (c *Collector) RecordConnectionClose() {
	c.connectionsActive.Add(-1)
}

// RecordConnectionFailed records a failed connection attempt.
func (c *Collector) RecordConnectionFailed() {
	c.connectionsFailed.Add(1)
}

// RecordConnectionRejected records a rejected connection (rate limit, capacity).
func (c *Collector) RecordConnectionRejected() {
	c.connectionsRejected.Add(1)
}

// RecordMessageReceived records an incoming message.
func (c *Collector) RecordMessageReceived(bytes int64) {
	c.messagesReceived.Add(1)
	c.bytesReceived.Add(bytes)
}

// RecordMessageSent records an outgoing message.
func (c *Collector) RecordMessageSent(bytes int64) {
	c.messagesSent.Add(1)
	c.bytesSent.Add(bytes)
}

// RecordMessageFailed records a failed message send.
func (c *Collector) RecordMessageFailed() {
	c.messagesFailed.Add(1)
}

// RecordSubscription records a new subscription.
func (c *Collector) RecordSubscription() {
	c.subscriptionsActive.Add(1)
	c.subscriptionsTotal.Add(1)
}

// RecordUnsubscription records a subscription being removed.
func (c *Collector) RecordUnsubscription() {
	c.subscriptionsActive.Add(-1)
}

// RecordAuthSuccess records a successful authentication.
func (c *Collector) RecordAuthSuccess() {
	c.authSuccesses.Add(1)
}

// RecordAuthFailure records a failed authentication.
func (c *Collector) RecordAuthFailure() {
	c.authFailures.Add(1)
}

// RecordLatency records a message processing latency sample.
func (c *Collector) RecordLatency(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Circular buffer behavior
	if len(c.latencySamples) >= c.maxLatencySamples {
		// Remove oldest sample
		c.latencySamples = c.latencySamples[1:]
	}
	c.latencySamples = append(c.latencySamples, d)
}

// Snapshot represents a point-in-time view of all metrics.
type Snapshot struct {
	ServerID  string    `json:"server_id"`
	Timestamp time.Time `json:"timestamp"`

	// Connections
	ConnectionsActive   int64 `json:"connections_active"`
	ConnectionsTotal    int64 `json:"connections_total"`
	ConnectionsFailed   int64 `json:"connections_failed"`
	ConnectionsRejected int64 `json:"connections_rejected"`

	// Messages
	MessagesReceived int64 `json:"messages_received"`
	MessagesSent     int64 `json:"messages_sent"`
	MessagesFailed   int64 `json:"messages_failed"`
	BytesReceived    int64 `json:"bytes_received"`
	BytesSent        int64 `json:"bytes_sent"`

	// Subscriptions
	SubscriptionsActive int64 `json:"subscriptions_active"`
	SubscriptionsTotal  int64 `json:"subscriptions_total"`

	// Authentication
	AuthSuccesses int64 `json:"auth_successes"`
	AuthFailures  int64 `json:"auth_failures"`

	// Latency (computed from samples)
	LatencyP50 time.Duration `json:"latency_p50"`
	LatencyP95 time.Duration `json:"latency_p95"`
	LatencyP99 time.Duration `json:"latency_p99"`
}

// Snapshot returns a point-in-time view of all metrics.
func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	latencies := make([]time.Duration, len(c.latencySamples))
	copy(latencies, c.latencySamples)
	c.mu.RUnlock()

	p50, p95, p99 := calculatePercentiles(latencies)

	return Snapshot{
		ServerID:  c.serverID,
		Timestamp: time.Now().UTC(),

		ConnectionsActive:   c.connectionsActive.Load(),
		ConnectionsTotal:    c.connectionsTotal.Load(),
		ConnectionsFailed:   c.connectionsFailed.Load(),
		ConnectionsRejected: c.connectionsRejected.Load(),

		MessagesReceived: c.messagesReceived.Load(),
		MessagesSent:     c.messagesSent.Load(),
		MessagesFailed:   c.messagesFailed.Load(),
		BytesReceived:    c.bytesReceived.Load(),
		BytesSent:        c.bytesSent.Load(),

		SubscriptionsActive: c.subscriptionsActive.Load(),
		SubscriptionsTotal:  c.subscriptionsTotal.Load(),

		AuthSuccesses: c.authSuccesses.Load(),
		AuthFailures:  c.authFailures.Load(),

		LatencyP50: p50,
		LatencyP95: p95,
		LatencyP99: p99,
	}
}

// Reset clears all metrics. Useful for testing.
func (c *Collector) Reset() {
	c.connectionsActive.Store(0)
	c.connectionsTotal.Store(0)
	c.connectionsFailed.Store(0)
	c.connectionsRejected.Store(0)
	c.messagesReceived.Store(0)
	c.messagesSent.Store(0)
	c.messagesFailed.Store(0)
	c.bytesReceived.Store(0)
	c.bytesSent.Store(0)
	c.subscriptionsActive.Store(0)
	c.subscriptionsTotal.Store(0)
	c.authSuccesses.Store(0)
	c.authFailures.Store(0)

	c.mu.Lock()
	c.latencySamples = c.latencySamples[:0]
	c.mu.Unlock()
}

// calculatePercentiles computes p50, p95, p99 from a slice of durations.
// The slice is sorted in place.
func calculatePercentiles(samples []time.Duration) (p50, p95, p99 time.Duration) {
	n := len(samples)
	if n == 0 {
		return 0, 0, 0
	}

	// Sort samples using insertion sort for small arrays, or quicksort-style for larger
	sortDurations(samples)

	p50 = samples[percentileIndex(n, 50)]
	p95 = samples[percentileIndex(n, 95)]
	p99 = samples[percentileIndex(n, 99)]

	return p50, p95, p99
}

func percentileIndex(n, percentile int) int {
	if n <= 0 {
		return 0
	}
	idx := ((n * percentile) + 99) / 100
	idx--
	if idx < 0 {
		return 0
	}
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// sortDurations sorts a slice of durations in ascending order.
func sortDurations(samples []time.Duration) {
	n := len(samples)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && samples[j-1] > samples[j]; j-- {
			samples[j-1], samples[j] = samples[j], samples[j-1]
		}
	}
}

// ConnectionsActive returns the current active connection count.
func (c *Collector) ConnectionsActive() int64 {
	return c.connectionsActive.Load()
}

// MessagesPerSecond computes approximate message rate.
// This is a simple approximation based on total messages and uptime.
func (s Snapshot) MessagesPerSecond(uptime time.Duration) float64 {
	if uptime <= 0 {
		return 0
	}
	total := float64(s.MessagesReceived + s.MessagesSent)
	return total / uptime.Seconds()
}

// BytesPerSecond computes approximate byte rate.
func (s Snapshot) BytesPerSecond(uptime time.Duration) float64 {
	if uptime <= 0 {
		return 0
	}
	total := float64(s.BytesReceived + s.BytesSent)
	return total / uptime.Seconds()
}

// ConnectionSuccessRate returns the ratio of successful connections.
func (s Snapshot) ConnectionSuccessRate() float64 {
	total := s.ConnectionsTotal + s.ConnectionsFailed + s.ConnectionsRejected
	if total == 0 {
		return 1.0
	}
	return float64(s.ConnectionsTotal) / float64(total)
}

// AuthSuccessRate returns the ratio of successful authentications.
func (s Snapshot) AuthSuccessRate() float64 {
	total := s.AuthSuccesses + s.AuthFailures
	if total == 0 {
		return 1.0
	}
	return float64(s.AuthSuccesses) / float64(total)
}
