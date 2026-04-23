package transport

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

type EnvelopeMetadata struct {
	CorrelationID  string                 `json:"correlation_id"`
	RequestID      string                 `json:"request_id"`
	IdempotencyKey string                 `json:"idempotency_key"`
	SchemaVersion  string                 `json:"schema_version"`
	Timestamp      time.Time              `json:"timestamp"`
	Extra          map[string]interface{} `json:"extra,omitempty"`
}

type PayloadEncoding string

const (
	PayloadEncodingJSON     PayloadEncoding = "json"
	PayloadEncodingProtobuf PayloadEncoding = "protobuf"
)

type Envelope struct {
	EventType       string           `json:"event_type"`
	Payload         map[string]any   `json:"payload"`
	PayloadEncoding PayloadEncoding  `json:"payload_encoding"`
	Metadata        EnvelopeMetadata `json:"metadata"`
}

type Route struct {
	Method             string   `json:"method"`
	Path               string   `json:"path"`
	EventType          string   `json:"event_type"`
	RequiredCapability string   `json:"required_capability"`
	Permission         string   `json:"permission"`
	TransportOrder     []string `json:"transport_order"`
}

func CreateEnvelope(eventType string, payload map[string]any, extra map[string]interface{}) Envelope {
	now := time.Now().UTC()
	token := fmt.Sprintf("%d_%08x", now.UnixMilli(), rand.Uint32())
	if payload == nil {
		payload = map[string]any{}
	}
	return Envelope{
		EventType:       eventType,
		Payload:         payload,
		PayloadEncoding: PayloadEncodingJSON,
		Metadata: EnvelopeMetadata{
			CorrelationID:  "corr_" + token,
			RequestID:      "req_" + token,
			IdempotencyKey: "idem_" + token,
			SchemaVersion:  EnvelopeSchemaVersion,
			Timestamp:      now,
			Extra:          extra,
		},
	}
}

func ResolveRoute(routes []Route, eventType string) *Route {
	for index := range routes {
		if routes[index].EventType == eventType {
			return &routes[index]
		}
	}
	return nil
}

func CanDispatch(route *Route, grantedCapabilities []string, hasPolicyAccess func(route *Route) bool) bool {
	if route == nil || !hasPolicyAccess(route) {
		return false
	}
	if route.RequiredCapability == "" {
		return true
	}
	for _, capability := range grantedCapabilities {
		if capability == "*" || capability == route.RequiredCapability {
			return true
		}
	}
	domain := strings.Split(route.RequiredCapability, ".")[0]
	if domain == "" {
		return false
	}
	for _, capability := range grantedCapabilities {
		if capability == domain+".*" {
			return true
		}
	}
	switch route.Permission {
	case "view":
		for _, capability := range grantedCapabilities {
			if capability == domain+".view" || capability == domain+".write" || capability == domain+".admin" {
				return true
			}
		}
	case "write":
		for _, capability := range grantedCapabilities {
			if capability == domain+".write" || capability == domain+".admin" {
				return true
			}
		}
	default:
		for _, capability := range grantedCapabilities {
			if capability == domain+".admin" {
				return true
			}
		}
	}
	return false
}
