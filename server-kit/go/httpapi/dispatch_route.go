package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// DispatchRequest is the canonical request envelope used by HTTP ingress.
type DispatchRequest struct {
	EventType          string                    `json:"event_type"`
	Payload            extension.Object          `json:"payload"`
	PayloadBytes       []byte                    `json:"-"`
	PayloadEncoding    string                    `json:"payload_encoding,omitempty"`
	ResponseEncoding   string                    `json:"-"`
	Metadata           extension.Object          `json:"metadata"`
	MetadataEnvelope   metadata.EnvelopeMetadata `json:"-"`
	CorrelationID      string                    `json:"correlation_id"`
	SchemaVersion      string                    `json:"schema_version"`
	Timestamp          string                    `json:"timestamp"`
	RequiredCapability string                    `json:"-"`
	RequiredPermission string                    `json:"-"`
}

// EnsureMetadata lazily materializes the extension.Object representation of metadata if nil.
func (d *DispatchRequest) EnsureMetadata() extension.Object {
	if d.Metadata == nil && d.MetadataEnvelope.CorrelationID != "" {
		d.Metadata = d.MetadataEnvelope.ToObject()
	}
	return d.Metadata
}

// DispatchExecutor executes one validated envelope request.
type DispatchExecutor func(http.ResponseWriter, *http.Request, DispatchRequest)

type DispatchRoutePlan struct {
	route              registry.HTTPRoute
	pathParams         []string
	includeHeaders     []headerPlan
	payloadExtraFields int
	eventType          string
	requiredCapability string
	requiredPermission string
}

type headerPlan struct {
	name string
	key  string
}

var (
	lastTimestampSec atomic.Int64
	lastTimestampStr atomic.Pointer[string]
)

func currentRFC3339() string {
	now := time.Now().UTC()
	sec := now.Unix()
	if lastTimestampSec.Load() == sec {
		if ptr := lastTimestampStr.Load(); ptr != nil {
			return *ptr
		}
	}
	formatted := now.Format(time.RFC3339)
	lastTimestampSec.Store(sec)
	lastTimestampStr.Store(&formatted)
	return formatted
}

// NewEventRouteHandler builds a route-level handler that maps HTTP requests to dispatch requests.
func NewEventRouteHandler(route registry.HTTPRoute, execute DispatchExecutor) http.HandlerFunc {
	plan := CompileDispatchRoute(route)
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

		req, err := plan.Build(r)
		if err != nil {
			domainerr.WriteHTTP(w, domainerr.Validation("invalid_json", "invalid json"), domainerr.ResponseOptions{})
			return
		}
		execute(w, r, req)
	}
}

// BuildDispatchRequest converts an HTTP request into a dispatch envelope.
func BuildDispatchRequest(r *http.Request, route registry.HTTPRoute) (DispatchRequest, error) {
	plan := CompileDispatchRoute(route)
	return plan.Build(r)
}

func CompileDispatchRoute(route registry.HTTPRoute) DispatchRoutePlan {
	plan := DispatchRoutePlan{
		route:              route,
		eventType:          strings.TrimSpace(route.EventType),
		requiredCapability: strings.TrimSpace(route.RequiredCapability),
		requiredPermission: strings.TrimSpace(route.Permission),
	}
	plan.pathParams = pathParamsFromPattern(route.Path)
	for _, name := range route.IncludeHeaders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		plan.includeHeaders = append(plan.includeHeaders, headerPlan{name: name, key: strings.ToLower(name)})
	}
	plan.payloadExtraFields = len(route.StaticPayload) + len(plan.pathParams)
	if route.IncludeRawBody {
		plan.payloadExtraFields++
	}
	if len(plan.includeHeaders) > 0 {
		plan.payloadExtraFields++
	}
	return plan
}

func (plan DispatchRoutePlan) Build(r *http.Request) (DispatchRequest, error) {
	return buildDispatchRequest(r, plan, true)
}

