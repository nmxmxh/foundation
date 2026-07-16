package httpserver

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	// This smoke test keeps the scaffold server package in the unit-test set
	// without assuming a project has preserved the baseline constructor shape.
	"bytes"
	"encoding/json"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerScaffoldPackageCompiles(t *testing.T) {

}

func newTestWSRuntimeServer(maxConnections int) *Server {
	return &Server{
		wsMaxConnections: maxConnections,
		ws:               newWSRuntime(),
	}
}

func TestReserveWSConnectionSlotRejectsWhenCapacityExceeded(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	srv.ws.connectionCnt.Store(1)

	if srv.reserveWSConnectionSlot() {
		t.Fatal("expected capacity rejection")
	}
	if got := srv.ws.connectionCnt.Load(); got != 1 {
		t.Fatalf("connection count drifted after rejection: %d", got)
	}
	if _, ok := srv.ws.connections.Load("overflow"); ok {
		t.Fatal("rejected connection was stored")
	}
	if got := srv.ws.metrics.Snapshot().ConnectionsRejected; got != 1 {
		t.Fatalf("rejected metric = %d, want 1", got)
	}
}

func TestRegisterWSConnectionUsesReservedSlot(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	if !srv.reserveWSConnectionSlot() {
		t.Fatal("reserve slot failed")
	}

	registered := srv.registerWSConnection(context.Background(), &wsConnection{
		id:       "accepted",
		deviceID: "accepted-device",
		reserved: true,
	})

	if !registered {
		t.Fatal("expected reserved connection to register")
	}
	if got := srv.ws.connectionCnt.Load(); got != 1 {
		t.Fatalf("connection count = %d, want 1", got)
	}
	if got := srv.ws.metrics.Snapshot().ConnectionsTotal; got != 1 {
		t.Fatalf("connection total metric = %d, want 1", got)
	}
}

func TestEnqueueWSRecordsBackpressureFailure(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	conn := &wsConnection{
		id:   "blocked",
		send: make(chan wsOutbound),
	}

	err := srv.enqueueWS(conn, websocket.TextMessage, []byte("payload"))

	if err == nil {
		t.Fatal("expected queue-full error")
	}
	if got := srv.ws.metrics.Snapshot().MessagesFailed; got != 1 {
		t.Fatalf("failed message metric = %d, want 1", got)
	}
}

