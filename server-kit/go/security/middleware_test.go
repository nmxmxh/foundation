package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
)

func TestSecurityHeadersAddsCrossOriginIsolationHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://example.com/healthz", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if got := recorder.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Fatalf("unexpected COOP header: %q", got)
	}
	if got := recorder.Header().Get("Cross-Origin-Embedder-Policy"); got != "require-corp" {
		t.Fatalf("unexpected COEP header: %q", got)
	}
	if got := recorder.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("missing HSTS header: %q", got)
	}
	if got := recorder.Header().Get("X-XSS-Protection"); got != "" {
		t.Fatalf("expected X-XSS-Protection to be unset, got %q", got)
	}
}

func TestCORSRejectsDisallowedPreflightOrigin(t *testing.T) {
	handler := CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodOptions, "https://api.example.com/v1/workspace", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden preflight, got %d", recorder.Code)
	}
}

func TestCORSAllowsConfiguredOriginAndEmptyPreflight(t *testing.T) {
	handler := CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/workspace", nil)
	req.Header.Set("Origin", "https://app.example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected handler status, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("unexpected allow origin: %q", got)
	}

	req = httptest.NewRequest(http.MethodOptions, "https://api.example.com/v1/workspace", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected empty-origin preflight to pass, got %d", recorder.Code)
	}
}

func TestCSRFProtectionRejectsCrossSiteMutation(t *testing.T) {
	handler := CSRFProtection(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/payments", nil)
	req.Host = "api.example.com"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden CSRF rejection, got %d", recorder.Code)
	}
}

func TestCSRFProtectionAllowsSafeRead(t *testing.T) {
	handler := CSRFProtection(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected safe method to pass, got %d", recorder.Code)
	}
}

func TestRequestFingerprintHashesSensitiveHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/workspace", nil)
	req.Header.Set("Authorization", "Bearer secret-token")

	fingerprint := requestFingerprint(req)

	if !strings.HasPrefix(fingerprint, "auth:") {
		t.Fatalf("expected auth fingerprint, got %q", fingerprint)
	}
	if strings.Contains(fingerprint, "secret-token") {
		t.Fatalf("fingerprint leaked raw token: %q", fingerprint)
	}
}

func TestInputValidationRejectsOversizeAndBadContentType(t *testing.T) {
	handler := InputValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/workspace", nil)
	req.ContentLength = 16 * 1024 * 1024
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversize rejection, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodPatch, "https://api.example.com/v1/workspace", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid content type rejection, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "https://api.example.com/v1/workspace", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected valid JSON request to pass, got %d", recorder.Code)
	}
}

