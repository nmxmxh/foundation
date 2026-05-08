package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestRouteOptionsCoverRawStreamingHeadersAndStaticPayload(t *testing.T) {
	route := MakeEventRoute(
		" patch ",
		" /v1/assets ",
		"assets:update:v1:requested",
		" update ",
		" UpdateRequest ",
		" UpdateResponse ",
		WithRawBody(),
		WithStreaming(),
		WithRequestHeaders("X-Trace-ID", "X-Trace-ID", " "),
		WithRequiredCapability(" assets.override "),
		WithPermission("nonsense"),
		WithStaticPayload(map[string]any{" ": "ignored", "mode": "test"}),
	)
	if route.Method != "PATCH" || route.Path != "/v1/assets" || route.Description != "update" {
		t.Fatalf("route normalization failed: %+v", route)
	}
	if !route.IncludeRawBody || !route.IsStreaming {
		t.Fatalf("expected raw body and streaming route")
	}
	if len(route.IncludeHeaders) != 1 || route.IncludeHeaders[0] != "X-Trace-ID" {
		t.Fatalf("unexpected headers: %+v", route.IncludeHeaders)
	}
	if route.RequiredCapability != "assets.override" || route.Permission != "write" {
		t.Fatalf("unexpected RBAC: %q %q", route.RequiredCapability, route.Permission)
	}
	if route.StaticPayload["mode"] != "test" {
		t.Fatalf("static payload missing: %+v", route.StaticPayload)
	}
}

func TestEmptyRouteOptionsAreNoops(t *testing.T) {
	route := MakeEventRoute(
		"GET",
		"/v1/test",
		"test:ping:v1:requested",
		"Ping",
		"PingRequest",
		"PingResponse",
		WithAnyOfQueryParams(" ", ""),
		WithStaticPayload(nil),
		nil,
	)
	if len(route.AnyOfQueryParams) != 0 {
		t.Fatalf("empty any-of params should be ignored: %+v", route.AnyOfQueryParams)
	}
	if len(dedupeNonEmpty([]string{" ", "a", "a", "b"})) != 2 {
		t.Fatalf("dedupeNonEmpty did not dedupe")
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
	rec := httptest.NewRecorder()
	route.Handler(rec, httptest.NewRequest(http.MethodGet, route.Path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static route status = %d", rec.Code)
	}
	var body staticScaffoldResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode static response: %v", err)
	}
	if body.EventType != "media:list_assets:v1:requested" || body.Payload["kind"] != "scaffold" {
		t.Fatalf("unexpected static response: %+v", body)
	}
}
