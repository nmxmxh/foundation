package httpapi

import "testing"

func TestMakeEventRouteWithOptions(t *testing.T) {
	route := MakeEventRoute(
		"get",
		"/v1/media/assets",
		"media:list_assets:v1:requested",
		"List registered media assets.",
		"ListAssetsRequest",
		"ListAssetsResponse",
		WithRequiredQueryParams("workspace_id", "workspace_id", " "),
		WithAnyOfQueryParams("page_size", "cursor", "page_size"),
	)

	if route.Method != "GET" {
		t.Fatalf("expected uppercased method")
	}
	if len(route.RequiredQueryParams) != 1 || route.RequiredQueryParams[0] != "workspace_id" {
		t.Fatalf("unexpected required query params: %+v", route.RequiredQueryParams)
	}
	if len(route.AnyOfQueryParams) != 1 || len(route.AnyOfQueryParams[0]) != 2 {
		t.Fatalf("unexpected any-of query params: %+v", route.AnyOfQueryParams)
	}
	if route.RequiredCapability != "media.list_assets" {
		t.Fatalf("unexpected inferred capability: %s", route.RequiredCapability)
	}
	if route.Permission != "view" {
		t.Fatalf("unexpected inferred permission: %s", route.Permission)
	}
}

func TestMakeEventRouteRBACOverride(t *testing.T) {
	route := MakeEventRoute(
		"post",
		"/v1/publish/schedules",
		"publish:create_schedule:v1:requested",
		"Create schedule",
		"CreateScheduleRequest",
		"CreateScheduleResponse",
		WithRBAC("publish.admin_override", "admin"),
	)
	if route.RequiredCapability != "publish.admin_override" {
		t.Fatalf("expected capability override")
	}
	if route.Permission != "admin" {
		t.Fatalf("expected admin permission override")
	}
}

func TestStaticRouteBuildsScaffoldHandler(t *testing.T) {
	route := StaticRoute(
		"GET",
		"/v1/media/assets",
		"media:list_assets:v1:requested",
		"List assets",
		"ListAssetsRequest",
		"ListAssetsResponse",
		"media",
		map[string]any{"kind": "scaffold"},
	)

	if route.Handler == nil {
		t.Fatalf("expected scaffold handler")
	}
	if route.StaticPayload["kind"] != "scaffold" {
		t.Fatalf("expected static payload on route metadata")
	}
}