func TestJWTAuthBypassRejectAndContextPropagation(t *testing.T) {
	manager, err := auth.NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager: %v", err)
	}
	token, err := manager.GenerateAccessToken(auth.Claims{
		UserID:         "usr_1",
		OrganizationID: "org_1",
		Role:           "operator",
		SessionID:      "sess_1",
		Capabilities:   []string{"orders.write"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	handler := JWTAuth(manager, []string{"/public"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetUserIDFromContext(r.Context()) != "usr_1" {
			t.Fatalf("missing user id in context")
		}
		if GetOrganizationIDFromContext(r.Context()) != "org_1" {
			t.Fatalf("missing organization id in context")
		}
		if GetRoleFromContext(r.Context()) != "operator" {
			t.Fatalf("missing role in context")
		}
		if got := GetCapabilitiesFromContext(r.Context()); len(got) != 1 || got[0] != "orders.write" {
			t.Fatalf("unexpected capabilities: %#v", got)
		}
		if GetSessionIDFromContext(r.Context()) != "sess_1" {
			t.Fatalf("missing session id in context")
		}
		if GetAccessExpiresAtFromContext(r.Context()) == "" || GetRefreshExpiresAtFromContext(r.Context()) == "" {
			t.Fatalf("expected access and refresh expiry context")
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected authorized request, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing auth rejection, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/public/status", nil)
	recorder = httptest.NewRecorder()
	JWTAuth(manager, []string{"/public"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected public path bypass, got %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	JWTAuth(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("expected nil manager bypass, got %d", recorder.Code)
	}
}

// TestJWTAuthAcceptsWebSocketQueryToken covers the browser WebSocket
// handshake: it cannot set an Authorization header, so the credential rides
// the access_token query parameter — accepted for upgrade requests only.
func TestJWTAuthAcceptsWebSocketQueryToken(t *testing.T) {
	manager, err := auth.NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager: %v", err)
	}
	token, err := manager.GenerateAccessToken(auth.Claims{
		UserID:         "usr_ws",
		OrganizationID: "org_ws",
	}, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	handler := JWTAuth(manager, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetOrganizationIDFromContext(r.Context()) != "org_ws" {
			t.Fatalf("missing organization from websocket query token")
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/projections/profile/chow_profiles?access_token="+token, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusSwitchingProtocols {
		t.Fatalf("expected websocket upgrade with query token to authenticate, got %d", recorder.Code)
	}

	// An ordinary request must not authenticate via query parameter — tokens
	// in URLs leak into logs and caches, so the channel is upgrade-only.
	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders?access_token="+token, nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected non-upgrade query token to be rejected, got %d", recorder.Code)
	}
}

// TestOptionalJWTAuthIdentityAndFailClosedCredentials covers the development
// posture: no credential proceeds anonymously, a valid credential establishes
// identity, and a presented-but-invalid credential still fails closed.
func TestOptionalJWTAuthIdentityAndFailClosedCredentials(t *testing.T) {
	manager, err := auth.NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager: %v", err)
	}
	token, err := manager.GenerateAccessToken(auth.Claims{
		UserID:         "usr_opt",
		OrganizationID: "org_opt",
	}, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	var gotUser string
	handler := OptionalJWTAuth(manager, []string{"/public"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = GetUserIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil))
	if recorder.Code != http.StatusNoContent || gotUser != "" {
		t.Fatalf("expected anonymous pass-through, got status %d user %q", recorder.Code, gotUser)
	}

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent || gotUser != "usr_opt" {
		t.Fatalf("expected identity established, got status %d user %q", recorder.Code, gotUser)
	}

	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid credential to fail closed, got %d", recorder.Code)
	}

	// A public path stays reachable even with a stale credential riding along.
	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/public/status", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected public path to serve despite stale credential, got %d", recorder.Code)
	}
}

func TestRequireCapabilitiesAndContextHelpers(t *testing.T) {
	authorizer := NewAuthorizer([]RoleTemplate{{Role: "operator", Capabilities: []string{"orders.write"}}})
	protected := RequireCapabilities(authorizer, "orders.write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	ctx := ContextWithRole(context.Background(), "operator")
	ctx = ContextWithCapabilities(ctx, []string{"metrics.view"})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/orders", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected role capability to pass, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/orders", nil)
	recorder = httptest.NewRecorder()
	protected.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected missing capability rejection, got %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	RequireCapabilities(nil, "orders.write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected nil authorizer bypass, got %d", recorder.Code)
	}

	ctx = ContextWithUserID(context.Background(), "usr_2")
	ctx = ContextWithOrganizationID(ctx, "org_2")
	ctx = ContextWithSessionID(ctx, "sess_2")
	ctx = ContextWithAccessExpiresAt(ctx, "access")
	ctx = ContextWithRefreshExpiresAt(ctx, "refresh")
	if GetUserIDFromContext(ctx) != "usr_2" ||
		GetOrganizationIDFromContext(ctx) != "org_2" ||
		GetSessionIDFromContext(ctx) != "sess_2" ||
		GetAccessExpiresAtFromContext(ctx) != "access" ||
		GetRefreshExpiresAtFromContext(ctx) != "refresh" {
		t.Fatalf("context helper round trip failed")
	}
	if GetUserIDFromContext(context.Background()) != "" || GetCapabilitiesFromContext(context.Background()) != nil {
		t.Fatalf("expected empty defaults for missing context values")
	}
}

// TestRateLimiterRejectionsDoNotExtendWindow guards recovery: a client that
// is over the limit and keeps retrying must be admitted again once its
// *allowed* requests age out — rejections themselves must not be recorded, or
// a retry loop throttles itself indefinitely.
func TestRateLimiterRejectionsDoNotExtendWindow(t *testing.T) {
	limiter := NewRateLimiter(2, 40*time.Millisecond)
	if !limiter.Allow("client") || !limiter.Allow("client") {
		t.Fatalf("expected budget of 2 to be admitted")
	}
	for i := 0; i < 20; i++ {
		if limiter.Allow("client") {
			t.Fatalf("expected over-limit request %d to be rejected", i)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if !limiter.Allow("client") {
		t.Fatalf("expected client to recover after window despite rejected retries")
	}
}

func TestRateLimiterClientIPAndFingerprintFallbacks(t *testing.T) {
	limiter := NewRateLimiter(1, 10*time.Millisecond)
	if !limiter.Allow("client") {
		t.Fatalf("expected first request to pass")
	}
	if limiter.Allow("client") {
		t.Fatalf("expected second request in window to fail")
	}
	time.Sleep(15 * time.Millisecond)
	if !limiter.Allow("client") {
		t.Fatalf("expected request after window to pass")
	}

	handler := NewRateLimiter(1, time.Minute).Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.RemoteAddr = "203.0.113.10:443"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected first handler request to pass, got %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit rejection, got %d", recorder.Code)
	}

	if requestFingerprint(nil) != "ip:unknown" {
		t.Fatalf("expected nil request fingerprint fallback")
	}
	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.Header.Set("X-API-Key", "api-secret")
	if got := requestFingerprint(req); !strings.HasPrefix(got, "apikey:") || strings.Contains(got, "api-secret") {
		t.Fatalf("unexpected api key fingerprint: %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 198.51.100.2")
	if got := GetClientIP(req); got != "198.51.100.1" {
		t.Fatalf("unexpected xff client ip: %q", got)
	}
	req.Header.Del("X-Forwarded-For")
	req.Header.Set("X-Real-IP", "198.51.100.3")
	if got := GetClientIP(req); got != "198.51.100.3" {
		t.Fatalf("unexpected real ip: %q", got)
	}
	if got := GetClientIP(nil); got != "" {
		t.Fatalf("expected empty nil client ip, got %q", got)
	}
}

func TestPublicPathAndOriginHelpers(t *testing.T) {
	if !isPublicPath("/healthz/live", nil) || !isPublicPath("/custom/status", []string{"/custom"}) {
		t.Fatalf("expected public path match")
	}
	if isPublicPath("", []string{"/"}) || isPublicPath("/private", []string{"/custom"}) {
		t.Fatalf("expected private path")
	}
	// The server root is public by exact match (it serves API docs), but that
	// must not leak to sub-paths — "/" is never a prefix-public entry.
	if !isPublicPath("/", nil) {
		t.Fatalf("expected root to be public")
	}
	if isPublicPath("/private", nil) {
		t.Fatalf("root being public must not make sub-paths public")
	}
	if !isOriginAllowed("https://app.example.com", []string{"*"}) {
		t.Fatalf("expected wildcard origin allow")
	}
	if isOriginAllowed("", []string{"*"}) || isOriginAllowed("https://evil.example.com", []string{"https://app.example.com"}) {
		t.Fatalf("expected origin denial")
	}
}