func TestRouteDispatchPublicPathBypassesCapability(t *testing.T) {
	ran := false
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) {
		ran = true
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:             http.MethodGet,
		Path:               "/v1/public-thing",
		EventType:          testEvent,
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

func TestObjectFromJSONValueHandlesUnmarshalable(t *testing.T) {
	got := objectFromJSONValue(make(chan int))
	if got == nil || len(got) != 0 {
		t.Fatalf("unmarshalable value = %v, want empty object", got)
	}

	obj := objectFromJSONValue(map[string]any{"k": "v"})
	if v, _ := obj.GetString("k"); v != "v" {
		t.Fatalf("object round-trip lost data: %v", obj)
	}
}

func TestOperationalAuthNoJWTNoContextRejected(t *testing.T) {
	s := newOperationalServer(t, nil, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestWSReaderSkipsEmptyFrames(t *testing.T) {
	srv, _ := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	conn := dialWS(t, srv, "")
	_ = readEnv(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte{}); err != nil {
		t.Fatalf("write empty frame: %v", err)
	}

	sendEnv(t, conn, requested("identity:ping:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:ping:v1:success" {
		t.Fatalf("response after empty frame = %q, want :success", resp.EventType)
	}
}

func TestWSAuthUpgradeUpdatesRouter(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"user_id": "user_routed", "organization_id": "org_1"}, nil
		},
	})
	router := wsrouting.NewRouter(nil, "ws-test-server")
	s.ws.WithRouter(router)

	conn := dialWS(t, srv, "deviceId=dev_routed_auth")
	_ = readEnv(t, conn)
	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	if resp := readEnv(t, conn); resp.EventType != "identity:authenticate_connection:v1:success" {
		t.Fatalf("auth response = %q", resp.EventType)
	}
	if !wsConnectionAuthenticated(s, "dev_routed_auth") {
		t.Fatal("connection should be authenticated")
	}

	deadline := time.Now().Add(time.Second)
	for len(router.GetLocalConnectionsByUser("user_routed")) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(router.GetLocalConnectionsByUser("user_routed")) == 0 {
		t.Fatal("router did not learn the authenticated user")
	}
}

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

func TestWSBinaryDomainErrorFrame(t *testing.T) {
	srv, _ := newWSTestServer(t, nil)
	conn := dialWS(t, srv, "format=binary")
	if _, _, err := conn.ReadMessage(); err != nil {
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

func serverWithDispatch(t *testing.T, h func(ctx context.Context, payload extension.Object) (any, error)) *Server {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("extra-test"))
	reg := registry.New(nil, gh, log)
	if err := reg.Register(testEvent, h); err != nil {
		t.Fatalf("register: %v", err)
	}
	return New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
}

func TestRunStartsAndShutsDown(t *testing.T) {
	s := newSmokeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestStreamResponseNDJSON(t *testing.T) {
	ch := make(chan map[string]any, 2)
	ch <- map[string]any{"n": 1}
	ch <- map[string]any{"n": 2}
	close(ch)
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return (<-chan map[string]any)(ch), nil
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_stream", extension.Object{})))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want ndjson", ct)
	}
	if !strings.Contains(rec.Body.String(), `"n":1`) || !strings.Contains(rec.Body.String(), `"n":2`) {
		t.Fatalf("stream body missing items: %s", rec.Body.String())
	}
}

func TestStreamResponseUnsupportedType(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return make(<-chan int), nil
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_bad", extension.Object{})))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported_response") {
		t.Fatalf("missing unsupported_response: %s", rec.Body.String())
	}
}

func TestDispatchHandlerErrorWritesDomainError(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return nil, domainerr.Validation("handler_failed", "the handler rejected the request")
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_err", extension.Object{})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestOperationalEndpointsProtected(t *testing.T) {
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("ops-test"))
	reg := registry.New(nil, gh, log)
	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}, ProtectOperationalEndpoints: true}, reg, gh)
	jwt, err := auth.NewJWTManager("ops-secret-value-1234567890")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	s.ConfigureAuth(jwt, nil, true)
	h := s.Handler()

	t.Run("no auth is 401", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("valid token passes", func(t *testing.T) {
		token, genErr := jwt.GenerateAccessToken(auth.Claims{UserID: "ops_user", OrganizationID: "org_1", Role: "admin"}, time.Minute)
		if genErr != nil {
			t.Fatalf("token: %v", genErr)
		}
		req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestRouteRBACEnforced(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)
	reached := false
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:             http.MethodGet,
		Path:               "/v1/protected",
		EventType:          "orders:read:v1:requested",
		RequiredCapability: "orders.read",
		Handler:            func(http.ResponseWriter, *http.Request) { reached = true },
	}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/protected", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if reached {
		t.Fatal("handler should not be reached when RBAC denies")
	}
}

func TestStreamResponseBytes(t *testing.T) {
	ch := make(chan []byte, 2)
	ch <- []byte("alpha")
	ch <- []byte("beta")
	close(ch)
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return (<-chan []byte)(ch), nil
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_bytes", extension.Object{})))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q, want octet-stream", ct)
	}
	if rec.Body.String() != "alphabeta" {
		t.Fatalf("stream body = %q, want alphabeta", rec.Body.String())
	}
}

func TestStreamResponseStopsOnClientCancel(t *testing.T) {
	ch := make(chan map[string]any)
	streaming := make(chan struct{})
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		close(streaming)
		return (<-chan map[string]any)(ch), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_cancel", extension.Object{})).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	<-streaming
	cancel()

	select {
	case <-done:

	case <-time.After(2 * time.Second):
		t.Fatal("stream did not terminate after request context was cancelled")
	}
}

