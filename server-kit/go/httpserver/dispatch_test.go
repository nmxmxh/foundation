package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

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
