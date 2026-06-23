package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsmetrics"
)

// TestWSMetricsReflectConnectionActivity verifies the observability contract of
// the websocket lane: when a metrics collector is configured, a full connection
// lifecycle (open, dispatch, subscribe, close) is reflected in the snapshot —
// connections counted, inbound and outbound messages tallied. This drives the
// metrics-recording paths threaded through the reader, writer, dispatch, and
// connection lifecycle, with the snapshot as the oracle.
func TestWSMetricsReflectConnectionActivity(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested":              func(context.Context, extension.Object) (any, error) { return map[string]any{"pong": true}, nil },
		"system:websocket_subscribe:v1:requested": func(context.Context, extension.Object) (any, error) { return map[string]any{}, nil },
	})
	collector := wsmetrics.NewCollector("metrics-test")
	s.ws.WithMetrics(collector)

	conn := dialWS(t, srv, "deviceId=dev_metrics")
	_ = readEnv(t, conn) // ack (a sent message)

	sendEnv(t, conn, requested("identity:ping:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:ping:v1:success" {
		t.Fatalf("ping response = %q", resp.EventType)
	}
	sendEnv(t, conn, requested("system:websocket_subscribe:v1:requested", extension.Object{"pattern": extension.String("orders:*")}))
	if resp := readEnv(t, conn); resp.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q", resp.EventType)
	}

	// Bounded wait (no fixed sleep loop beyond a short cap) for the server-side
	// counters to settle after the responses were observed by the client.
	snap := collector.Snapshot()
	deadline := time.Now().Add(time.Second)
	for (snap.ConnectionsTotal == 0 || snap.MessagesReceived < 2 || snap.MessagesSent < 3) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		snap = collector.Snapshot()
	}

	if snap.ConnectionsTotal < 1 {
		t.Fatalf("connections total = %d, want >= 1", snap.ConnectionsTotal)
	}
	if snap.MessagesReceived < 2 {
		t.Fatalf("messages received = %d, want >= 2 (ping + subscribe)", snap.MessagesReceived)
	}
	if snap.MessagesSent < 3 {
		t.Fatalf("messages sent = %d, want >= 3 (ack + 2 responses)", snap.MessagesSent)
	}

	_ = conn.Close()
	// After close the active gauge must fall back toward zero (connection cleanup).
	deadline = time.Now().Add(time.Second)
	for collector.Snapshot().ConnectionsActive > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if active := collector.Snapshot().ConnectionsActive; active != 0 {
		t.Fatalf("connections active after close = %d, want 0", active)
	}
}