func TestRequestIP(t *testing.T) {
	fwd := httptest.NewRequest("GET", "/", nil)
	fwd.Header.Set("X-Forwarded-For", " 203.0.113.7 , 10.0.0.1 ")
	if got := requestIP(fwd); got != "203.0.113.7" {
		t.Fatalf("X-Forwarded-For IP = %q, want 203.0.113.7", got)
	}

	hostPort := httptest.NewRequest("GET", "/", nil)
	hostPort.RemoteAddr = "192.0.2.5:5555"
	if got := requestIP(hostPort); got != "192.0.2.5" {
		t.Fatalf("host:port IP = %q, want 192.0.2.5", got)
	}

	bare := httptest.NewRequest("GET", "/", nil)
	bare.RemoteAddr = "noport"
	if got := requestIP(bare); got != "noport" {
		t.Fatalf("bare RemoteAddr IP = %q, want noport", got)
	}

	if got := requestIP(nil); got != "" {
		t.Fatalf("nil request IP = %q, want empty", got)
	}
}

func TestNormalizedRouteMethod(t *testing.T) {
	if got := normalizedRouteMethod("  "); got != http.MethodGet {
		t.Fatalf("blank method = %q, want GET", got)
	}
	if got := normalizedRouteMethod("post"); got != http.MethodPost {
		t.Fatalf("lowercase method = %q, want POST", got)
	}
}

func TestMethodMux(t *testing.T) {

	rec := httptest.NewRecorder()
	methodMux(nil)(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty mux = %d, want 404", rec.Code)
	}

	handlers := map[string]http.HandlerFunc{
		http.MethodGet: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) },
	}
	mux := methodMux(handlers)

	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("GET = %d, want 418", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest("HEAD", "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("HEAD fallback = %d, want 418", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest("DELETE", "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE = %d, want 405", rec.Code)
	}
}

func TestEnrichMetadataFromRequest(t *testing.T) {
	enrichMetadataFromRequest(nil, httptest.NewRequest("GET", "/", nil))

	md := &metadata.EnvelopeMetadata{}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "ovs-agent/1.0")
	r.RemoteAddr = "198.51.100.9:443"
	enrichMetadataFromRequest(md, r)
	if md.GlobalContext == nil {
		t.Fatal("GlobalContext should be initialized")
	}
	if md.GlobalContext.Source != "api" {
		t.Fatalf("source = %q, want api", md.GlobalContext.Source)
	}
	if md.GlobalContext.UserAgent != "ovs-agent/1.0" {
		t.Fatalf("user agent = %q", md.GlobalContext.UserAgent)
	}
	if md.GlobalContext.IPAddress != "198.51.100.9" {
		t.Fatalf("ip = %q, want 198.51.100.9", md.GlobalContext.IPAddress)
	}
}

func TestEnrichMetadataFromAuthContext(t *testing.T) {
	md := &metadata.EnvelopeMetadata{}
	enrichMetadataFromAuthContext(t.Context(), md)
	if md.GlobalContext != nil {
		t.Fatal("empty auth context should not initialize GlobalContext")
	}

	ctx := security.ContextWithUserID(t.Context(), "user_1")
	ctx = security.ContextWithOrganizationID(ctx, "org_1")
	ctx = security.ContextWithRole(ctx, "admin")
	enrichMetadataFromAuthContext(ctx, md)
	if md.GlobalContext == nil ||
		md.GlobalContext.UserID != "user_1" ||
		md.GlobalContext.OrganizationID != "org_1" ||
		md.GlobalContext.RoleID != "admin" {
		t.Fatalf("auth enrichment = %+v", md.GlobalContext)
	}
}

