package transport

import (
	"crypto/rand"
	"encoding/hex"
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
	correlationID := NewCorrelationID()
	if payload == nil {
		payload = map[string]any{}
	}
	return Envelope{
		EventType:       eventType,
		Payload:         payload,
		PayloadEncoding: PayloadEncodingJSON,
		Metadata: EnvelopeMetadata{
			CorrelationID:  correlationID,
			RequestID:      correlationID,
			IdempotencyKey: "idem_" + strings.TrimPrefix(correlationID, "corr_"),
			SchemaVersion:  EnvelopeSchemaVersion,
			Timestamp:      now,
			Extra:          extra,
		},
	}
}

func NewCorrelationID() string {
	var random [8]byte
	now := time.Now().UTC()
	if _, err := rand.Read(random[:]); err == nil {
		var storage [len("corr_") + len("20060102T150405.000000000") + 1 + 16]byte
		buf := storage[:0]
		buf = append(buf, "corr_"...)
		buf = now.AppendFormat(buf, "20060102T150405.000000000")
		buf = append(buf, '_')
		offset := len(buf)
		buf = buf[:offset+hex.EncodedLen(len(random))]
		hex.Encode(buf[offset:], random[:])
		return string(buf)
	}
	var storage [len("corr_") + len("20060102T150405.000000000")]byte
	buf := storage[:0]
	buf = append(buf, "corr_"...)
	buf = now.AppendFormat(buf, "20060102T150405.000000000")
	return string(buf)
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
	domain, _, _ := strings.Cut(route.RequiredCapability, ".")
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
