package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// serverWithDispatch builds a server whose registry has a single handler for
// testEvent returning the given function's result.
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

// TestRunStartsAndShutsDown exercises the Run lifecycle: it starts listening on
// an ephemeral port and returns nil once the context is cancelled.
func TestRunStartsAndShutsDown(t *testing.T) {
	s := newSmokeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // let ListenAndServe bind
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

// TestStreamResponseNDJSON covers handleStreamResponse for a map stream: the
// handler returns a channel and the response is streamed as ndjson.
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

// TestStreamResponseUnsupportedType covers the fail-loud default: a handler
// returning a value the registry classifies as a stream handle but that the
// writer cannot serialize yields a 500 rather than an empty 200.
func TestStreamResponseUnsupportedType(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return make(<-chan int), nil // not a supported stream element type
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

// TestDispatchHandlerErrorWritesDomainError covers writeDispatchError: a handler
// that fails surfaces the domain error with the right status.
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

// TestOperationalEndpointsProtected covers the operational auth gate: with
// ProtectOperationalEndpoints set, /metricsz requires a valid bearer token.
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
	s.ConfigureAuth(jwt, nil, true) // rbac nil → authenticated is sufficient
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

// TestRouteRBACEnforced covers wrapRouteRBAC + enforceRBAC: a custom-handler
// route with a required capability is denied for an unauthenticated request.
func TestRouteRBACEnforced(t *testing.T) {
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true) // requireAuthForDispatch = true
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

// TestStreamResponseBytes covers handleStreamResponse for a []byte stream.
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

// TestStreamResponseStopsOnClientCancel covers the cancellation path of
// handleStreamResponse (TE-17): when the request context is cancelled mid-stream
// (the client disconnected), the streaming loop must stop rather than block
// forever on a never-closing handler channel. The handler signals once it has
// been invoked (so we cancel only after the stream loop is running), and the
// oracle is termination: ServeHTTP returns promptly after cancel.
func TestStreamResponseStopsOnClientCancel(t *testing.T) {
	ch := make(chan map[string]any) // open, never closes -> loop blocks unless cancelled
	streaming := make(chan struct{})
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		close(streaming) // dispatch reached; the stream loop is about to read ch
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

	<-streaming // ensure the stream loop is active before simulating client departure
	cancel()

	select {
	case <-done:
		// terminated on cancel as required
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not terminate after request context was cancelled")
	}
}