func TestEnforceRBACGuards(t *testing.T) {
	s := newSmokeServer(t)

	if err := s.enforceRBAC(t.Context(), "demo:e:v1:requested", "", ""); err != nil {
		t.Fatalf("disabled dispatch auth err = %v, want nil", err)
	}

	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)

	if err := s.enforceRBAC(t.Context(), "demo:e:v1:requested", "", ""); err == nil {
		t.Fatal("missing user err = nil, want unauthorized")
	}

	s.rbac = nil
	ctx := security.ContextWithUserID(t.Context(), "user_1")
	if err := s.enforceRBAC(ctx, "demo:e:v1:requested", "", ""); err != nil {
		t.Fatalf("nil rbac with user err = %v, want nil", err)
	}
}

func TestConfigureRateLimitDefaults(t *testing.T) {
	s := newSmokeServer(t)
	s.ConfigureRateLimit(true, 0, 0)
	if !s.apiRateLimitEnabled {
		t.Fatal("rate limit should be enabled")
	}
	if s.apiRateLimitRequests != 1 {
		t.Fatalf("requests = %d, want clamped to 1", s.apiRateLimitRequests)
	}
	if s.apiRateLimitWindow != time.Minute {
		t.Fatalf("window = %v, want clamped to 1m", s.apiRateLimitWindow)
	}
}

func TestConfigureWebSocketAndCompressionDefaults(t *testing.T) {
	s := newSmokeServer(t)

	s.ConfigureWebSocket(true, 0, true)
	if s.wsMaxConnections != 10000 {
		t.Fatalf("wsMaxConnections = %d, want clamped to 10000", s.wsMaxConnections)
	}
	if !s.wsEnabled || !s.wsAuthRequired {
		t.Fatal("ConfigureWebSocket flags not applied")
	}

	s.ConfigureCompression(true, 0, 5)
	if s.httpCompressionMinBytes != 1024 {
		t.Fatalf("httpCompressionMinBytes = %d, want clamped to 1024", s.httpCompressionMinBytes)
	}
	if !s.httpCompressionEnabled {
		t.Fatal("compression flag not applied")
	}
}

func TestSetHTTPRoutes(t *testing.T) {
	s := newSmokeServer(t)
	s.SetHTTPRoutes(nil)
	if len(s.routes) != 0 {
		t.Fatalf("empty routes len = %d, want 0", len(s.routes))
	}
	in := []registry.HTTPRoute{{Method: "GET", Path: "/v1/x", EventType: "x:y:v1:requested"}}
	s.SetHTTPRoutes(in)
	if len(s.routes) != 1 || s.routes[0].Path != "/v1/x" {
		t.Fatalf("routes = %+v", s.routes)
	}

	in[0].Path = "/mutated"
	if s.routes[0].Path != "/v1/x" {
		t.Fatal("SetHTTPRoutes did not take a defensive copy")
	}
}

func TestHealthEndpointsCustomHandlers(t *testing.T) {
	s := newSmokeServer(t)
	custom := func(code int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
	}
	s.ConfigureHealthChecks(custom(201), custom(202), custom(203))

	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		want int
	}{
		{"healthz", s.healthz, 201},
		{"liveness", s.liveness, 202},
		{"readiness", s.readiness, 203},
	} {
		rec := httptest.NewRecorder()
		tc.fn(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != tc.want {
			t.Fatalf("%s custom handler = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}
func newOperationalServer(t *testing.T, jwt *auth.JWTManager, authorizer *security.Authorizer) *Server {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("ops-neg-test"))
	reg := registry.New(nil, gh, log)
	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}, ProtectOperationalEndpoints: true}, reg, gh)
	s.ConfigureAuth(jwt, authorizer, true)
	return s
}

func TestOperationalAuthNegativeCases(t *testing.T) {
	jwt, err := auth.NewJWTManager("ops-secret-value-1234567890")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	h := newOperationalServer(t, jwt, nil).Handler()

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
		wantBody   string
	}{
		{"no header", "", http.StatusUnauthorized, "authorization_required"},
		{"malformed header", "Garbage", http.StatusUnauthorized, "authorization_required"},
		{"invalid token", "Bearer not.a.valid.jwt", http.StatusUnauthorized, "authorization_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body missing %q: %s", tc.wantBody, rec.Body.String())
			}
		})
	}
}

