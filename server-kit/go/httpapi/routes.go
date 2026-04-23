package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

type staticScaffoldResponse struct {
	EventType      string         `json:"event_type"`
	Domain         string         `json:"domain"`
	Status         string         `json:"status"`
	RequestSchema  string         `json:"request_schema"`
	ResponseSchema string         `json:"response_schema"`
	Payload        map[string]any `json:"payload"`
}

// RouteOption mutates HTTP route metadata for docs/runtime wiring.
type RouteOption func(*registry.HTTPRoute)

func WithRequiredQueryParams(params ...string) RouteOption {
	normalized := dedupeNonEmpty(params)
	return func(route *registry.HTTPRoute) {
		route.RequiredQueryParams = append(route.RequiredQueryParams, normalized...)
	}
}

func WithAnyOfQueryParams(params ...string) RouteOption {
	normalized := dedupeNonEmpty(params)
	return func(route *registry.HTTPRoute) {
		if len(normalized) == 0 {
			return
		}
		route.AnyOfQueryParams = append(route.AnyOfQueryParams, normalized)
	}
}

func WithRequiredCapability(capability string) RouteOption {
	capability = strings.TrimSpace(capability)
	return func(route *registry.HTTPRoute) {
		route.RequiredCapability = capability
	}
}

func WithPermission(permission string) RouteOption {
	permission = security.NormalizePermission(permission)
	return func(route *registry.HTTPRoute) {
		route.Permission = permission
	}
}

func WithRBAC(capability, permission string) RouteOption {
	return func(route *registry.HTTPRoute) {
		WithRequiredCapability(capability)(route)
		WithPermission(permission)(route)
	}
}

func WithRawBody() RouteOption {
	return func(route *registry.HTTPRoute) {
		route.IncludeRawBody = true
	}
}

func WithStreaming() RouteOption {
	return func(route *registry.HTTPRoute) {
		route.IsStreaming = true
	}
}

func WithRequestHeaders(headers ...string) RouteOption {
	normalized := dedupeNonEmpty(headers)
	return func(route *registry.HTTPRoute) {
		route.IncludeHeaders = append(route.IncludeHeaders, normalized...)
	}
}

func WithStaticPayload(payload map[string]any) RouteOption {
	return func(route *registry.HTTPRoute) {
		if payload == nil {
			return
		}
		if route.StaticPayload == nil {
			route.StaticPayload = map[string]any{}
		}
		for key, value := range payload {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			route.StaticPayload[key] = value
		}
	}
}

func MakeEventRoute(method, path, eventType, description, requestSchema, responseSchema string, opts ...RouteOption) registry.HTTPRoute {
	route := registry.HTTPRoute{
		Method:         strings.ToUpper(strings.TrimSpace(method)),
		Path:           strings.TrimSpace(path),
		EventType:      strings.TrimSpace(eventType),
		Description:    strings.TrimSpace(description),
		RequestSchema:  strings.TrimSpace(requestSchema),
		ResponseSchema: strings.TrimSpace(responseSchema),
		StaticPayload:  map[string]any{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&route)
		}
	}
	if route.RequiredCapability == "" {
		route.RequiredCapability = security.CapabilityFromEvent(route.EventType)
	}
	if route.Permission == "" {
		route.Permission = security.PermissionFromEvent(route.EventType)
	}
	return route
}

// StaticRoute creates a scaffold route that returns a standard route metadata payload.
func StaticRoute(method, path, eventType, description, requestSchema, responseSchema, domain string, payload map[string]any, opts ...RouteOption) registry.HTTPRoute {
	route := MakeEventRoute(method, path, eventType, description, requestSchema, responseSchema, opts...)
	if payload != nil {
		WithStaticPayload(payload)(&route)
	}
	route.Handler = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := staticScaffoldResponse{
			EventType:      eventType,
			Domain:         domain,
			Status:         "scaffold",
			RequestSchema:  requestSchema,
			ResponseSchema: responseSchema,
			Payload:        payload,
		}
		_ = json.NewEncoder(w).Encode(response)
	}
	return route
}

func dedupeNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
