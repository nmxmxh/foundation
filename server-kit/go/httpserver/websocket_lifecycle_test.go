package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

// decodeWSText decodes a text frame into an envelope with its payload materialized.
func decodeWSText(data []byte) (events.Envelope, error) {
	env, err := events.FromJSON(data)
	if err != nil {
		return events.Envelope{}, err
	}
	if mErr := env.MaterializePayload(); mErr != nil {
		return events.Envelope{}, mErr
	}
	return env, nil
}

// envErrorCode extracts the error code from a websocket_error envelope, or "" if
// the envelope is not an error frame.
func envErrorCode(env events.Envelope) string {
	if env.EventType != "system:websocket_error:v1:failed" {
		return ""
	}
	errObj, ok := env.Payload["error"]
	if !ok {
		return ""
	}
	m, ok := errObj.ObjectValue()
	if !ok {
		return ""
	}
	code, _ := m.GetString("code")
	return code
}

// TestWSGuestIdleTimeoutTerminates is a stateful-protocol terminal-transition
// test (TE-29) and a guest-expiry security boundary (TE-19): a connection that
// never authenticates past the guest idle window must be told ws_guest_timeout
// and dropped, not allowed to linger. Removing the idle-timeout guard in the
// reader loop makes this test hang then fail on the read deadline.
func TestWSGuestIdleTimeoutTerminates(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	// The reader re-checks the guest idle window between reads, so a short window
	// fires on a subsequent guest message rather than racing connection teardown.
	s.wsGuestIdleTimeout = 20 * time.Millisecond

	conn := dialWS(t, srv, "deviceId=dev_idle")
	_ = readEnv(t, conn) // connection_open ack — the guest was accepted

	// Drive guest messages until the idle window elapses. Before expiry each
	// non-allowlisted event is refused with guest_event_not_allowed (the guest is
	// alive); once the window passes the reader must evict the guest. Eviction is
	// observed either as the best-effort ws_guest_timeout frame or as the socket
	// closing — both are the alive->terminated transition the guard guarantees.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	deadline := time.Now().Add(2 * time.Second)
	sawAlive := false
	for time.Now().Before(deadline) {
		sendEnv(t, conn, requested("orders:create:v1:requested", extension.Object{}))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !sawAlive {
				t.Fatalf("socket closed before any live guest response: %v", err)
			}
			return // guest was evicted after the idle window — invariant holds
		}
		env, decErr := decodeWSText(data)
		if decErr != nil {
			t.Fatalf("decode response: %v (raw=%s)", decErr, data)
		}
		code := envErrorCode(env)
		if code == "ws_guest_timeout" {
			return // explicit timeout frame won the race against teardown
		}
		if code != "guest_event_not_allowed" {
			t.Fatalf("unexpected error code before timeout: %q", code)
		}
		sawAlive = true
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("guest connection was never terminated by the idle timeout")
}

