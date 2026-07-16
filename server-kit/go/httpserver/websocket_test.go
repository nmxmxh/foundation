package httpserver

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	kitcompress "github.com/nmxmxh/ovasabi_foundation/server-kit/go/compress"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsmetrics"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsHandler is a registered dispatch handler used by the WS e2e tests.
type wsHandler = bootstrap.HandlerFunc

// wsSubscriptionCount returns the number of subscription patterns recorded on the
// server-side connection for the given device id, or -1 if no such connection.
func wsSubscriptionCount(s *Server, deviceID string) int {
	count := -1
	s.ws.connections.Range(func(_, value any) bool {
		conn, ok := value.(*wsConnection)
		if !ok || conn.deviceID != deviceID {
			return true
		}
		conn.mu.RLock()
		count = len(conn.subscriptions)
		conn.mu.RUnlock()
		return false
	})
	return count
}

// wsConnectionAuthenticated reports whether the server-side connection for the
// given device id has been upgraded to authenticated.
func wsConnectionAuthenticated(s *Server, deviceID string) bool {
	authed := false
	s.ws.connections.Range(func(_, value any) bool {
		conn, ok := value.(*wsConnection)
		if !ok || conn.deviceID != deviceID {
			return true
		}
		authed = conn.isAuthenticated()
		return false
	})
	return authed
}

// userMetadata builds an envelope metadata object addressed to a user/device,
// matching what forwardEventToConnections reads for targeting.
func userMetadata(userID, deviceID string) extension.Object {
	md := metadata.New()
	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}
	md.GlobalContext.UserID = userID
	md.GlobalContext.DeviceID = deviceID
	return md.ToObject()
}

// newWSTestServer assembles a server with the given guest-allowed handlers, mounts
// it under httptest, and returns the live server plus the *Server for assertions.
// Each handler's event is added to the unauthenticated allowset so a guest socket
// can drive it without a JWT.
func newWSTestServer(t *testing.T, handlers map[string]wsHandler) (*httptest.Server, *Server) {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("ws-test"))
	reg := registry.New(nil, gh, log)
	for eventType, h := range handlers {
		if regErr := reg.Register(eventType, h); regErr != nil {
			t.Fatalf("register %s: %v", eventType, regErr)
		}
	}
	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
	for eventType := range handlers {
		s.AddUnauthenticatedWSEvent(eventType)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, s
}

// dialWS opens a websocket to the test server's /ws endpoint with optional query.
func dialWS(t *testing.T, srv *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	if query != "" {
		wsURL += "?" + query
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return conn
}

// readEnv reads the next text frame and decodes it as an events.Envelope. Ping
// control frames are handled by gorilla transparently; only data frames surface.
func readEnv(t *testing.T, conn *websocket.Conn) events.Envelope {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	env, err := events.FromJSON(data)
	if err != nil {
		t.Fatalf("decode envelope: %v (raw=%s)", err, data)
	}
	if err := env.MaterializePayload(); err != nil {
		t.Fatalf("materialize payload: %v (raw=%s)", err, data)
	}
	return env
}

// sendEnv writes an envelope as a JSON text frame.
func sendEnv(t *testing.T, conn *websocket.Conn, env events.Envelope) {
	t.Helper()
	raw, err := env.ToJSON()
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

func requested(eventType string, payload extension.Object) events.Envelope {
	return events.Envelope{
		EventType:     eventType,
		Payload:       payload,
		CorrelationID: "corr_ws",
		Timestamp:     time.Now().UTC(),
		SchemaVersion: events.EnvelopeSchemaVersion,
	}
}

// TestWSConnectionAck verifies the server emits the connection-open ack as the
// first frame after a successful upgrade.
func TestWSConnectionAck(t *testing.T) {
	srv, _ := newWSTestServer(t, nil)
	conn := dialWS(t, srv, "deviceId=dev_1")
	ack := readEnv(t, conn)
	if ack.EventType != "identity:connection_open:v1:ack" {
		t.Fatalf("first frame = %q, want connection_open ack", ack.EventType)
	}
	if cid, _ := ack.Payload.GetString("connection_id"); cid == "" {
		t.Fatalf("ack missing connection_id: %v", ack.Payload)
	}
	if state, _ := ack.Payload.GetString("state"); state != "guest" {
		t.Fatalf("ack state = %q, want guest", state)
	}
}

// TestWSGuestDispatch drives a guest-allowed event end to end and asserts the
// success terminal envelope carries the handler's result.
func TestWSGuestDispatch(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("identity:ping:v1:requested", extension.Object{}))
	resp := readEnv(t, conn)
	if resp.EventType != "identity:ping:v1:success" {
		t.Fatalf("response = %q, want :success", resp.EventType)
	}
	if pong, _ := resp.Payload.GetBool("pong"); !pong {
		t.Fatalf("handler result not echoed: %v", resp.Payload)
	}
	if resp.CorrelationID != "corr_ws" {
		t.Fatalf("correlation not preserved: %q", resp.CorrelationID)
	}
}

// TestWSGuestEventNotAllowed asserts a guest sending a non-allowlisted event is
// rejected with a websocket_error (guest_event_not_allowed), not dispatched.
func TestWSGuestEventNotAllowed(t *testing.T) {
	srv, _ := newWSTestServer(t, nil) // nothing allowlisted
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("orders:create:v1:requested", extension.Object{}))
	resp := readEnv(t, conn)
	if resp.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("response = %q, want websocket_error", resp.EventType)
	}
	var code string
	if errObj, ok := resp.Payload["error"]; ok {
		if m, ok := errObj.ObjectValue(); ok {
			code, _ = m.GetString("code")
		}
	}
	if code != "guest_event_not_allowed" {
		t.Fatalf("error code = %q, want guest_event_not_allowed (payload=%v)", code, resp.Payload)
	}
}

