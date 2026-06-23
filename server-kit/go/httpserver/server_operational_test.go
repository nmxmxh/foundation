package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

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

// TestOperationalAuthNegativeCases covers the trust-boundary rejections of the
// protected operational endpoints (TE-19): a malformed Authorization header and a
// structurally-invalid token are each refused, with distinct error classes for
// "no usable credential" versus "credential present but invalid".
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

// TestOperationalAuthRBACDeniesInsufficientCapability covers authorizeOperational
// Context (TE-09/TE-19): a fully-authenticated operator who lacks the ops.metrics
// capability is denied 403, proving the operational gate authorizes the action,
// not merely the presence of a token.
func TestOperationalAuthRBACDeniesInsufficientCapability(t *testing.T) {
	jwt, err := auth.NewJWTManager("ops-secret-value-1234567890")
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	// Authorizer where role "viewer" grants only an unrelated capability.
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

// TestOperationalAuthHonorsUpstreamContextIdentity covers the branch where an
// upstream middleware already authenticated the request: with a user already in
// context and no RBAC configured, the operational endpoint serves without
// re-parsing a token.
func TestOperationalAuthHonorsUpstreamContextIdentity(t *testing.T) {
	h := newOperationalServer(t, nil, nil).Handler() // jwt nil, rbac nil

	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req = req.WithContext(security.ContextWithUserID(req.Context(), "upstream_user"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
