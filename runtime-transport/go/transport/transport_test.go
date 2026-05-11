package transport

import (
	"strings"
	"testing"
)

func TestCreateEnvelopeIncludesRequiredMetadata(t *testing.T) {
	envelope := CreateEnvelope("workspace.v1.created", map[string]any{"workspace_id": "ws_1"}, nil)
	if envelope.Metadata.CorrelationID == "" || envelope.Metadata.RequestID == "" || envelope.Metadata.IdempotencyKey == "" {
		t.Fatalf("envelope metadata is incomplete: %+v", envelope.Metadata)
	}
	if envelope.Metadata.RequestID != envelope.Metadata.CorrelationID {
		t.Fatalf("request_id = %q, want correlation_id %q", envelope.Metadata.RequestID, envelope.Metadata.CorrelationID)
	}
	if envelope.PayloadEncoding != PayloadEncodingJSON {
		t.Fatalf("payload encoding = %s, want %s", envelope.PayloadEncoding, PayloadEncodingJSON)
	}
	if envelope.Metadata.SchemaVersion != EnvelopeSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", envelope.Metadata.SchemaVersion, EnvelopeSchemaVersion)
	}
	if envelope.Metadata.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be set")
	}
}

func TestCreateEnvelopeNormalizesNilPayload(t *testing.T) {
	envelope := CreateEnvelope("workspace.v1.created", nil, map[string]interface{}{"source": "test"})
	if envelope.Payload == nil {
		t.Fatal("expected nil payload to be normalized to an empty map")
	}
	if envelope.Metadata.Extra["source"] != "test" {
		t.Fatalf("extra metadata not preserved: %+v", envelope.Metadata.Extra)
	}
	if !strings.HasPrefix(envelope.Metadata.IdempotencyKey, "idem_") {
		t.Fatalf("idempotency key = %q, want idem_ prefix", envelope.Metadata.IdempotencyKey)
	}
}

func TestResolveRouteReturnsMatchingRoute(t *testing.T) {
	routes := []Route{
		{EventType: "workspace:create:requested", Path: "/workspaces"},
		{EventType: "media:probe:requested", Path: "/media/probe"},
	}
	route := ResolveRoute(routes, "media:probe:requested")
	if route == nil || route.Path != "/media/probe" {
		t.Fatalf("ResolveRoute() = %+v, want media route", route)
	}
	if ResolveRoute(routes, "missing:event") != nil {
		t.Fatal("expected missing route to resolve nil")
	}
	index := NewRouteIndex(routes)
	if route := index.Resolve("media:probe:requested"); route == nil || route.Path != "/media/probe" {
		t.Fatalf("RouteIndex.Resolve() = %+v, want media route", route)
	}
	if index.Resolve("missing:event") != nil {
		t.Fatal("expected missing indexed route to resolve nil")
	}
	if (*RouteIndex)(nil).Resolve("media:probe:requested") != nil {
		t.Fatal("nil route index should resolve nil")
	}
}

func TestCanDispatchAuthorizationMatrix(t *testing.T) {
	policyAllow := func(_ *Route) bool { return true }
	policyDeny := func(_ *Route) bool { return false }
	cases := []struct {
		name         string
		route        *Route
		capabilities []string
		policy       func(*Route) bool
		want         bool
	}{
		{name: "nil route", route: nil, policy: policyAllow, want: false},
		{name: "policy denied", route: &Route{}, policy: policyDeny, want: false},
		{name: "no required capability", route: &Route{}, policy: policyAllow, want: true},
		{name: "exact capability", route: &Route{RequiredCapability: "workspace.write"}, capabilities: []string{"workspace.write"}, policy: policyAllow, want: true},
		{name: "wildcard capability", route: &Route{RequiredCapability: "workspace.write"}, capabilities: []string{"*"}, policy: policyAllow, want: true},
		{name: "domain wildcard", route: &Route{RequiredCapability: "workspace.write"}, capabilities: []string{"workspace.*"}, policy: policyAllow, want: true},
		{name: "view accepts write", route: &Route{RequiredCapability: "workspace.view", Permission: "view"}, capabilities: []string{"workspace.write"}, policy: policyAllow, want: true},
		{name: "write rejects view", route: &Route{RequiredCapability: "workspace.write", Permission: "write"}, capabilities: []string{"workspace.view"}, policy: policyAllow, want: false},
		{name: "admin fallback", route: &Route{RequiredCapability: "workspace.delete", Permission: "delete"}, capabilities: []string{"workspace.admin"}, policy: policyAllow, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDispatch(tc.route, tc.capabilities, tc.policy); got != tc.want {
				t.Fatalf("CanDispatch() = %v, want %v", got, tc.want)
			}
		})
	}
}
