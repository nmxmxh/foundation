package httpapi

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// RouteCatalogSchemaVersion identifies the on-disk catalog contract. Bump it
// when the entry shape changes so stale frontend artifacts are caught by the
// generator's --check gate.
const RouteCatalogSchemaVersion = "1.0"

// RouteCatalogEntry is the client-facing projection of a registered HTTPRoute.
// It carries exactly the fields the runtime-transport RuntimeRoute needs, so the
// frontend route registry can be generated from the backend's authoritative
// route set rather than re-deriving method/capability/permission in JS (which
// would drift from catalog.go and miss hand-registered routes such as the
// transfer/upload routes).
type RouteCatalogEntry struct {
	Method             string `json:"method"`
	Path               string `json:"path"`
	EventType          string `json:"event_type"`
	RequiredCapability string `json:"required_capability"`
	Permission         string `json:"permission"`
}

// RouteCatalog is the serialized, deterministic catalog consumed by
// generate_frontend_commands.mjs.
type RouteCatalog struct {
	SchemaVersion string              `json:"schema_version"`
	GeneratedBy   string              `json:"generated_by"`
	Routes        []RouteCatalogEntry `json:"routes"`
}

// BuildRouteCatalog projects the registered routes into the client catalog. It
// is pure and deterministic: entries are de-duplicated by method+path, the
// permission is normalized to the view/write/admin vocabulary the client
// understands, and the result is sorted by event type then method+path so the
// generated artifact is stable across runs.
func BuildRouteCatalog(routes []registry.HTTPRoute) RouteCatalog {
	entries := make([]RouteCatalogEntry, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		eventType := strings.TrimSpace(route.EventType)
		method := strings.ToUpper(strings.TrimSpace(route.Method))
		path := strings.TrimSpace(route.Path)
		if eventType == "" || method == "" || path == "" {
			continue
		}
		key := method + " " + path
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		entries = append(entries, RouteCatalogEntry{
			Method:             method,
			Path:               path,
			EventType:          eventType,
			RequiredCapability: capabilityFor(route),
			Permission:         permissionFor(route),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].EventType != entries[j].EventType {
			return entries[i].EventType < entries[j].EventType
		}
		if entries[i].Method != entries[j].Method {
			return entries[i].Method < entries[j].Method
		}
		return entries[i].Path < entries[j].Path
	})

	return RouteCatalog{
		SchemaVersion: RouteCatalogSchemaVersion,
		GeneratedBy:   "server-kit/go/httpapi.BuildRouteCatalog",
		Routes:        entries,
	}
}

// MarshalRouteCatalog serializes the catalog as stable, indented JSON with a
// trailing newline so it round-trips cleanly through the generator's staleness
// check and version control. Apps call this from a small route-catalog command.
func MarshalRouteCatalog(routes []registry.HTTPRoute) ([]byte, error) {
	catalog := BuildRouteCatalog(routes)
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// capabilityFor falls back to the event-derived capability when a route does not
// pin one explicitly, mirroring MakeEventRoute's defaulting.
func capabilityFor(route registry.HTTPRoute) string {
	if cap := strings.TrimSpace(route.RequiredCapability); cap != "" {
		return cap
	}
	return security.CapabilityFromEvent(route.EventType)
}

// permissionFor normalizes the route permission to view/write/admin, deriving it
// from the event type when unset.
func permissionFor(route registry.HTTPRoute) string {
	permission := security.NormalizePermission(route.Permission)
	if permission == "" {
		permission = security.PermissionFromEvent(route.EventType)
	}
	return permission
}