// TestWSAuthRequiredRejectsGuest asserts that when wsAuthRequired is on, an
// unauthenticated socket sending a non-allowlisted event gets auth_required.
func TestWSAuthRequiredRejectsGuest(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	s.ConfigureWebSocket(true, 100, true) // authRequired = true
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("orders:create:v1:requested", extension.Object{}))
	resp := readEnv(t, conn)
	if resp.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("response = %q, want websocket_error", resp.EventType)
	}
	var code string
	if errObj, ok := resp.Payload["error"]; ok {
		if m, ok := errObj.ObjectValue(); ok {
			code, _ = m.GetString("code")
		}
	}
	if code != "auth_required" {
		t.Fatalf("error code = %q, want auth_required", code)
	}
}

// TestWSSubscribeUnsubscribe exercises the subscription lifecycle: a subscribe
// request acks success and records the pattern; unsubscribe removes it.
func TestWSSubscribeUnsubscribe(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"system:websocket_subscribe:v1:requested":   func(_ context.Context, _ extension.Object) (any, error) { return map[string]any{}, nil },
		"system:websocket_unsubscribe:v1:requested": func(_ context.Context, _ extension.Object) (any, error) { return map[string]any{}, nil },
	})
	conn := dialWS(t, srv, "deviceId=dev_sub")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("system:websocket_subscribe:v1:requested", extension.Object{"pattern": extension.String("orders:*")}))
	resp := readEnv(t, conn)
	if resp.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q, want :success", resp.EventType)
	}
	if pattern, _ := resp.Payload.GetString("pattern"); pattern != "orders:*" {
		t.Fatalf("subscribe pattern = %q", pattern)
	}

	// The server-side connection should now carry the subscription.
	if got := wsSubscriptionCount(s, "dev_sub"); got == 0 {
		t.Fatal("subscription not recorded on connection")
	}

	sendEnv(t, conn, requested("system:websocket_unsubscribe:v1:requested", extension.Object{"pattern": extension.String("orders:*")}))
	resp = readEnv(t, conn)
	if resp.EventType != "system:websocket_unsubscribe:v1:success" {
		t.Fatalf("unsubscribe response = %q, want :success", resp.EventType)
	}
}

// TestWSSubscribeRequiresPattern asserts an empty subscription pattern is
// rejected with a validation error rather than silently accepted.
func TestWSSubscribeRequiresPattern(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"system:websocket_subscribe:v1:requested": func(_ context.Context, _ extension.Object) (any, error) { return map[string]any{}, nil },
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("system:websocket_subscribe:v1:requested", extension.Object{"pattern": extension.String("   ")}))
	resp := readEnv(t, conn)
	if resp.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("response = %q, want websocket_error", resp.EventType)
	}
}