func TestOperationalAuthRBACDeniesInsufficientCapability(t *testing.T) {
	jwt, err := auth.NewJWTManager("ops-secret-value-1234567890")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}

	authorizer := security.NewAuthorizer([]security.RoleTemplate{{Role: "viewer", Capabilities: []string{"other.read"}}})
	h := newOperationalServer(t, jwt, authorizer).Handler()

	token, err := jwt.GenerateAccessToken(auth.Claims{UserID: "ops_user", OrganizationID: "org_1", Role: "viewer"}, time.Minute)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAuthenticateOperationalRequestValidToken drives the direct token path of
// authenticateOperationalRequest: a request carrying a valid bearer token but no
// pre-set identity is validated and its claims are projected into the request
// context. The HTTP handler tests only reach the early-return branch (upstream
// auth middleware pre-populates the context), so this exercises the
// token-validation-to-context build — and the nil-request guard — directly.
func TestAuthenticateOperationalRequestValidToken(t *testing.T) {
	jwt, err := auth.NewJWTManager("ops-secret-value-1234567890")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	s := newOperationalServer(t, jwt, nil) // nil authorizer → rbac disabled, authorize passes

	token, err := jwt.GenerateAccessToken(auth.Claims{
		UserID: "ops_user", OrganizationID: "org_9", Role: "admin", SessionID: "sess_9",
	}, time.Minute)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	got, err := s.authenticateOperationalRequest(req)
	if err != nil {
		t.Fatalf("authenticateOperationalRequest() err=%v", err)
	}
	ctx := got.Context()
	if security.GetUserIDFromContext(ctx) != "ops_user" ||
		security.GetOrganizationIDFromContext(ctx) != "org_9" ||
		security.GetRoleFromContext(ctx) != "admin" ||
		security.GetSessionIDFromContext(ctx) != "sess_9" {
		t.Fatalf("claims not projected into context: user=%q org=%q role=%q sess=%q",
			security.GetUserIDFromContext(ctx), security.GetOrganizationIDFromContext(ctx),
			security.GetRoleFromContext(ctx), security.GetSessionIDFromContext(ctx))
	}

	// A nil request is rejected rather than panicking.
	if _, err := s.authenticateOperationalRequest(nil); err == nil {
		t.Fatal("nil request must be unauthorized")
	}
}

func TestOperationalAuthHonorsUpstreamContextIdentity(t *testing.T) {
	h := newOperationalServer(t, nil, nil).Handler()

	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req = req.WithContext(security.ContextWithUserID(req.Context(), "upstream_user"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func identityContext(userID, role string, capabilities []string) context.Context {
	ctx := security.ContextWithUserID(context.Background(), userID)
	ctx = security.ContextWithRole(ctx, role)
	return security.ContextWithCapabilities(ctx, capabilities)
}

func TestDispatchRBACAllowsAndDenies(t *testing.T) {
	handlerRan := false
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		handlerRan = true
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)

	t.Run("granted capability is allowed", func(t *testing.T) {
		handlerRan = false
		req := postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_ok", extension.Object{}))
		req = req.WithContext(identityContext("user_1", "member", []string{"demo.ping"}))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if !handlerRan {
			t.Fatal("handler should run when the caller holds the capability")
		}
	})

	t.Run("missing capability is denied", func(t *testing.T) {
		handlerRan = false
		req := postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_deny", extension.Object{}))
		req = req.WithContext(identityContext("user_2", "member", []string{"other.thing"}))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if handlerRan {
			t.Fatal("handler must not run when the caller lacks the capability")
		}
	})

	t.Run("unauthenticated is rejected", func(t *testing.T) {
		handlerRan = false
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_anon", extension.Object{})))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		if handlerRan {
			t.Fatal("handler must not run for an unauthenticated dispatch")
		}
	})
}

func TestRouteRBACAllowsGrantedAndBypassesPublic(t *testing.T) {
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) { return nil, nil })
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)

	reached := 0
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:             http.MethodGet,
		Path:               "/v1/protected",
		EventType:          "orders:read:v1:requested",
		RequiredCapability: "orders.read",
		Handler:            func(http.ResponseWriter, *http.Request) { reached++ },
	}})

	req := httptest.NewRequest(http.MethodGet, "/v1/protected", nil)
	req = req.WithContext(identityContext("user_1", "member", []string{"orders.read"}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if reached != 1 {
		t.Fatalf("authorized handler reached %d times, want 1 (status=%d)", reached, rec.Code)
	}

	s.AddPublicPath("/v1/protected")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/protected", nil))
	if reached != 2 {
		t.Fatalf("public path handler reached %d times, want 2 (status=%d)", reached, rec.Code)
	}
}

func TestIsPublicRouteBypassesAuth(t *testing.T) {
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) { return nil, nil })
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true)

	reached := 0
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:   http.MethodGet,
		Path:     "/v1/media/objects/",
		Handler:  func(w http.ResponseWriter, _ *http.Request) { reached++; w.WriteHeader(http.StatusOK) },
		IsPublic: true,
	}})

	for _, path := range []string{"/v1/media/objects/seed.png", "/api/v1/media/objects/seed.png"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("unauthenticated GET %s: status = %d, want 200 (public route must bypass auth)", path, rec.Code)
		}
	}
	if reached != 2 {
		t.Fatalf("public media handler reached %d times, want 2", reached)
	}
}