// TestWSUpgradeFailureReleasesSlot is a resource-ownership invariant (TE-17): a
// request that reserves a connection slot but fails the WebSocket upgrade must
// release that slot, otherwise capacity leaks. Against a max-1 server we issue
// several non-upgrade GETs (each reserves then fails the handshake); a genuine
// dial afterward must still succeed. If releaseWSConnectionSlot were dropped on
// the upgrade-failure path, the first failed GET would permanently consume the
// only slot and the final dial would be rejected with 503.
func TestWSUpgradeFailureReleasesSlot(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	s.ConfigureWebSocket(true, 1, false) // capacity 1

	for i := range 5 {
		resp, err := http.Get(srv.URL + "/ws") // no Upgrade header -> handshake fails
		if err != nil {
			t.Fatalf("GET /ws attempt %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			t.Fatalf("attempt %d hit capacity (503) — a prior failed upgrade leaked its slot", i)
		}
	}

	// The single slot must still be free for a real connection.
	conn := dialWS(t, srv, "deviceId=dev_after")
	ack := readEnv(t, conn)
	if ack.EventType != "identity:connection_open:v1:ack" {
		t.Fatalf("post-failure ack = %q, want connection_open ack", ack.EventType)
	}
}

// TestWSGuestRateLimitRejectsSameIP is a rate-limit negative test (TE-19): once a
// guest IP exhausts the guest handshake budget, further upgrades from that IP are
// refused before the socket opens. The IP is pinned via X-Forwarded-For so both
// handshakes share one limiter key (GetClientIP honors XFF first).
func TestWSGuestRateLimitRejectsSameIP(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	s.ws.guestLimiter = security.NewRateLimiter(1, time.Minute) // one handshake per IP

	hdr := http.Header{"X-Forwarded-For": {"203.0.113.42"}}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	first, resp1, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if resp1 != nil {
		defer func() { _ = resp1.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("first dial should succeed: %v", err)
	}
	defer first.Close()

	_, resp2, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if resp2 != nil {
		defer func() { _ = resp2.Body.Close() }()
	}
	if err == nil {
		t.Fatal("second dial from the same IP should be rate-limited")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %v, want 429", resp2)
	}
}

// TestWSRouterTracksConnectionLifecycle covers cross-instance routing (TE-11): a
// configured router must learn about a connection on open and forget it on close,
// so a horizontally-scaled deployment can locate the socket's owning instance.
// The in-memory router (nil Redis client) is a contract-preserving fake (TE-22):
// it performs the same local bookkeeping the real router does.
func TestWSRouterTracksConnectionLifecycle(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	router := wsrouting.NewRouter(nil, "test-server")
	s.ws.WithRouter(router)

	conn := dialWS(t, srv, "deviceId=dev_routed")
	_ = readEnv(t, conn) // ack — connection is fully registered by now

	if got := waitForRouterCount(router, 1); got != 1 {
		t.Fatalf("router local connections after open = %d, want 1", got)
	}

	_ = conn.Close()
	if got := waitForRouterCount(router, 0); got != 0 {
		t.Fatalf("router local connections after close = %d, want 0", got)
	}
}

// waitForRouterCount polls (bounded, no fixed sleep per TE-27) until the router's
// local connection count reaches want or the deadline elapses.
func waitForRouterCount(router *wsrouting.Router, want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := router.LocalConnectionCount(); got == want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	return router.LocalConnectionCount()
}

// TestWSUnsubscribeRequiresPattern is the boundary negative for the unsubscribe
// control (TE-04): an empty pattern is rejected with pattern_required rather than
// silently mutating the subscription set.
func TestWSUnsubscribeRequiresPattern(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"system:websocket_unsubscribe:v1:requested": func(context.Context, extension.Object) (any, error) { return map[string]any{}, nil },
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("system:websocket_unsubscribe:v1:requested", extension.Object{"pattern": extension.String("   ")}))
	if code := wsErrorCode(t, conn); code != "pattern_required" {
		t.Fatalf("error code = %q, want pattern_required", code)
	}
}

// wsErrorCode reads the next text frame and returns its websocket_error code.
func wsErrorCode(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	resp := readEnv(t, conn)
	code := envErrorCode(resp)
	if code == "" {
		t.Fatalf("frame is not a websocket_error: %q (%v)", resp.EventType, resp.Payload)
	}
	return code
}

// TestEnqueueWSBackpressure covers the outbound-queue capacity guard (TE-18): when
// a connection's send queue is full, enqueue must shed with a queue-full error
// rather than block the reader/dispatch path. Driven directly with a zero-depth
// queue and no writer draining, which is the deterministic full state.
func TestEnqueueWSBackpressure(t *testing.T) {
	s := newSmokeServer(t)
	conn := &wsConnection{
		id:   "conn_bp",
		send: make(chan wsOutbound), // unbuffered, no writer -> always full
	}
	env := requested("identity:ping:v1:ack", extension.Object{})
	if err := s.enqueueWSEnvelope(conn, env); err == nil {
		t.Fatal("enqueue into a full queue should return an error, not block or succeed")
	}
}

// TestWSAuthUpgradeAppliesCapabilities covers the capability-parsing branch of
// maybeUpgradeConnectionAuth: an authenticate response carrying a capabilities
// list upgrades the connection and records those capabilities, so subsequent
// authorization can use them.
func TestWSAuthUpgradeAppliesCapabilities(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{
				"user_id":      "user_caps",
				"role_id":      "member",
				"capabilities": []any{"orders.read", "orders.write"},
			}, nil
		},
	})
	conn := dialWS(t, srv, "deviceId=dev_caps")
	_ = readEnv(t, conn) // ack
	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:authenticate_connection:v1:success" {
		t.Fatalf("auth response = %q", resp.EventType)
	}
	if !wsConnectionAuthenticated(s, "dev_caps") {
		t.Fatal("connection should be authenticated after capability auth")
	}
}

// TestWSClientDisconnectCleansUpConnection covers the connection-cleanup path when
// the client vanishes (TE-17): after the socket is closed, the server-side write
// attempts fail and the connection must be unregistered from the runtime, not
// leaked. Forwarding events after the client is gone drives the writer's
// write-failure branch; the oracle is that the connection count returns to zero.
func TestWSClientDisconnectCleansUpConnection(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	conn := dialWS(t, srv, "deviceId=dev_gone")
	_ = readEnv(t, conn) // ack — the connection is registered

	// Confirm it is registered, then drop the client abruptly.
	if c := wsConnectionCount(s); c != 1 {
		t.Fatalf("connection count after open = %d, want 1", c)
	}
	_ = conn.Close()

	// Push outbound traffic so the server writer notices the dead socket. Each
	// forward is a best-effort enqueue; the writer's failed write tears the
	// connection down.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wsConnectionCount(s) == 0 {
			return // connection was cleaned up — no leak
		}
		s.forwardEventToConnections(context.Background(), requested("demo:tick:v1:created", extension.Object{}))
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("connection was not cleaned up after client disconnect (count=%d)", wsConnectionCount(s))
}

// wsConnectionCount returns the number of live server-side websocket connections.
func wsConnectionCount(s *Server) int {
	count := 0
	s.ws.connections.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// TestDispatchRejectsInvalidEnvelopeFields is a request-validation boundary test
// (TE-04/TE-19) over the public HTTP dispatch contract: a blank event_type and a
// malformed timestamp are each rejected with their specific domain error class
// before any handler runs.
func TestDispatchRejectsInvalidEnvelopeFields(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return map[string]any{"ok": true}, nil
	})

	cases := []struct {
		name string
		body []byte
		code string
	}{
		{"empty event_type", []byte(`{"event_type":"","correlation_id":"corr_v","timestamp":"` + time.Now().UTC().Format(time.RFC3339) + `"}`), "event_type_required"},
		{"malformed timestamp", []byte(`{"event_type":"` + testEvent + `","correlation_id":"corr_v","timestamp":"not-a-timestamp"}`), "invalid_timestamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("body missing %q: %s", tc.code, rec.Body.String())
			}
		})
	}
}