// TestWSAuthUpgradeAndEventForwarding authenticates the connection via a handler
// that returns identity, then verifies a bus event addressed to that user is
// forwarded to the subscribed socket.
func TestWSAuthUpgradeAndEventForwarding(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"user_id": "user_42", "organization_id": "org_9", "role_id": "admin"}, nil
		},
		"system:websocket_subscribe:v1:requested": func(_ context.Context, _ extension.Object) (any, error) { return map[string]any{}, nil },
	})
	conn := dialWS(t, srv, "deviceId=dev_auth")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	authResp := readEnv(t, conn)
	if authResp.EventType != "identity:authenticate_connection:v1:success" {
		t.Fatalf("auth response = %q", authResp.EventType)
	}
	// Connection should now be authenticated server-side.
	if !wsConnectionAuthenticated(s, "dev_auth") {
		t.Fatal("connection not upgraded to authenticated")
	}

	// Subscribe to a pattern, then forward a matching bus event to this user.
	sendEnv(t, conn, requested("system:websocket_subscribe:v1:requested", extension.Object{"pattern": extension.String("billing:*")}))
	subResp := readEnv(t, conn)
	if subResp.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q", subResp.EventType)
	}

	s.forwardEventToConnections(context.Background(), events.Envelope{
		EventType:     "billing:invoice:v1:created",
		Payload:       extension.Object{"amount": extension.Int(100)},
		Metadata:      userMetadata("user_42", "dev_auth"),
		CorrelationID: "corr_fwd",
		Timestamp:     time.Now().UTC(),
		SchemaVersion: events.EnvelopeSchemaVersion,
	})
	fwd := readEnv(t, conn)
	if fwd.EventType != "billing:invoice:v1:created" {
		t.Fatalf("forwarded event = %q, want billing:invoice", fwd.EventType)
	}
}

// TestWSBinaryFormatRoundTrip drives the binary lane: a binary-format socket
// sends a binary envelope and receives binary responses.
func TestWSBinaryFormatRoundTrip(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	conn := dialWS(t, srv, "format=binary")
	// Ack arrives as a binary frame.
	mt, _, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("ack frame type = %d, want binary", mt)
	}

	raw, err := requested("identity:ping:v1:requested", extension.Object{}).ToBinary()
	if err != nil {
		t.Fatalf("encode binary: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read binary response: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("response frame type = %d, want binary", mt)
	}
	env, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("decode binary response: %v", err)
	}
	if env.EventType != "identity:ping:v1:success" {
		t.Fatalf("binary response = %q", env.EventType)
	}
}

// TestWSInvalidEnvelopeRejected asserts a malformed text frame yields an
// invalid_envelope error rather than closing the socket.
func TestWSInvalidEnvelopeRejected(t *testing.T) {
	srv, _ := newWSTestServer(t, nil)
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not an envelope")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := readEnv(t, conn)
	if resp.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("response = %q, want websocket_error", resp.EventType)
	}
}

// TestWSDisabledReturns404 asserts the endpoint 404s when WS is disabled.
func TestWSDisabledReturns404(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	s.ConfigureWebSocket(false, 100, false)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected dial to fail when ws disabled")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %v, want 404", resp)
	}
}

// TestWSCapacityRejectsBeyondLimit asserts a second connection is rejected once
// the (1) connection slot is taken.
func TestWSCapacityRejectsBeyondLimit(t *testing.T) {
	srv, s := newWSTestServer(t, nil)
	s.ConfigureWebSocket(true, 1, false) // max 1 connection
	first := dialWS(t, srv, "deviceId=first")
	_ = readEnv(t, first) // ack — keeps the slot held

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?deviceId=second"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected second dial to be rejected at capacity")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}
}

// TestWSDispatchHandlerError asserts a handler returning a domain error produces
// a :failed terminal envelope carrying the error (buildWSDispatchErrorEnvelope).
func TestWSDispatchHandlerError(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return nil, domainerr.Validation("bad_ping", "ping was rejected")
		},
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("identity:ping:v1:requested", extension.Object{}))
	resp := readEnv(t, conn)
	if resp.EventType != "identity:ping:v1:failed" {
		t.Fatalf("response = %q, want :failed", resp.EventType)
	}
}

// TestWSLogoutClearsAuth authenticates then logs out, verifying clearAuth runs
// and the connection drops back to guest state.
func TestWSLogoutClearsAuth(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"user_id": "u_logout"}, nil
		},
		"identity:logout_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{}, nil
		},
	})
	conn := dialWS(t, srv, "deviceId=dev_logout")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	_ = readEnv(t, conn) // auth success
	if !wsConnectionAuthenticated(s, "dev_logout") {
		t.Fatal("expected authenticated after auth event")
	}

	sendEnv(t, conn, requested("identity:logout_connection:v1:requested", extension.Object{}))
	_ = readEnv(t, conn) // logout response
	if wsConnectionAuthenticated(s, "dev_logout") {
		t.Fatal("expected guest state after logout")
	}
}