// TestIsPublicRouteWithPathParamsBypassesAuth registers a public route the way
// domains actually do — with a "{param}" template in the path — and asserts an
// unauthenticated request to a concrete key still bypasses enforced JWT auth.
// The template's brace segment never prefix-matches a real request path, so
// registerPublicRoutePaths must expose the static prefix before the first
// parameter.
func TestIsPublicRouteWithPathParamsBypassesAuth(t *testing.T) {
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) { return nil, nil })
	manager, err := auth.NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager: %v", err)
	}
	s.ConfigureAuth(manager, security.NewAuthorizer(nil), true)

	reached := 0
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method:   http.MethodGet,
		Path:     "/v1/media/objects/{key...}",
		Handler:  func(w http.ResponseWriter, _ *http.Request) { reached++; w.WriteHeader(http.StatusOK) },
		IsPublic: true,
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/media/objects/seed/chef/amara.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unauthenticated GET of templated public route: status = %d, want 200", rec.Code)
	}
	if reached != 1 {
		t.Fatalf("templated public handler reached %d times, want 1", reached)
	}
}

// TestOptionalAuthEstablishesIdentityWithoutRequiringIt covers the development
// posture: auth not required, but a presented bearer token must still populate
// the security context — command metadata and projection tenancy depend on it.
func TestOptionalAuthEstablishesIdentityWithoutRequiringIt(t *testing.T) {
	s := serverWithDispatch(t, func(context.Context, extension.Object) (any, error) { return nil, nil })
	manager, err := auth.NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager: %v", err)
	}
	s.ConfigureAuth(manager, security.NewAuthorizer(nil), false)

	token, err := manager.GenerateAccessToken(auth.Claims{
		UserID:         "usr_1",
		OrganizationID: "org_1",
		Role:           "customer",
	}, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	var gotOrg string
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method: http.MethodGet,
		Path:   "/v1/whoami",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			gotOrg = security.GetOrganizationIDFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		},
	}})
	handler := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotOrg != "org_1" {
		t.Fatalf("optional auth with token: status = %d, org = %q; want 200/org_1", rec.Code, gotOrg)
	}

	gotOrg = "unset"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/whoami", nil))
	if rec.Code != http.StatusOK || gotOrg != "" {
		t.Fatalf("optional auth without token: status = %d, org = %q; want 200/anonymous", rec.Code, gotOrg)
	}
}

