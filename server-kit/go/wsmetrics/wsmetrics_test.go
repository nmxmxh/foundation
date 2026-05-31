package wsmetrics

import (
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector("server-1")
	if c.ServerID() != "server-1" {
		t.Errorf("ServerID() = %q, want %q", c.ServerID(), "server-1")
	}
}

func TestConnectionMetrics(t *testing.T) {
	c := NewCollector("test")

	c.RecordConnectionOpen()
	c.RecordConnectionOpen()
	c.RecordConnectionClose()

	if c.ConnectionsActive() != 1 {
		t.Errorf("ConnectionsActive() = %d, want 1", c.ConnectionsActive())
	}

	snap := c.Snapshot()
	if snap.ConnectionsTotal != 2 {
		t.Errorf("ConnectionsTotal = %d, want 2", snap.ConnectionsTotal)
	}
	if snap.ConnectionsActive != 1 {
		t.Errorf("ConnectionsActive = %d, want 1", snap.ConnectionsActive)
	}
}

func TestConnectionFailedAndRejected(t *testing.T) {
	c := NewCollector("test")

	c.RecordConnectionFailed()
	c.RecordConnectionFailed()
	c.RecordConnectionRejected()

	snap := c.Snapshot()
	if snap.ConnectionsFailed != 2 {
		t.Errorf("ConnectionsFailed = %d, want 2", snap.ConnectionsFailed)
	}
	if snap.ConnectionsRejected != 1 {
		t.Errorf("ConnectionsRejected = %d, want 1", snap.ConnectionsRejected)
	}
}

func TestMessageMetrics(t *testing.T) {
	c := NewCollector("test")

	c.RecordMessageReceived(100)
	c.RecordMessageReceived(200)
	c.RecordMessageSent(150)
	c.RecordMessageFailed()

	snap := c.Snapshot()
	if snap.MessagesReceived != 2 {
		t.Errorf("MessagesReceived = %d, want 2", snap.MessagesReceived)
	}
	if snap.MessagesSent != 1 {
		t.Errorf("MessagesSent = %d, want 1", snap.MessagesSent)
	}
	if snap.MessagesFailed != 1 {
		t.Errorf("MessagesFailed = %d, want 1", snap.MessagesFailed)
	}
	if snap.BytesReceived != 300 {
		t.Errorf("BytesReceived = %d, want 300", snap.BytesReceived)
	}
	if snap.BytesSent != 150 {
		t.Errorf("BytesSent = %d, want 150", snap.BytesSent)
	}
}

func TestSubscriptionMetrics(t *testing.T) {
	c := NewCollector("test")

	c.RecordSubscription()
	c.RecordSubscription()
	c.RecordSubscription()
	c.RecordUnsubscription()

	snap := c.Snapshot()
	if snap.SubscriptionsActive != 2 {
		t.Errorf("SubscriptionsActive = %d, want 2", snap.SubscriptionsActive)
	}
	if snap.SubscriptionsTotal != 3 {
		t.Errorf("SubscriptionsTotal = %d, want 3", snap.SubscriptionsTotal)
	}
}

func TestAuthMetrics(t *testing.T) {
	c := NewCollector("test")

	c.RecordAuthSuccess()
	c.RecordAuthSuccess()
	c.RecordAuthFailure()

	snap := c.Snapshot()
	if snap.AuthSuccesses != 2 {
		t.Errorf("AuthSuccesses = %d, want 2", snap.AuthSuccesses)
	}
	if snap.AuthFailures != 1 {
		t.Errorf("AuthFailures = %d, want 1", snap.AuthFailures)
	}
}

func TestLatencyMetrics(t *testing.T) {
	c := NewCollector("test")

	// Add some latency samples
	for i := 1; i <= 100; i++ {
		c.RecordLatency(time.Duration(i) * time.Millisecond)
	}

	snap := c.Snapshot()

	// P50 should be around 50ms
	if snap.LatencyP50 < 45*time.Millisecond || snap.LatencyP50 > 55*time.Millisecond {
		t.Errorf("LatencyP50 = %v, want ~50ms", snap.LatencyP50)
	}

	// P95 should be around 95ms
	if snap.LatencyP95 < 90*time.Millisecond || snap.LatencyP95 > 100*time.Millisecond {
		t.Errorf("LatencyP95 = %v, want ~95ms", snap.LatencyP95)
	}

	// P99 should be around 99ms
	if snap.LatencyP99 < 95*time.Millisecond || snap.LatencyP99 > 100*time.Millisecond {
		t.Errorf("LatencyP99 = %v, want ~99ms", snap.LatencyP99)
	}
}

