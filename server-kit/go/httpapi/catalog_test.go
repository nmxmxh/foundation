package httpapi

import "testing"

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
		if route.Metadata["route_source"] != "event_type" {
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
