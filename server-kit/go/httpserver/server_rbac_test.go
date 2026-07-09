package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// identityContext simulates the post-authentication request context the auth
// middleware would install: a user with a role and a set of granted capabilities.
func identityContext(userID, role string, capabilities []string) context.Context {
	ctx := security.ContextWithUserID(context.Background(), userID)
	ctx = security.ContextWithRole(ctx, role)
	return security.ContextWithCapabilities(ctx, capabilities)
}

// TestDispatchRBACAllowsAndDenies is the authorization boundary test for the
// dispatch path (TE-09/TE-19): with dispatch auth required and an authorizer
// installed, a caller whose granted capabilities cover the event's derived
// capability is allowed through to the handler, while a caller missing it is
// denied with insufficient_capability before the handler runs. Removing the
// enforceRBAC call would let the denied case reach the handler and return 200.
func TestDispatchRBACAllowsAndDenies(t *testing.T) {
	handlerRan := false
	s := serverWithDispatch(t, func(_ context.Context, _ extension.Object) (any, error) {
		handlerRan = true
		return map[string]any{"ok": true}, nil
	})
	s.ConfigureAuth(nil, security.NewAuthorizer(nil), true) // requireAuthForDispatch = true

	// testEvent is "demo:ping:v1:requested" -> capability "demo.ping".
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

// TestRouteRBACAllowsGrantedAndBypassesPublic complements TestRouteRBACEnforced
// (which proves denial): a capability-gated custom route admits a caller who
// holds the capability, and a public path bypasses RBAC entirely.
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

	// Authorized caller reaches the handler.
	req := httptest.NewRequest(http.MethodGet, "/v1/protected", nil)
	req = req.WithContext(identityContext("user_1", "member", []string{"orders.read"}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if reached != 1 {
		t.Fatalf("authorized handler reached %d times, want 1 (status=%d)", reached, rec.Code)
	}

	// Marking the path public bypasses RBAC: an unauthenticated request reaches it.
	s.AddPublicPath("/v1/protected")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/protected", nil))
	if reached != 2 {
		t.Fatalf("public path handler reached %d times, want 2 (status=%d)", reached, rec.Code)
	}
}

// TestIsPublicRouteBypassesAuth proves that a route flagged IsPublic bypasses
// the JWTAuth middleware under requireAuthForDispatch without an explicit
// AddPublicPath call: registerPublicRoutePaths propagates the route flag into
// s.publicPaths, and both "<path>" and "/api<path>" are exposed. This is the
// regression guard for public media/object serving 401ing under REQUIRE_AUTH,
// which <img> tags (unable to send bearer tokens) cannot recover from.
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

// TestTerminalEventType pins the lifecycle-suffix rewriter used to derive a
// terminal event type from a request/ack event: a recognized lifecycle suffix is
// replaced, and an event with no lifecycle suffix gets the terminal appended.
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
		{"orders:create:v1", "success", "orders:create:v1:success"}, // no suffix -> append
		{"  ", "success", ""}, // blank in -> trimmed empty, returned as-is
		{"orders:create:v1:requested", "  ", "orders:create:v1:requested"}, // blank terminal -> unchanged
	}
	for _, tc := range cases {
		if got := terminalEventType(tc.in, tc.terminal); got != tc.want {
			t.Fatalf("terminalEventType(%q,%q) = %q, want %q", tc.in, tc.terminal, got, tc.want)
		}
	}
}
