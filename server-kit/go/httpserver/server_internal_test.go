package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// TestRequestIP covers the client-IP extraction precedence: X-Forwarded-For wins
// (first hop), otherwise the host is split from RemoteAddr, and an addr with no
// port falls through to the raw value. A nil request yields the empty string.
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

// TestNormalizedRouteMethod pins the route-method normalization: blank defaults
// to GET and casing is upper-folded.
func TestNormalizedRouteMethod(t *testing.T) {
	if got := normalizedRouteMethod("  "); got != http.MethodGet {
		t.Fatalf("blank method = %q, want GET", got)
	}
	if got := normalizedRouteMethod("post"); got != http.MethodPost {
		t.Fatalf("lowercase method = %q, want POST", got)
	}
}

// TestMethodMux covers the per-method dispatch: an empty table is a 404, a
// matching method runs, HEAD falls back to GET, and an unmapped method is a 405.
func TestMethodMux(t *testing.T) {
	// Empty table -> 404.
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

	// HEAD falls back to the GET handler.
	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest("HEAD", "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("HEAD fallback = %d, want 418", rec.Code)
	}

	// Unmapped method -> 405.
	rec = httptest.NewRecorder()
	mux(rec, httptest.NewRequest("DELETE", "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE = %d, want 405", rec.Code)
	}
}

// TestEnrichMetadataFromRequest covers the request-derived enrichment: defaults
// are filled (source, user-agent, IP) and a nil metadata/request is a no-op.
func TestEnrichMetadataFromRequest(t *testing.T) {
	enrichMetadataFromRequest(nil, httptest.NewRequest("GET", "/", nil)) // no panic

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

// TestEnrichMetadataFromAuthContext covers the auth-derived enrichment: an empty
// context is a no-op, while a context carrying identity stamps user/org/role.
func TestEnrichMetadataFromAuthContext(t *testing.T) {
	md := &metadata.EnvelopeMetadata{}
	enrichMetadataFromAuthContext(t.Context(), md) // no identity -> no-op
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

// TestEnforceRBACGuards covers the early returns of enforceRBAC: disabled
// dispatch auth is a pass, an enabled gate with no authenticated user is
// unauthorized, and a missing authorizer passes once a user is present.
func TestEnforceRBACGuards(t *testing.T) {
	s := newSmokeServer(t)

	// Auth not required for dispatch -> always passes.
	if err := s.enforceRBAC(t.Context(), "demo:e:v1:requested", "", ""); err != nil {
		t.Fatalf("disabled dispatch auth err = %v, want nil", err)
	}

	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true) // requireAuthForDispatch = true
	// No user in context -> unauthorized.
	if err := s.enforceRBAC(t.Context(), "demo:e:v1:requested", "", ""); err == nil {
		t.Fatal("missing user err = nil, want unauthorized")
	}
	// User present but no rbac configured -> pass.
	s.rbac = nil
	ctx := security.ContextWithUserID(t.Context(), "user_1")
	if err := s.enforceRBAC(ctx, "demo:e:v1:requested", "", ""); err != nil {
		t.Fatalf("nil rbac with user err = %v, want nil", err)
	}
}

// TestConfigureRateLimitDefaults covers the clamp branches: non-positive request
// counts and windows fall back to sane defaults.
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

// TestConfigureWebSocketAndCompressionDefaults covers the clamp branches of the
// websocket and compression configuration (TE-04 boundaries): non-positive
// max-connections and min-bytes fall back to safe defaults rather than disabling
// the limit or compressing zero-byte payloads.
func TestConfigureWebSocketAndCompressionDefaults(t *testing.T) {
	s := newSmokeServer(t)

	s.ConfigureWebSocket(true, 0, true) // 0 -> default cap
	if s.wsMaxConnections != 10000 {
		t.Fatalf("wsMaxConnections = %d, want clamped to 10000", s.wsMaxConnections)
	}
	if !s.wsEnabled || !s.wsAuthRequired {
		t.Fatal("ConfigureWebSocket flags not applied")
	}

	s.ConfigureCompression(true, 0, 5) // 0 -> default min bytes
	if s.httpCompressionMinBytes != 1024 {
		t.Fatalf("httpCompressionMinBytes = %d, want clamped to 1024", s.httpCompressionMinBytes)
	}
	if !s.httpCompressionEnabled {
		t.Fatal("compression flag not applied")
	}
}

// TestSetHTTPRoutes covers both the empty reset and the defensive copy paths.
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
	// Mutating the input slice must not alter the stored copy.
	in[0].Path = "/mutated"
	if s.routes[0].Path != "/v1/x" {
		t.Fatal("SetHTTPRoutes did not take a defensive copy")
	}
}

// TestHealthEndpointsCustomHandlers covers the delegated branch of the health
// endpoints: when custom handlers are configured, healthz/liveness/readiness
// defer to them instead of the default JSON status.
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