// TestWSAuthUpgradeNoUserIDIsIgnored asserts an auth response without a user_id
// does not upgrade the connection (the auth-failure branch).
func TestWSAuthUpgradeNoUserIDIsIgnored(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"organization_id": "org_only"}, nil // no user_id
		},
	})
	conn := dialWS(t, srv, "deviceId=dev_noupg")
	_ = readEnv(t, conn) // ack
	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	_ = readEnv(t, conn)
	if wsConnectionAuthenticated(s, "dev_noupg") {
		t.Fatal("connection should not upgrade without a user_id")
	}
}

// TestWSWriterSendsPing drives the writer's ping ticker by shrinking the ping
// interval, and confirms the client receives a ping control frame.
func TestWSWriterSendsPing(t *testing.T) {
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("ws-ping"))
	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, registry.New(nil, gh, log), gh)
	s.wsPingInterval = 40 * time.Millisecond
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv, "")
	pinged := make(chan struct{}, 1)
	conn.SetPingHandler(func(string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}
		return nil
	})
	_ = readEnv(t, conn) // ack
	// Read in a loop to let control frames be processed.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	go func() { _, _, _ = conn.ReadMessage() }()
	select {
	case <-pinged:
	case <-time.After(time.Second):
		t.Fatal("expected a ping control frame from the writer ticker")
	}
}

// TestWSBinaryInvalidEnvelopeRejected covers decodeWSEnvelope's binary failure
// branch: a binary frame that is not a valid envelope yields invalid_envelope.
func TestWSBinaryInvalidEnvelopeRejected(t *testing.T) {
	srv, _ := newWSTestServer(t, nil)
	conn := dialWS(t, srv, "format=binary")
	_, _, _ = conn.ReadMessage() // ack (binary)

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write: %v", err)
	}
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("frame type = %d, want binary", mt)
	}
	env, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("response = %q, want websocket_error", env.EventType)
	}
}

// TestWSRuntimeAccessors covers the small runtime configuration/observability
// surface: Metrics, ScalingConfig, WithRouter, WithMetrics.
func TestWSRuntimeAccessors(t *testing.T) {
	rt := newWSRuntime()
	if rt.Metrics() == nil {
		t.Fatal("Metrics() nil")
	}
	if rt.ScalingConfig() == nil {
		t.Fatal("ScalingConfig() nil")
	}
	rt.WithRouter(nil)  // no-op, must not panic
	rt.WithMetrics(nil) // nil collector ignored
	custom := wsmetrics.NewCollector("custom")
	rt.WithMetrics(custom)
	if rt.metrics != custom {
		t.Fatal("WithMetrics did not apply custom collector")
	}
	// Nil-safe behavior on the zero runtime.
	var nilRT *wsRuntime
	if nilRT.Metrics() != nil || nilRT.ScalingConfig() != nil {
		t.Fatal("nil runtime accessors should be nil-safe")
	}
}

// TestWSBusForwardingAndRecentEvents covers the bus-driven path: a subscribed
// guest receives events published to the in-memory bus (ensureEventSubscription
// + forwardEventToConnections), and /v1/events/recent reflects the publication.
func TestWSBusForwardingAndRecentEvents(t *testing.T) {
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	bus := events.NewInMemoryBus(50)
	gh := graceful.NewHandler(
		graceful.WithLogger(log),
		graceful.WithService("ws-bus"),
		graceful.WithEventEmitter(graceful.NewInMemoryEventEmitter(bus)),
	)
	reg := registry.New(nil, gh, log)
	if regErr := reg.Register("system:websocket_subscribe:v1:requested",
		func(_ context.Context, _ extension.Object) (any, error) { return map[string]any{}, nil }); regErr != nil {
		t.Fatalf("register: %v", regErr)
	}
	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
	s.AddUnauthenticatedWSEvent("system:websocket_subscribe:v1:requested")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv, "deviceId=dev_bus")
	_ = readEnv(t, conn) // ack
	sendEnv(t, conn, requested("system:websocket_subscribe:v1:requested", extension.Object{"pattern": extension.String("demo:*")}))
	if sub := readEnv(t, conn); sub.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q", sub.EventType)
	}

	// Publish a matching event to the bus; the server's "*" subscription should
	// forward it to the subscribed connection.
	if pubErr := bus.Publish(context.Background(), events.Envelope{
		EventType:     "demo:thing:v1:success",
		Payload:       extension.Object{"hello": extension.String("world")},
		CorrelationID: "corr_bus",
		Timestamp:     time.Now().UTC(),
		SchemaVersion: events.EnvelopeSchemaVersion,
	}); pubErr != nil {
		t.Fatalf("publish: %v", pubErr)
	}
	fwd := readEnv(t, conn)
	if fwd.EventType != "demo:thing:v1:success" {
		t.Fatalf("forwarded event = %q", fwd.EventType)
	}

	// /v1/events/recent should report the published event.
	resp, err := http.Get(srv.URL + "/v1/events/recent")
	if err != nil {
		t.Fatalf("recent events GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recent events status = %d", resp.StatusCode)
	}
}

