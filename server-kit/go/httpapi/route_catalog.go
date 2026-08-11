package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ErrDuplicateRouteEventType reports a catalog that names one event_type twice.
//
// The clients generated from this catalog index routes by event type — the
// browser registry (@ovasabi/runtime-transport createRouteRegistry) throws on a
// duplicate while *building*, which takes down every dispatch in the app, not
// only the ambiguous one. A catalog carrying that ambiguity is unusable, and it
// must not be possible to write one to disk and find out at import time in
// someone's browser. MarshalRouteCatalog refuses instead.
var ErrDuplicateRouteEventType = errors.New("route catalog names an event_type more than once")

// BuildRouteCatalog projects the registered routes into the client catalog. It
// is pure and deterministic: a stated route displaces the fallback derived from
// its event name (see MergeRoutes), what remains is de-duplicated by
// method+path, the permission is normalized to the view/write/admin vocabulary
// the client understands, and the result is sorted by event type then
// method+path so the generated artifact is stable across runs.
func BuildRouteCatalog(routes []registry.HTTPRoute) RouteCatalog {
	routes = MergeRoutes(routes)

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

// ValidateRouteCatalog reports a catalog the generated clients cannot index.
//
// One rule: an event_type names at most one route. MergeRoutes already removes
// the common cause — a stated route and the fallback derived from the same
// event — so what reaches here is a genuine conflict between two hand-written
// routes, which nothing but the author can settle.
func ValidateRouteCatalog(catalog RouteCatalog) error {
	seen := make(map[string]string, len(catalog.Routes))
	var conflicts []string
	for _, entry := range catalog.Routes {
		if first, dup := seen[entry.EventType]; dup {
			conflicts = append(conflicts, fmt.Sprintf(
				"%s (%s and %s %s)", entry.EventType, first, entry.Method, entry.Path))
			continue
		}
		seen[entry.EventType] = entry.Method + " " + entry.Path
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("%w: %s", ErrDuplicateRouteEventType, strings.Join(conflicts, ", "))
}

// MarshalRouteCatalog serializes the catalog as stable, indented JSON with a
// trailing newline so it round-trips cleanly through the generator's staleness
// check and version control. Apps call this from a small route-catalog command.
//
// It validates before serializing, so a catalog the clients cannot index fails
// the build that produced it rather than the browser that loads it.
func MarshalRouteCatalog(routes []registry.HTTPRoute) ([]byte, error) {
	catalog := BuildRouteCatalog(routes)
	if err := ValidateRouteCatalog(catalog); err != nil {
		return nil, err
	}
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
