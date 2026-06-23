package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

// TestRouteDispatchPublicPathBypassesCapability covers the public-path branch of
// routeDispatch (TE-19): a capability-gated dispatch route mounted on a public
// path has its capability requirement stripped, so an unauthenticated request is
// served. This is the complement to TestRouteRBACEnforced (which proves a gated,
// non-public route denies).
func TestRouteDispatchPublicPathBypassesCapability(t *testing.T) {
	ran := false
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) {
		ran = true
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true) // dispatch auth required
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:             http.MethodGet,
		Path:               "/v1/public-thing",
		EventType:          testEvent, // no Handler -> routeDispatch path
		RequiredCapability: "demo.ping",
	}})
	s.AddPublicPath("/v1/public-thing")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/public-thing", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public route status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !ran {
		t.Fatal("public-path route should dispatch without authentication")
	}
}

// TestObjectFromJSONValueHandlesUnmarshalable covers the marshal-failure branch of
// objectFromJSONValue: a value that cannot be JSON-encoded (a channel) yields an
// empty object rather than panicking, so a malformed payload cannot crash the
// websocket dispatch path.
func TestObjectFromJSONValueHandlesUnmarshalable(t *testing.T) {
	got := objectFromJSONValue(make(chan int))
	if got == nil || len(got) != 0 {
		t.Fatalf("unmarshalable value = %v, want empty object", got)
	}
	// A normal value still round-trips into an object.
	obj := objectFromJSONValue(map[string]any{"k": "v"})
	if v, _ := obj.GetString("k"); v != "v" {
		t.Fatalf("object round-trip lost data: %v", obj)
	}
}

// TestOperationalAuthNoJWTNoContextRejected covers the branch where operational
// endpoints are protected but no JWT manager is configured and the request
// carries no upstream identity: it must be rejected as authorization_required
// rather than served.
func TestOperationalAuthNoJWTNoContextRejected(t *testing.T) {
	s := newOperationalServer(t, nil, nil) // jwt nil, rbac nil, protected
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestWSReaderSkipsEmptyFrames covers the reader's zero-length-payload guard: an
// empty frame is skipped (not treated as a malformed envelope), and a subsequent
// valid request is still dispatched on the same connection.
func TestWSReaderSkipsEmptyFrames(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn) // ack

	if err := conn.WriteMessage(websocket.TextMessage, []byte{}); err != nil {
		t.Fatalf("write empty frame: %v", err)
	}
	// The connection survives the empty frame and still serves a real request.
	sendEnv(t, conn, requested("identity:ping:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:ping:v1:success" {
		t.Fatalf("response after empty frame = %q, want :success", resp.EventType)
	}
}

// TestWSAuthUpgradeUpdatesRouter covers the router branch of maybeUpgradeConnection
// Auth (TE-11 cross-instance routing): when a router is configured, authenticating
// a connection propagates the user identity to the router so cross-instance
// delivery can target the authenticated user.
func TestWSAuthUpgradeUpdatesRouter(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"user_id": "user_routed", "organization_id": "org_1"}, nil
		},
	})
	router := wsrouting.NewRouter(nil, "ws-test-server")
	s.ws.WithRouter(router)

	conn := dialWS(t, srv, "deviceId=dev_routed_auth")
	_ = readEnv(t, conn) // ack
	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:authenticate_connection:v1:success" {
		t.Fatalf("auth response = %q", resp.EventType)
	}
	if !wsConnectionAuthenticated(s, "dev_routed_auth") {
		t.Fatal("connection should be authenticated")
	}
	// The router learned the authenticated user.
	deadline := time.Now().Add(time.Second)
	for len(router.GetLocalConnectionsByUser("user_routed")) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(router.GetLocalConnectionsByUser("user_routed")) == 0 {
		t.Fatal("router did not learn the authenticated user")
	}
}

// TestPathAndEventGuards covers the empty-input boundaries of two predicates: an
// empty path is never public, and an empty event type is never guest-allowed.
func TestPathAndEventGuards(t *testing.T) {
	s := newSmokeServer(t)
	s.AddPublicPath("/v1/open")
	if s.isPublicPath("") {
		t.Fatal("empty path must not be considered public")
	}
	if !s.isPublicPath("/v1/open/sub") {
		t.Fatal("prefix of a public path should be public")
	}
	if s.isWSGuestAllowedEvent("") {
		t.Fatal("empty event type must not be guest-allowed")
	}
}

// TestWSBinaryDomainErrorFrame covers the binary-format branch of the websocket
// error path: a binary-format connection that triggers a domain error (a guest
// sending a non-allowlisted event) receives the error as a binary frame, matching
// the negotiated wire format.
func TestWSBinaryDomainErrorFrame(t *testing.T) {
	srv, _ := newWSTestServer(t, nil) // nothing allowlisted
	conn := dialWS(t, srv, "format=binary")
	if _, _, err := conn.ReadMessage(); err != nil { // binary ack
		t.Fatalf("read ack: %v", err)
	}

	raw, err := requested("orders:create:v1:requested", extension.Object{}).ToBinary()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("error frame type = %d, want binary", mt)
	}
	env, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("decode binary error frame: %v", err)
	}
	if env.EventType != "system:websocket_error:v1:failed" {
		t.Fatalf("event type = %q, want websocket_error", env.EventType)
	}
}