func TestLatencyPercentilesUseConservativeNearestRank(t *testing.T) {
	samples := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		100 * time.Millisecond,
	}

	p50, p95, p99 := calculatePercentiles(samples)
	if p50 != 3*time.Millisecond {
		t.Fatalf("p50 = %s, want 3ms", p50)
	}
	if p95 != 100*time.Millisecond {
		t.Fatalf("p95 = %s, want 100ms", p95)
	}
	if p99 != 100*time.Millisecond {
		t.Fatalf("p99 = %s, want 100ms", p99)
	}
}

func TestLatencyCircularBuffer(t *testing.T) {
	c := NewCollector("test")
	c.maxLatencySamples = 10

	// Add more samples than buffer size
	for i := 1; i <= 20; i++ {
		c.RecordLatency(time.Duration(i) * time.Millisecond)
	}

	c.mu.RLock()
	samples := len(c.latencySamples)
	c.mu.RUnlock()

	if samples != 10 {
		t.Errorf("latencySamples length = %d, want 10", samples)
	}
}

func TestReset(t *testing.T) {
	c := NewCollector("test")

	c.RecordConnectionOpen()
	c.RecordMessageReceived(100)
	c.RecordLatency(10 * time.Millisecond)

	c.Reset()

	snap := c.Snapshot()
	if snap.ConnectionsActive != 0 {
		t.Errorf("ConnectionsActive after reset = %d, want 0", snap.ConnectionsActive)
	}
	if snap.MessagesReceived != 0 {
		t.Errorf("MessagesReceived after reset = %d, want 0", snap.MessagesReceived)
	}
}

func TestMessagesPerSecond(t *testing.T) {
	snap := Snapshot{
		MessagesReceived: 500,
		MessagesSent:     500,
	}

	rate := snap.MessagesPerSecond(10 * time.Second)
	if rate != 100 {
		t.Errorf("MessagesPerSecond = %f, want 100", rate)
	}
}

func TestBytesPerSecond(t *testing.T) {
	snap := Snapshot{
		BytesReceived: 5000,
		BytesSent:     5000,
	}

	rate := snap.BytesPerSecond(10 * time.Second)
	if rate != 1000 {
		t.Errorf("BytesPerSecond = %f, want 1000", rate)
	}
}

func TestConnectionSuccessRate(t *testing.T) {
	snap := Snapshot{
		ConnectionsTotal:    90,
		ConnectionsFailed:   5,
		ConnectionsRejected: 5,
	}

	rate := snap.ConnectionSuccessRate()
	if rate != 0.9 {
		t.Errorf("ConnectionSuccessRate = %f, want 0.9", rate)
	}
}

func TestConnectionSuccessRateEmpty(t *testing.T) {
	snap := Snapshot{}
	rate := snap.ConnectionSuccessRate()
	if rate != 1.0 {
		t.Errorf("ConnectionSuccessRate for empty = %f, want 1.0", rate)
	}
}

func TestAuthSuccessRate(t *testing.T) {
	snap := Snapshot{
		AuthSuccesses: 80,
		AuthFailures:  20,
	}

	rate := snap.AuthSuccessRate()
	if rate != 0.8 {
		t.Errorf("AuthSuccessRate = %f, want 0.8", rate)
	}
}

func TestAuthSuccessRateEmpty(t *testing.T) {
	snap := Snapshot{}
	rate := snap.AuthSuccessRate()
	if rate != 1.0 {
		t.Errorf("AuthSuccessRate for empty = %f, want 1.0", rate)
	}
}

func TestSnapshotTimestamp(t *testing.T) {
	c := NewCollector("test")
	before := time.Now()
	snap := c.Snapshot()
	after := time.Now()

	if snap.Timestamp.Before(before) || snap.Timestamp.After(after) {
		t.Error("Snapshot timestamp should be between before and after")
	}
}

func TestEmptyLatencyPercentiles(t *testing.T) {
	c := NewCollector("test")
	snap := c.Snapshot()

	if snap.LatencyP50 != 0 {
		t.Errorf("LatencyP50 for empty = %v, want 0", snap.LatencyP50)
	}
	if snap.LatencyP95 != 0 {
		t.Errorf("LatencyP95 for empty = %v, want 0", snap.LatencyP95)
	}
	if snap.LatencyP99 != 0 {
		t.Errorf("LatencyP99 for empty = %v, want 0", snap.LatencyP99)
	}
}

func TestZeroUptime(t *testing.T) {
	snap := Snapshot{
		MessagesReceived: 100,
		BytesReceived:    1000,
	}

	if snap.MessagesPerSecond(0) != 0 {
		t.Error("MessagesPerSecond(0) should return 0")
	}
	if snap.BytesPerSecond(0) != 0 {
		t.Error("BytesPerSecond(0) should return 0")
	}
}