func TestTerminalEventType(t *testing.T) {
	cases := []struct {
		in       string
		terminal string
		want     string
	}{
		{"orders:create:v1:requested", "success", "orders:create:v1:success"},
		{"orders:create:v1:ack", "failed", "orders:create:v1:failed"},
		{"orders:create:v1:success", "failed", "orders:create:v1:failed"},
		{"orders:create:v1:failed", "success", "orders:create:v1:success"},
		{"orders:create:v1", "success", "orders:create:v1:success"},
		{"  ", "success", ""},
		{"orders:create:v1:requested", "  ", "orders:create:v1:requested"},
	}
	for _, tc := range cases {
		if got := terminalEventType(tc.in, tc.terminal); got != tc.want {
			t.Fatalf("terminalEventType(%q,%q) = %q, want %q", tc.in, tc.terminal, got, tc.want)
		}
	}
}
func newSmokeServer(t *testing.T) *Server {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("smoke"))
	reg := registry.New(nil, gh, log)
	return New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
}

func TestServerHandlerWiresHealthAndMiddleware(t *testing.T) {
	h := newSmokeServer(t).Handler()

	t.Run("healthz returns 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz = %d, want 200", rec.Code)
		}
	})

	t.Run("security headers applied", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing security headers: %v", rec.Header())
		}
	})

	t.Run("unknown route 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no/such/path", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown route = %d, want 404", rec.Code)
		}
	})

	t.Run("projection path unmounted by default 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/x/y", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unmounted projection = %d, want 404", rec.Code)
		}
	})
}

func TestConfigureProjectionGatewayMounts(t *testing.T) {
	s := newSmokeServer(t)
	s.ConfigureProjectionGateway(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/x/y", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("mounted projection = %d, want 418", rec.Code)
	}
}

const testEvent = "demo:ping:v1:requested"

// serverWithHandler builds a server whose registry has a single echo handler, so
// the dispatch pipeline has something to resolve.
func serverWithHandler(t *testing.T) *Server {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("dispatch-test"))
	reg := registry.New(nil, gh, log)
	if err := reg.Register(testEvent, func(_ context.Context, payload extension.Object) (any, error) {
		return map[string]any{"echo": payload}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
}

func TestConfigureSettersAndAccessors(t *testing.T) {
	s := serverWithHandler(t)
	jwt, _ := auth.NewJWTManager("smoke-secret-value-1234567890")
	s.ConfigureAuth(jwt, security.NewAuthorizer(nil), true)
	s.ConfigureRateLimit(true, 50, time.Minute)
	s.ConfigureCompression(true, 512, 5)
	s.ConfigureWebSocket(true, 100, false)
	s.AddPublicPath("/custom-public")
	s.AddUnauthenticatedWSEvent("demo:ping:v1:requested")
	s.ConfigureHealthChecks(nil, nil, nil)
	// Handler builds with all the configuration applied.
	if s.Handler() == nil {
		t.Fatal("Handler() nil")
	}
}

func postJSON(path string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// dispatchBody builds a valid dispatch request (with the required timestamp).
func dispatchBody(eventType, corr string, payload extension.Object) []byte {
	body, err := json.Marshal(httpapi.DispatchRequest{
		EventType:     eventType,
		CorrelationID: corr,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Payload:       payload,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func TestDispatchPipeline(t *testing.T) {
	h := serverWithHandler(t).Handler()

	t.Run("registered handler returns its result (not an empty 200)", func(t *testing.T) {
		// Regression: a handler returning map[string]any was misclassified as a
		// stream and silently dropped (200, empty body). The result must come back.
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody(testEvent, "corr_1", extension.Object{"name": extension.String("ovs")})))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			t.Fatal("successful dispatch returned an empty body (handler result was dropped)")
		}
		if !strings.Contains(rec.Body.String(), "echo") {
			t.Fatalf("response missing handler result: %s", rec.Body.String())
		}
	})

	t.Run("GET is 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/dispatch", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET dispatch = %d, want 405", rec.Code)
		}
	})

	t.Run("invalid JSON is 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, postJSON("/v1/dispatch", []byte("{not json")))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad json = %d, want 400", rec.Code)
		}
	})

	t.Run("unknown handler is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, postJSON("/v1/dispatch", dispatchBody("demo:nope:v1:requested", "corr_2", nil)))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown handler = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestDomainRouteDispatch(t *testing.T) {
	s := serverWithHandler(t)
	s.SetHTTPRoutes([]registry.HTTPRoute{
		httpapi.MakeEventRoute(http.MethodPost, "/v1/demo/ping", testEvent, "ping", "", "", httpapi.WithPublic()),
	})
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postJSON("/v1/demo/ping", dispatchBody(testEvent, "corr_3", extension.Object{"k": extension.String("v")})))
	if rec.Code != http.StatusOK {
		t.Fatalf("route dispatch = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Wrong method on a registered path → method handling kicks in.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/demo/ping", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("DELETE on POST route should not be 200")
	}
}

