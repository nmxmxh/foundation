package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

// DispatchRequest is the canonical request envelope used by HTTP ingress.
type DispatchRequest struct {
	EventType          string         `json:"event_type"`
	Payload            map[string]any `json:"payload"`
	PayloadBytes       []byte         `json:"-"`
	PayloadEncoding    string         `json:"payload_encoding,omitempty"`
	ResponseEncoding   string         `json:"-"`
	Metadata           map[string]any `json:"metadata"`
	CorrelationID      string         `json:"correlation_id"`
	SchemaVersion      string         `json:"schema_version"`
	Timestamp          string         `json:"timestamp"`
	RequiredCapability string         `json:"-"`
	RequiredPermission string         `json:"-"`
}

// DispatchExecutor executes one validated envelope request.
type DispatchExecutor func(http.ResponseWriter, *http.Request, DispatchRequest)

var pathParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// NewEventRouteHandler builds a route-level handler that maps HTTP requests to dispatch requests.
func NewEventRouteHandler(route registry.HTTPRoute, execute DispatchExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if execute == nil {
			domainerr.WriteHTTP(w, domainerr.Internal("route_executor_unavailable", "route executor unavailable"), domainerr.ResponseOptions{})
			return
		}

		if !strings.EqualFold(strings.TrimSpace(route.Method), r.Method) {
			domainerr.WriteHTTP(w, domainerr.Validation("method_not_allowed", "method not allowed"), domainerr.ResponseOptions{
				Status: http.StatusMethodNotAllowed,
			})
			return
		}

		req, err := BuildDispatchRequest(r, route)
		if err != nil {
			domainerr.WriteHTTP(w, domainerr.Validation("invalid_json", "invalid json"), domainerr.ResponseOptions{})
			return
		}
		execute(w, r, req)
	}
}

// BuildDispatchRequest converts an HTTP request into a dispatch envelope.
func BuildDispatchRequest(r *http.Request, route registry.HTTPRoute) (DispatchRequest, error) {
	payload, rawBody, payloadEncoding, responseEncoding, err := payloadFromRequest(r)
	if err != nil {
		return DispatchRequest{}, err
	}
	if payloadEncoding != "protobuf" {
		appendPathParams(payload, r, route.Path)
		for key, value := range route.StaticPayload {
			if _, exists := payload[key]; exists {
				continue
			}
			payload[key] = value
		}
	}
	if route.IncludeRawBody && len(rawBody) > 0 && payloadEncoding != "protobuf" {
		payload["_raw_body"] = string(rawBody)
	}
	if len(route.IncludeHeaders) > 0 && payloadEncoding != "protobuf" {
		headers := map[string]any{}
		for _, name := range route.IncludeHeaders {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			headers[strings.ToLower(name)] = strings.TrimSpace(r.Header.Get(name))
		}
		payload["_request_headers"] = headers
	}

	req := DispatchRequest{
		EventType:          strings.TrimSpace(route.EventType),
		Payload:            payload,
		PayloadBytes:       rawBody,
		PayloadEncoding:    payloadEncoding,
		ResponseEncoding:   responseEncoding,
		Metadata:           metadataFromHeaders(r),
		CorrelationID:      strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		SchemaVersion:      "1.0",
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		RequiredCapability: strings.TrimSpace(route.RequiredCapability),
		RequiredPermission: strings.TrimSpace(route.Permission),
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return req, nil
}

func appendPathParams(payload map[string]any, r *http.Request, pathPattern string) {
	if r == nil || payload == nil || strings.TrimSpace(pathPattern) == "" {
		return
	}
	matches := pathParamPattern.FindAllStringSubmatch(pathPattern, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		if _, exists := payload[name]; exists {
			continue
		}
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			payload[name] = value
		}
	}
}

func payloadFromRequest(r *http.Request) (map[string]any, []byte, string, string, error) {
	if r == nil {
		return map[string]any{}, nil, "json", "json", nil
	}
	responseEncoding := responseEncodingFromRequest(r)
	if r.Method == http.MethodGet || r.Method == http.MethodDelete {
		out := map[string]any{}
		for key, values := range r.URL.Query() {
			if len(values) == 0 {
				continue
			}
			out[key] = strings.TrimSpace(values[0])
		}
		return out, nil, "json", responseEncoding, nil
	}

	if r.Body == nil {
		return map[string]any{}, nil, "json", responseEncoding, nil
	}
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, "", "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	if len(bytes.TrimSpace(rawBody)) == 0 {
		return map[string]any{}, rawBody, requestEncodingFromRequest(r), responseEncoding, nil
	}
	if requestEncodingFromRequest(r) == "protobuf" {
		return nil, rawBody, "protobuf", responseEncoding, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, rawBody, "json", responseEncoding, nil
		}
		return nil, rawBody, "", "", err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, rawBody, "json", responseEncoding, nil
}

func metadataFromHeaders(r *http.Request) map[string]any {
	metadata := map[string]any{}
	if r == nil {
		return metadata
	}
	if idempotencyKey := strings.TrimSpace(r.Header.Get("X-Idempotency-Key")); idempotencyKey != "" {
		metadata["idempotency_key"] = idempotencyKey
	}
	if traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID")); traceID != "" {
		metadata["trace_id"] = traceID
	}
	if spanID := strings.TrimSpace(r.Header.Get("X-Span-ID")); spanID != "" {
		metadata["span_id"] = spanID
	}
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-ID")); requestID != "" {
		metadata["request_id"] = requestID
	}
	return metadata
}

func requestEncodingFromRequest(r *http.Request) string {
	if r == nil {
		return "json"
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.Contains(contentType, "application/x-protobuf") || strings.Contains(contentType, "application/protobuf") {
		return "protobuf"
	}
	return "json"
}

func responseEncodingFromRequest(r *http.Request) string {
	if r == nil {
		return "json"
	}
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(accept, "application/x-protobuf") || strings.Contains(accept, "application/protobuf") {
		return "protobuf"
	}
	return "json"
}
