package transport

import (
	"strconv"
	"testing"
)

func BenchmarkCreateEnvelopeJSON(b *testing.B) {
	payload := ObjectFromMap(map[string]any{
		"workspace_id": "ws_1",
		"body":         "runtime-transport-go-payload",
	})
	extra := ObjectFromMap(map[string]any{"source": "bench"})
	b.ReportAllocs()
	for b.Loop() {
		envelope := CreateEnvelope("workspace:create:v1:requested", payload, extra)
		if envelope.Metadata.CorrelationID == "" {
			b.Fatal("missing correlation id")
		}
	}
}

func BenchmarkResolveRouteLinear16(b *testing.B) {
	routes := make([]Route, 16)
	for i := range routes {
		routes[i] = Route{
			Method:    "POST",
			Path:      "/runtime/dispatch",
			EventType: "runtime:event_" + string(rune('a'+i)) + ":v1:requested",
		}
	}
	target := routes[len(routes)-1].EventType
	b.ReportAllocs()
	for b.Loop() {
		if ResolveRoute(routes, target) == nil {
			b.Fatal("route not resolved")
		}
	}
}

func BenchmarkResolveRouteLinear1024(b *testing.B) {
	routes := make([]Route, 1024)
	for i := range routes {
		routes[i] = Route{
			Method:    "POST",
			Path:      "/runtime/dispatch",
			EventType: "runtime:event_" + strconv.Itoa(i) + ":v1:requested",
		}
	}
	target := routes[len(routes)-1].EventType
	b.ReportAllocs()
	for b.Loop() {
		if ResolveRoute(routes, target) == nil {
			b.Fatal("route not resolved")
		}
	}
}

func BenchmarkRouteIndexResolve1024(b *testing.B) {
	routes := make([]Route, 1024)
	for i := range routes {
		routes[i] = Route{
			Method:    "POST",
			Path:      "/runtime/dispatch",
			EventType: "runtime:event_" + strconv.Itoa(i) + ":v1:requested",
		}
	}
	index := NewRouteIndex(routes)
	target := routes[len(routes)-1].EventType
	b.ReportAllocs()
	for b.Loop() {
		if index.Resolve(target) == nil {
			b.Fatal("route not resolved")
		}
	}
}

func BenchmarkCanDispatchExactCapability(b *testing.B) {
	route := &Route{RequiredCapability: "workspace.write", Permission: "write"}
	capabilities := []string{"profile.view", "workspace.write", "billing.view"}
	allow := func(*Route) bool { return true }
	b.ReportAllocs()
	for b.Loop() {
		if !CanDispatch(route, capabilities, allow) {
			b.Fatal("dispatch denied")
		}
	}
}

func BenchmarkCanDispatchWriteViaAdminFallback(b *testing.B) {
	route := &Route{RequiredCapability: "workspace.write", Permission: "write"}
	capabilities := []string{"profile.view", "workspace.admin", "billing.view"}
	allow := func(*Route) bool { return true }
	b.ReportAllocs()
	for b.Loop() {
		if !CanDispatch(route, capabilities, allow) {
			b.Fatal("dispatch denied")
		}
	}
}

func BenchmarkSchemaRegistryNegotiate(b *testing.B) {
	registry := NewSchemaRegistry()
	for _, version := range []string{"v1", "v2", "v3"} {
		if err := registry.Register(SchemaVersion{
			EventType: "runtime:dispatch:v1:requested",
			Version:   version,
		}); err != nil {
			b.Fatal(err)
		}
	}
	accepted := []string{"v4", "v3", "v2"}
	b.ReportAllocs()
	for b.Loop() {
		schema, err := registry.Negotiate("runtime:dispatch:v1:requested", accepted)
		if err != nil {
			b.Fatal(err)
		}
		if schema.Version != "v3" {
			b.Fatalf("version = %s", schema.Version)
		}
	}
}
