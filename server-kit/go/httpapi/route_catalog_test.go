package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func TestBuildRouteCatalog_ProjectsAndSorts(t *testing.T) {
	t.Parallel()
	routes := []registry.HTTPRoute{
		MakeEventRoute("POST", "/v1/user/create", "user:create:v1:requested", "", "", ""),
		MakeEventRoute("GET", "/v1/user/list", "user:list_users:v1:requested", "", "", ""),
	}
	catalog := BuildRouteCatalog(routes)

	if catalog.SchemaVersion != RouteCatalogSchemaVersion {
		t.Errorf("schema=%q", catalog.SchemaVersion)
	}
	if len(catalog.Routes) != 2 {
		t.Fatalf("routes=%d want 2", len(catalog.Routes))
	}
	// Sorted by event type: user:create before user:list_users.
	if catalog.Routes[0].EventType != "user:create:v1:requested" {
		t.Errorf("first=%q want user:create", catalog.Routes[0].EventType)
	}
	// Permission projected to the client vocabulary.
	if catalog.Routes[1].Permission != "view" {
		t.Errorf("list permission=%q want view", catalog.Routes[1].Permission)
	}
	if catalog.Routes[0].Permission != "write" {
		t.Errorf("create permission=%q want write", catalog.Routes[0].Permission)
	}
	if catalog.Routes[0].RequiredCapability == "" {
		t.Error("capability should be derived when unset")
	}
}

func TestBuildRouteCatalog_SkipsIncompleteAndDeduplicates(t *testing.T) {
	t.Parallel()
	routes := []registry.HTTPRoute{
		{Method: "POST", Path: "/v1/a/create", EventType: "a:create:v1:requested"},
		{Method: "POST", Path: "/v1/a/create", EventType: "a:create:v1:requested"}, // dup method+path
		{Method: "POST", Path: "", EventType: "b:create:v1:requested"},              // no path
		{Method: "", Path: "/v1/c", EventType: "c:create:v1:requested"},             // no method
		{Method: "POST", Path: "/v1/d", EventType: ""},                              // no event
	}
	catalog := BuildRouteCatalog(routes)
	if len(catalog.Routes) != 1 {
		t.Fatalf("routes=%d want 1 (only the unique complete route)", len(catalog.Routes))
	}
	if catalog.Routes[0].EventType != "a:create:v1:requested" {
		t.Errorf("entry=%+v", catalog.Routes[0])
	}
}

func TestBuildRouteCatalog_IncludesCustomNonEventRoutes(t *testing.T) {
	t.Parallel()
	// A custom route (e.g. a transfer upload) carries a path and capability that a
	// proto event-pair could never derive; the catalog must still capture it.
	mgr := newRouteManager(t, nil)
	store := memoryStore(t)
	custom, err := MakeTransferRoute(baseConfig(mgr, store))
	if err != nil {
		t.Fatalf("MakeTransferRoute: %v", err)
	}
	catalog := BuildRouteCatalog([]registry.HTTPRoute{custom})
	if len(catalog.Routes) != 1 {
		t.Fatalf("routes=%d want 1", len(catalog.Routes))
	}
	entry := catalog.Routes[0]
	if entry.Path != "/media/upload" || entry.Method != "PUT" {
		t.Errorf("custom route projection=%+v", entry)
	}
}

func TestMarshalRouteCatalog_StableAndTrailingNewline(t *testing.T) {
	t.Parallel()
	routes := []registry.HTTPRoute{
		MakeEventRoute("POST", "/v1/user/create", "user:create:v1:requested", "", "", ""),
	}
	a, err := MarshalRouteCatalog(routes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := MarshalRouteCatalog(routes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Error("marshal must be deterministic")
	}
	if len(a) == 0 || a[len(a)-1] != '\n' {
		t.Error("output must end with a trailing newline")
	}
	// Round-trips back into the catalog type.
	var rt RouteCatalog
	if err := json.Unmarshal(a, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rt.Routes) != 1 || rt.Routes[0].EventType != "user:create:v1:requested" {
		t.Errorf("round-trip=%+v", rt)
	}
}
