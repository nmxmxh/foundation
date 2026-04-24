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
