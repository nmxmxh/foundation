package transport

import "testing"

func TestSchemaRegistryNegotiatesPreferredSupportedVersion(t *testing.T) {
	registry := NewSchemaRegistry()
	_ = registry.Register(SchemaVersion{EventType: "order:create", Version: "v1", Deprecated: true, Sunset: "2026-06-01", Migration: "v2"})
	_ = registry.Register(SchemaVersion{EventType: "order:create", Version: "v2"})

	schema, err := registry.Negotiate("order:create", []string{"v2", "v1"})
	if err != nil {
		t.Fatalf("Negotiate() error = %v", err)
	}
	if schema.Version != "v2" {
		t.Fatalf("version = %s, want v2", schema.Version)
	}
}

func TestSchemaRegistryFallsBackToDeprecatedWhenNecessary(t *testing.T) {
	registry := NewSchemaRegistry()
	_ = registry.Register(SchemaVersion{EventType: "order:create", Version: "v1", Deprecated: true})
	schema, err := registry.Negotiate("order:create", []string{"v2", "v1"})
	if err != nil {
		t.Fatalf("Negotiate() error = %v", err)
	}
	if schema.Version != "v1" {
		t.Fatalf("version = %s, want v1", schema.Version)
	}
}

func TestSchemaRegistryRejectsInvalidRegistrationAndNegotiation(t *testing.T) {
	if err := (*SchemaRegistry)(nil).Register(SchemaVersion{EventType: "order:create", Version: "v1"}); err == nil {
		t.Fatal("expected nil registry registration to fail")
	}
	registry := NewSchemaRegistry()
	if err := registry.Register(SchemaVersion{Version: "v1"}); err == nil {
		t.Fatal("expected missing event type to fail")
	}
	if _, err := (*SchemaRegistry)(nil).Negotiate("order:create", []string{"v1"}); err == nil {
		t.Fatal("expected nil registry negotiation to fail")
	}
	if _, err := registry.Negotiate("missing:event", []string{"v1"}); err == nil {
		t.Fatal("expected unregistered event negotiation to fail")
	}
	if err := registry.Register(SchemaVersion{EventType: "order:create", Version: "v1"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Negotiate("order:create", []string{"v2"}); err == nil {
		t.Fatal("expected incompatible schema negotiation to fail")
	}
}