// TestWSCompressedBinaryEnvelopeParity is a binary-lane parity test (TE-11): when
// WS compression is enabled, a compressed binary envelope must decode to the same
// request — and produce the same terminal :success — as the uncompressed binary
// frame. This exercises decodeWSEnvelope's decompress-then-decode fallback.
func TestWSCompressedBinaryEnvelopeParity(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	s.wsCompressionEnabled = true

	conn := dialWS(t, srv, "format=binary")
	if _, _, err := conn.ReadMessage(); err != nil { // binary ack
		t.Fatalf("read ack: %v", err)
	}

	bin, err := requested("identity:ping:v1:requested", extension.Object{}).ToBinary()
	if err != nil {
		t.Fatalf("ToBinary: %v", err)
	}
	compressed, err := kitcompress.CompressGzip(bin, 6)
	if err != nil {
		t.Fatalf("CompressGzip: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, compressed); err != nil {
		t.Fatalf("write compressed: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	env, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.EventType != "identity:ping:v1:success" {
		t.Fatalf("response = %q, want :success — compressed lane did not reach the handler", env.EventType)
	}
}

// TestRegisterWSConnectionEnforcesCapacityOnRegister covers the capacity guard on
// the register path for a connection that did not pre-reserve a slot (TE-18 hard
// bound). With the slot count already at the max, registering an unreserved
// connection must be refused rather than overcommitting the server.
func TestRegisterWSConnectionEnforcesCapacityOnRegister(t *testing.T) {
	s := newSmokeServer(t)
	s.ConfigureWebSocket(true, 1, false) // capacity 1
	s.ws.connectionCnt.Add(1)            // the single slot is already taken

	conn := &wsConnection{id: "conn_unreserved", reserved: false}
	if s.registerWSConnection(context.Background(), conn) {
		t.Fatal("registering an unreserved connection past capacity should be refused")
	}
}

// TestRecentEventsWithoutBusReturnsEmpty covers the recent-events endpoint when no
// in-memory event bus is wired: it must return a well-formed empty event list
// rather than erroring.
func TestRecentEventsWithoutBusReturnsEmpty(t *testing.T) {
	s := newSmokeServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events/recent", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"events"`) {
		t.Fatalf("body missing events key: %s", rec.Body.String())
	}
}

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

// failingWSConn is a wsRawConn whose writes fail on demand, letting the writer's
// disconnect branch be driven deterministically (no real socket teardown to
// race against context cancellation).
type failingWSConn struct {
	writeErr error
	closed   bool
}

func (f *failingWSConn) WriteMessage(int, []byte) error { return f.writeErr }
func (f *failingWSConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("closed")
}
func (f *failingWSConn) SetWriteDeadline(time.Time) error { return nil }
func (f *failingWSConn) Close() error                     { f.closed = true; return nil }

// TestRunWSWriterRecordsFailureOnWriteError covers the writer's disconnect path:
// a queued frame whose write fails must record a failed-message metric and stop
// the writer loop. Previously this branch was only hit by chance when a real
// client vanished mid-write, which made the package's coverage flaky.
func TestRunWSWriterRecordsFailureOnWriteError(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	srv.wsPingInterval = time.Minute // positive so NewTicker doesn't panic; it never fires here
	conn := &wsConnection{
		id:   "writer-fail",
		conn: &failingWSConn{writeErr: errors.New("broken pipe")},
		send: make(chan wsOutbound, 1),
	}
	conn.send <- wsOutbound{messageType: websocket.TextMessage, payload: []byte("frame")}

	done := make(chan struct{})
	go func() {
		srv.runWSWriter(context.Background(), conn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runWSWriter did not return after a write error")
	}
	if got := srv.ws.metrics.Snapshot().MessagesFailed; got != 1 {
		t.Fatalf("MessagesFailed = %d, want 1", got)
	}
}
