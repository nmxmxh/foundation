package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

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

// TestServerHandlerWiresHealthAndMiddleware exercises the assembled handler: the
// health endpoints, unknown-route handling, and the security/middleware stack —
// the foundation runtime wiring that now lives in this package.
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

// TestConfigureProjectionGatewayMounts verifies the mount seam: once configured,
// the projection path is served (not 404).
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
