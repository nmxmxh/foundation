package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