func TestOperationalEndpoints(t *testing.T) {
	h := serverWithHandler(t).Handler() // ProtectOperationalEndpoints=false → open
	for _, path := range []string{"/metricsz", "/metricsz/trace", "/v1/events/recent", "/health/live", "/health/ready"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code >= 500 {
			t.Fatalf("%s = %d (server error)", path, rec.Code)
		}
	}
}

func TestRouteHelpers(t *testing.T) {
	if normalizedRouteMethod("") != http.MethodGet {
		t.Fatalf("empty method should normalize to GET")
	}
	if normalizedRouteMethod("get") != http.MethodGet {
		t.Fatalf("get should normalize to GET")
	}
	// methodMux dispatches by method and 405s the rest.
	mux := methodMux(map[string]http.HandlerFunc{
		http.MethodGet: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	})
	rec := httptest.NewRecorder()
	mux(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("methodMux GET = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("methodMux POST = %d, want 405", rec.Code)
	}
}

func TestRequestIPPrefersForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := requestIP(req); got != "203.0.113.7" {
		t.Fatalf("requestIP = %q, want 203.0.113.7", got)
	}
}

func TestPublicPathMatching(t *testing.T) {
	s := serverWithHandler(t)
	s.AddPublicPath("/open")
	if !s.isPublicPath("/healthz") {
		t.Fatal("/healthz should be public")
	}
	if !s.isPublicPath("/open") {
		t.Fatal("/open should be public after AddPublicPath")
	}
	if s.isPublicPath("/v1/secret") {
		t.Fatal("/v1/secret should not be public")
	}
}

// TestDispatchProtobufResponseLane covers the protobuf request/response lane end to
// end (TE-11 binary parity): a typed handler registered with a proto binding,
// driven over an HTTP route with application/x-protobuf content negotiation, must
// decode the protobuf request, run, and serialize a protobuf response — not fall
// back to JSON. This exercises performDispatch's protobuf-request path and
// executeDispatch's protobuf-response write.
func TestDispatchProtobufResponseLane(t *testing.T) {
	const eventType = "media:process_asset:v1:requested"

	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("proto-test"))
	reg := registry.New(nil, gh, log)

	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	ran := false
	if err := reg.RegisterTypedWithOptions(eventType, binding,
		func(_ context.Context, request proto.Message) (proto.Message, error) {
			ran = true
			req := request.(*testprotos.TestRequest)
			return &testprotos.TestResponse{ResourceId: req.GetWorkspaceId() + ":processed"}, nil
		}, bootstrap.ConcurrencyOptions{}); err != nil {
		t.Fatalf("RegisterTypedWithOptions() err=%v", err)
	}

	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method: http.MethodPost, Path: "/v1/asset", EventType: eventType,
	}})

	body, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_123"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/asset", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Accept", "application/x-protobuf")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !ran {
		t.Fatal("typed handler did not run")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Fatalf("response content-type = %q, want application/x-protobuf", ct)
	}
	var resp testprotos.TestResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a protobuf TestResponse: %v", err)
	}
	if resp.GetResourceId() != "wrk_123:processed" {
		t.Fatalf("resource_id = %q, want wrk_123:processed", resp.GetResourceId())
	}
}
