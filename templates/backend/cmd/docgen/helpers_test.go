package main

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func TestDocgenCommandPackageCompiles(t *testing.T) {
	// This smoke test keeps doc generation code in the unit-test set while
	// allowing projects to customize helper behavior and proto domains.
}

func TestDocgenRegistersNamedRouteSchemas(t *testing.T) {
	spec := Generate(Config{
		Title:   "test",
		Version: "test",
		Routes: []registry.HTTPRoute{
			{
				Method:         "POST",
				Path:           "/v1/examples",
				EventType:      "example:create:v1:requested",
				Description:    "Create example.",
				RequestSchema:  "CreateExampleRequest",
				ResponseSchema: "CreateExampleResponse",
			},
		},
	})

	if _, ok := spec.Components.Schemas["CreateExampleRequest"]; !ok {
		t.Fatal("request schema ref was not registered")
	}
	if _, ok := spec.Components.Schemas["CreateExampleResponse"]; !ok {
		t.Fatal("response schema ref was not registered")
	}
}
