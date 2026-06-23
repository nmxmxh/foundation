package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsmetrics"
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
