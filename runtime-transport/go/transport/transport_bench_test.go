package transport

import "testing"

func BenchmarkCreateEnvelopeJSON(b *testing.B) {
	payload := map[string]any{
		"workspace_id": "ws_1",
		"body":         "runtime-transport-go-payload",
	}
	extra := map[string]interface{}{"source": "bench"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		if ResolveRoute(routes, target) == nil {
			b.Fatal("route not resolved")
		}
	}
}

func BenchmarkCanDispatchExactCapability(b *testing.B) {
	route := &Route{RequiredCapability: "workspace.write", Permission: "write"}
	capabilities := []string{"profile.view", "workspace.write", "billing.view"}
	allow := func(*Route) bool { return true }
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		schema, err := registry.Negotiate("runtime:dispatch:v1:requested", accepted)
		if err != nil {
			b.Fatal(err)
		}
		if schema.Version != "v3" {
			b.Fatalf("version = %s", schema.Version)
		}
	}
}
