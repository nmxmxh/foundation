package transport

import "fmt"

type SchemaVersion struct {
	EventType  string
	Version    string
	Deprecated bool
	Sunset     string
	Migration  string
}

type SchemaRegistry struct {
	versions map[string]map[string]SchemaVersion
}

func NewSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{versions: map[string]map[string]SchemaVersion{}}
}

func (r *SchemaRegistry) Register(schema SchemaVersion) error {
	if r == nil {
		return fmt.Errorf("schema registry is nil")
	}
	if schema.EventType == "" || schema.Version == "" {
		return fmt.Errorf("event type and version are required")
	}
	if r.versions[schema.EventType] == nil {
		r.versions[schema.EventType] = map[string]SchemaVersion{}
	}
	r.versions[schema.EventType][schema.Version] = schema
	return nil
}

func (r *SchemaRegistry) Negotiate(eventType string, accepted []string) (SchemaVersion, error) {
	if r == nil {
		return SchemaVersion{}, fmt.Errorf("schema registry is nil")
	}
	versions := r.versions[eventType]
	if len(versions) == 0 {
		return SchemaVersion{}, fmt.Errorf("event type %q is not registered", eventType)
	}
	for _, version := range accepted {
		if schema, ok := versions[version]; ok && !schema.Deprecated {
			return schema, nil
		}
	}
	for _, version := range accepted {
		if schema, ok := versions[version]; ok {
			return schema, nil
		}
	}
	return SchemaVersion{}, fmt.Errorf("no compatible schema version for %q", eventType)
}