func buildDispatchRequest(r *http.Request, plan DispatchRoutePlan, planned bool) (DispatchRequest, error) {
	route := plan.route
	payload, rawBody, payloadEncoding, responseEncoding, err := payloadFromRequest(r)
	if err != nil {
		return DispatchRequest{}, err
	}
	if payload == nil && payloadEncoding != "protobuf" {
		payload = extension.Object{}
	}
	if payloadEncoding != "protobuf" {
		if planned {
			appendPlannedPathParams(payload, r, plan.pathParams)
		} else {
			appendPathParams(payload, r, route.Path)
		}
		for key, value := range route.StaticPayload {
			if _, exists := payload[key]; exists {
				continue
			}
			payload[key] = value.Clone()
		}
	}
	if route.IncludeRawBody && len(rawBody) > 0 && payloadEncoding != "protobuf" {
		payload["_raw_body"] = extension.String(string(rawBody))
	}
	if (len(plan.includeHeaders) > 0 || len(route.IncludeHeaders) > 0) && payloadEncoding != "protobuf" {
		headers := includedHeadersObject(r, route.IncludeHeaders, plan.includeHeaders)
		payload["_request_headers"] = extension.ObjectValueOwned(headers)
	}

	requestMetadata := MetadataFromRequest(r)
	payloadBytes := rawBody
	if payloadEncoding != "protobuf" && !route.IncludeRawBody {
		payloadBytes = nil
	}

	arena := scratch.FromContext(r.Context())
	var metadataObj extension.Object
	if arena == nil {
		metadataObj = requestMetadata.ToObject()
	}

	req := DispatchRequest{
		EventType:          plan.eventType,
		Payload:            payload,
		PayloadBytes:       payloadBytes,
		PayloadEncoding:    payloadEncoding,
		ResponseEncoding:   responseEncoding,
		Metadata:           metadataObj,
		MetadataEnvelope:   requestMetadata,
		CorrelationID:      requestMetadata.CorrelationID,
		SchemaVersion:      "1.0",
		Timestamp:          currentRFC3339(),
		RequiredCapability: plan.requiredCapability,
		RequiredPermission: plan.requiredPermission,
	}
	return req, nil
}

func includedHeadersObject(r *http.Request, routeHeaders []string, planned []headerPlan) extension.Object {
	if len(planned) > 0 {
		headers := make(extension.Object, len(planned))
		for _, header := range planned {
			headers[header.key] = extension.String(strings.TrimSpace(r.Header.Get(header.name)))
		}
		return headers
	}
	headers := make(extension.Object, len(routeHeaders))
	for _, name := range routeHeaders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		headers[strings.ToLower(name)] = extension.String(strings.TrimSpace(r.Header.Get(name)))
	}
	return headers
}

func appendPathParams(payload extension.Object, r *http.Request, pathPattern string) {
	if r == nil || payload == nil || strings.TrimSpace(pathPattern) == "" {
		return
	}
	for offset := 0; offset < len(pathPattern); {
		open := strings.IndexByte(pathPattern[offset:], '{')
		if open < 0 {
			return
		}
		open += offset
		close := strings.IndexByte(pathPattern[open+1:], '}')
		if close < 0 {
			return
		}
		close += open + 1
		name := strings.TrimSpace(pathPattern[open+1 : close])
		offset = close + 1
		if name == "" {
			continue
		}
		if _, exists := payload[name]; exists {
			continue
		}
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			payload[name] = extension.String(value)
		}
	}
}

func appendPlannedPathParams(payload extension.Object, r *http.Request, names []string) {
	if r == nil || payload == nil || len(names) == 0 {
		return
	}
	for _, name := range names {
		if _, exists := payload[name]; exists {
			continue
		}
		if value := strings.TrimSpace(r.PathValue(name)); value != "" {
			payload[name] = extension.String(value)
		}
	}
}

