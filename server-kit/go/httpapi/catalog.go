package httpapi

import (
	"fmt"
	"sort"
	"strings"

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
		WithMetadata(map[string]any{
			"route_source": "event_type",
			"domain":       domain,
			"action":       action,
			"event_type":   eventType,
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
