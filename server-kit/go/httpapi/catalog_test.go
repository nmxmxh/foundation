package httpapi

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func TestRoutesFromHandlerMapDerivesStableCatalogue(t *testing.T) {
	routes := RoutesFromHandlerMap(map[string]func(){
		"identity:create_user:v1:requested": nil,
		"identity:get_user:v1:requested":    nil,
		"identity:update_user:v1:requested": nil,
		"identity:remove_user:v1:requested": nil,
	})
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	got := map[string]string{}
	for _, route := range routes {
		got[route.EventType] = route.Method + " " + route.Path
	}
	for eventType, want := range map[string]string{
		"identity:create_user:v1:requested": "POST /v1/identity/create-user",
		"identity:get_user:v1:requested":    "GET /v1/identity/get-user",
		"identity:update_user:v1:requested": "PATCH /v1/identity/update-user",
		"identity:remove_user:v1:requested": "DELETE /v1/identity/remove-user",
	} {
		if got[eventType] != want {
			t.Fatalf("%s: got %q want %q", eventType, got[eventType], want)
		}
	}
	for _, route := range routes {
		routeSource, _ := route.Metadata.GetString("route_source")
		if routeSource != "event_type" {
			t.Fatalf("expected event_type route metadata, got %+v", route.Metadata)
		}
		if len(route.Tags) == 0 || route.Tags[0] != "identity" {
			t.Fatalf("expected domain tag, got %+v", route.Tags)
		}
	}
}

func TestRoutesFromEventTypesDedupesAndSorts(t *testing.T) {
	routes := RoutesFromEventTypes([]string{
		"z:create:v1:requested",
		"a:create:v1:requested",
		"a:create:v1:requested",
		"",
	})
	if len(routes) != 2 {
		t.Fatalf("expected deduped routes, got %d", len(routes))
	}
	if routes[0].EventType != "a:create:v1:requested" {
		t.Fatalf("routes not sorted: %+v", routes)
	}
}

// A stated route displaces the fallback derived from the same event name.
//
// The two arrive from different places — the handler map derives one, the
// project states the other — and both used to reach the catalogue, which is how
// an auth surface came to be published at both /v1/auth/login and the derived
// /v1/pronto/auth/login: two doors to one handler, only one of them wrapped in
// the credential rate limiter, and a duplicate the browser registry refuses to
// build from.
func TestMergeRoutesPrefersStatedOverDerived(t *testing.T) {
	derived := RoutesFromEventTypes([]string{
		"identity:login:v1:requested",
		"identity:get_user:v1:requested",
	})
	stated := []registry.HTTPRoute{
		MakeEventRoute("POST", "/v1/login", "identity:login:v1:requested", "", "", ""),
	}

	merged := MergeRoutes(derived, stated)

	if len(merged) != 2 {
		t.Fatalf("routes=%d want 2: %+v", len(merged), merged)
	}
	byEvent := map[string]string{}
	for _, route := range merged {
		if _, dup := byEvent[route.EventType]; dup {
			t.Fatalf("%s survived twice: %+v", route.EventType, merged)
		}
		byEvent[route.EventType] = route.Method + " " + route.Path
	}
	if got := byEvent["identity:login:v1:requested"]; got != "POST /v1/login" {
		t.Errorf("login=%q want the stated route", got)
	}
	// The event nobody stated keeps its convention route.
	if got := byEvent["identity:get_user:v1:requested"]; got != "GET /v1/identity/get-user" {
		t.Errorf("get_user=%q want the derived route", got)
	}
}

// Routes with no event type are served directly by their own handler and are
// never in competition with anything.
func TestMergeRoutesKeepsEventlessRoutes(t *testing.T) {
	stated := []registry.HTTPRoute{
		{Method: "GET", Path: "/robots.txt"},
		{Method: "GET", Path: "/sitemap.xml"},
	}

	merged := MergeRoutes(nil, stated)

	if len(merged) != 2 {
		t.Fatalf("routes=%d want 2: %+v", len(merged), merged)
	}
}

// Two stated routes for one event cannot be resolved here: nothing tells them
// apart, and deciding a published contract by which append ran last would be
// worse than saying so. They survive for ValidateRouteCatalog to report.
func TestMergeRoutesKeepsStatedConflictsForValidation(t *testing.T) {
	stated := []registry.HTTPRoute{
		MakeEventRoute("POST", "/v1/login", "identity:login:v1:requested", "", "", ""),
		MakeEventRoute("POST", "/v1/session", "identity:login:v1:requested", "", "", ""),
	}

	merged := MergeRoutes(nil, stated)

	if len(merged) != 2 {
		t.Fatalf("routes=%d want both stated routes kept: %+v", len(merged), merged)
	}
}