func pathParamsFromPattern(pathPattern string) []string {
	if strings.TrimSpace(pathPattern) == "" {
		return nil
	}
	out := []string{}
	for offset := 0; offset < len(pathPattern); {
		open := strings.IndexByte(pathPattern[offset:], '{')
		if open < 0 {
			return out
		}
		open += offset
		close := strings.IndexByte(pathPattern[open+1:], '}')
		if close < 0 {
			return out
		}
		close += open + 1
		name := strings.TrimSpace(pathPattern[open+1 : close])
		offset = close + 1
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func payloadFromRequest(r *http.Request) (extension.Object, []byte, string, string, error) {
	if r == nil {
		return extension.Object{}, nil, "json", "json", nil
	}
	requestEncoding := requestEncodingFromRequest(r)
	responseEncoding := responseEncodingFromRequest(r, requestEncoding)
	if r.Method == http.MethodGet || r.Method == http.MethodDelete {
		if r.URL.RawQuery == "" {
			return extension.Object{}, nil, requestEncoding, responseEncoding, nil
		}
		query := r.URL.Query()
		if err := security.RejectDuplicateQueryParams(query); err != nil {
			return nil, nil, "", "", domainerr.Validation("duplicate_query_parameter", "duplicate query parameter")
		}
		out := extension.Object{}
		for key, values := range query {
			if len(values) == 0 {
				continue
			}
			out[key] = extension.String(strings.TrimSpace(values[0]))
		}
		return out, nil, requestEncoding, responseEncoding, nil
	}

	if r.Body == nil {
		return extension.Object{}, nil, requestEncoding, responseEncoding, nil
	}
	rawBody, err := readRequestBody(r)
	if err != nil {
		return nil, nil, "", "", err
	}
	if !hasNonSpace(rawBody) {
		return extension.Object{}, rawBody, requestEncoding, responseEncoding, nil
	}
	if requestEncoding == "protobuf" {
		return nil, rawBody, "protobuf", responseEncoding, nil
	}

	payload, err := extension.ObjectFromJSON(rawBody)
	if err != nil {
		return nil, rawBody, "", "", err
	}
	return payload, rawBody, "json", responseEncoding, nil
}

// maxPresizedBody caps the maximum trusted Content-Length for upfront buffer sizing.
const maxPresizedBody = 1 << 20

// defaultInitialBodyCap sizes unknown-length bodies to prevent buffer growth churn.
const defaultInitialBodyCap = 4096

// readRequestBody reads the request body using Content-Length sizing hints.
// It avoids initial allocation churn by pre-sizing the buffer.
func readRequestBody(r *http.Request) ([]byte, error) {
	size := r.ContentLength
	if size == 0 {
		return nil, nil
	}
	if size > maxPresizedBody {
		return io.ReadAll(r.Body)
	}
	initCap := size + 1
	if size < 0 {
		initCap = defaultInitialBodyCap
	}
	var buf []byte
	if arena := scratch.FromContext(r.Context()); arena != nil && int(initCap) <= arena.Available() {
		var err error
		buf, err = arena.AllocBytes(0, int(initCap))
		if err != nil {
			buf = make([]byte, 0, initCap)
		}
	} else {
		buf = make([]byte, 0, initCap)
	}
	// One byte of headroom: a body longer than its declared Content-Length is
	// read in full and rejected downstream rather than silently truncated here.
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Body.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return buf, err
		}
	}
}

func requestEncodingFromRequest(r *http.Request) string {
	if r == nil {
		return "json"
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if hasSubstrFold(contentType, "application/x-protobuf") || hasSubstrFold(contentType, "application/protobuf") {
		return "protobuf"
	}
	return "json"
}

func hasNonSpace(data []byte) bool {
	for _, ch := range data {
		switch ch {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return true
		}
	}
	return false
}

func responseEncodingFromRequest(r *http.Request, requestEncoding string) string {
	if r == nil {
		return "json"
	}
	accept := strings.TrimSpace(r.Header.Get("Accept"))
	if hasSubstrFold(accept, "application/x-protobuf") || hasSubstrFold(accept, "application/protobuf") {
		return "protobuf"
	}
	if accept == "" && requestEncoding == "protobuf" {
		return "protobuf"
	}
	return "json"
}

func hasSubstrFold(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if strings.EqualFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}
