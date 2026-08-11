package httpapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

// RoutesFromHandlerMap derives deterministic HTTP routes from a service handler
// map keyed by event_type. It is the canonical fallback catalogue for dynamic
// map-based services whose HTTP shape can be inferred from event names.
func RoutesFromHandlerMap[T any](handlers map[string]T) []registry.HTTPRoute {
	eventTypes := make([]string, 0, len(handlers))
	for eventType := range handlers {
		eventType = strings.TrimSpace(eventType)
		if eventType != "" {
			eventTypes = append(eventTypes, eventType)
		}
	}
	return RoutesFromEventTypes(eventTypes)
}

// RoutesFromEventTypes derives deterministic HTTP routes from event_type names.
// Explicit project route catalogues should still be used when a domain needs
// richer typed schemas, custom paths, streaming, raw bodies, or RBAC overrides.
func RoutesFromEventTypes(eventTypes []string) []registry.HTTPRoute {
	eventTypes = append([]string(nil), eventTypes...)
	sort.Strings(eventTypes)

	routes := make([]registry.HTTPRoute, 0, len(eventTypes))
	seen := make(map[string]struct{}, len(eventTypes))
	for _, eventType := range eventTypes {
		eventType = strings.TrimSpace(eventType)
		if eventType == "" {
			continue
		}
		if _, ok := seen[eventType]; ok {
			continue
		}
		seen[eventType] = struct{}{}
		routes = append(routes, RouteFromEventType(eventType))
	}
	return routes
}

// DerivedRouteSource marks a route whose method and path were inferred from its
// event_type rather than stated by the project. See MergeRoutes.
const DerivedRouteSource = "event_type"

// MergeRoutes reconciles derived routes with the ones a project states by hand,
// so an event_type names exactly one route.
//
// The two are produced independently and both are legitimate. A project lists
// its handlers in one map, from which RoutesFromHandlerMap derives a
// conventional route for every event; separately it states routes for the
// events that need a custom path, their own middleware, or a rate limiter. What
// nothing did was subtract the first from the second, so an event with a stated
// route was published twice — once at the path a client actually calls, once at
// the derived path, which reaches the same handler with none of the wrapping.
//
// Two routes for one event is not a cosmetic redundancy. The generated frontend
// registry (@ovasabi/runtime-transport createRouteRegistry) keys routes by event
// type and refuses to build on a duplicate, so it takes down every dispatch in
// the browser at import — not just its own. On the server it publishes a second
// door to a handler whose stated door was wrapped for a reason.
//
// The stated route wins. It carries the path a client already calls and the
// handler the project chose; the derived route is the convention fallback for
// events nobody gave a path. Routes are identified as derived by the
// route_source metadata RouteFromEventType stamps, so a project that states a
// route never has to say so twice.
//
// Only the fallback is dropped. Two *stated* routes for one event are a
// conflict this cannot resolve — nothing distinguishes them, and picking by
// declaration order would decide a contract by the order of two appends — so
// both survive here and ValidateRouteCatalog reports them. Order is otherwise
// preserved, and routes without an event_type, served directly by their own
// handler, are all kept.
func MergeRoutes(derived []registry.HTTPRoute, stated ...[]registry.HTTPRoute) []registry.HTTPRoute {
	combined := make([]registry.HTTPRoute, 0, len(derived))
	combined = append(combined, derived...)
	for _, routes := range stated {
		combined = append(combined, routes...)
	}

	claimed := make(map[string]struct{}, len(combined))
	for _, route := range combined {
		eventType := strings.TrimSpace(route.EventType)
		if eventType != "" && !isDerivedRoute(route) {
			claimed[eventType] = struct{}{}
		}
	}

	merged := make([]registry.HTTPRoute, 0, len(combined))
	for _, route := range combined {
		eventType := strings.TrimSpace(route.EventType)
		if eventType != "" && isDerivedRoute(route) {
			if _, stated := claimed[eventType]; stated {
				continue
			}
		}
		merged = append(merged, route)
	}
	return merged
}

// isDerivedRoute reports whether a route's shape was inferred from its event
// name rather than stated by the project.
func isDerivedRoute(route registry.HTTPRoute) bool {
	source, _ := route.Metadata.GetString("route_source")
	return source == DerivedRouteSource
}

// RouteFromEventType derives a conventional REST route from an event_type.
func RouteFromEventType(eventType string) registry.HTTPRoute {
	eventType = strings.TrimSpace(eventType)
	parts := strings.Split(eventType, ":")
	if len(parts) < 4 {
		return MakeEventRoute("POST", "/v1/dispatch", eventType, eventType, "", "")
	}

	domain := strings.TrimSpace(parts[0])
	actionParts := actionPartsFromEvent(parts)
	method := methodForAction(actionParts)
	description := fmt.Sprintf("%s %s.", titleWords(domain), strings.Join(actionParts, " "))
	action := strings.Join(actionParts, ":")
	return MakeEventRoute(
		method,
		"/v1/"+domain+"/"+strings.Join(actionParts, "/"),
		eventType,
		description,
		"",
		"",
		WithTags(domain, actionParts[0]),
		WithMetadataObject(extension.Object{
			"route_source": extension.String("event_type"),
			"domain":       extension.String(domain),
			"action":       extension.String(action),
			"event_type":   extension.String(eventType),
		}),
	)
}

func actionPartsFromEvent(parts []string) []string {
	out := make([]string, 0, len(parts)-3)
	for _, part := range parts[1:] {
		if part == "v1" || part == "requested" {
			break
		}
		part = strings.TrimSpace(strings.ReplaceAll(part, "_", "-"))
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{"dispatch"}
	}
	return out
}

func methodForAction(parts []string) string {
	action := strings.Join(parts, ":")
	switch {
	case strings.HasPrefix(action, "get") || strings.Contains(action, ":get") ||
		strings.HasPrefix(action, "list") || strings.Contains(action, ":list") ||
		strings.Contains(action, "upcoming"):
		return "GET"
	case strings.HasPrefix(action, "delete") || strings.HasPrefix(action, "remove") ||
		strings.Contains(action, "disconnect") || strings.Contains(action, "cancel"):
		return "DELETE"
	case strings.HasPrefix(action, "update") || strings.Contains(action, "update") ||
		strings.HasPrefix(action, "resolve") || strings.HasPrefix(action, "accept") ||
		strings.HasPrefix(action, "reject") || strings.HasPrefix(action, "toggle") ||
		strings.HasPrefix(action, "activate") || strings.HasPrefix(action, "freeze") ||
		strings.HasPrefix(action, "mark") || strings.HasPrefix(action, "decide") ||
		strings.Contains(action, "upsert"):
		return "PATCH"
	default:
		return "POST"
	}
}

func titleWords(value string) string {
	words := strings.Fields(strings.ReplaceAll(value, "_", " "))
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
