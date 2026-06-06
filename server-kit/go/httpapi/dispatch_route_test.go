package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func TestNewEventRouteHandlerValidationAndExecution(t *testing.T) {
	route := registry.HTTPRoute{Method: http.MethodPost, EventType: "orders:create:v1:requested"}
	rec := httptest.NewRecorder()
	NewEventRouteHandler(route, nil)(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("nil executor status = %d", rec.Code)
	}

	called := false
	handler := NewEventRouteHandler(route, func(w http.ResponseWriter, _ *http.Request, req DispatchRequest) {
		called = true
		name, _ := req.Payload.GetString("name")
		if req.EventType != "orders:create:v1:requested" || name != "Ada" {
			t.Fatalf("dispatch request = %+v", req)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewBufferString(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewBufferString(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusAccepted || !called {
		t.Fatalf("handler status=%d called=%v", rec.Code, called)
	}
}

func TestBuildDispatchRequestPayloadVariants(t *testing.T) {
	route := registry.HTTPRoute{
		Method:         http.MethodGet,
		Path:           "/v1/orders/{order_id}",
		EventType:      "orders:get:v1:requested",
		StaticPayload:  extension.Object{"source": extension.String("route"), "order_id": extension.String("static")},
		IncludeHeaders: []string{"X-Trace-ID", " "},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/orders/ord_1?workspace_id=wrk_1", nil)
	req.SetPathValue("order_id", "ord_1")
	req.Header.Set("X-Trace-ID", "trace_1")
	out, err := BuildDispatchRequest(req, route)
	if err != nil {
		t.Fatalf("BuildDispatchRequest() error = %v", err)
	}
	orderID, _ := out.Payload.GetString("order_id")
	source, _ := out.Payload.GetString("source")
	workspaceID, _ := out.Payload.GetString("workspace_id")
	if orderID != "ord_1" || source != "route" || workspaceID != "wrk_1" {
		t.Fatalf("payload = %+v", out.Payload)
	}
	headersValue, _ := out.Payload["_request_headers"].ObjectValue()
	traceID, _ := headersValue.GetString("x-trace-id")
	if traceID != "trace_1" {
		t.Fatalf("headers payload = %+v", out.Payload["_request_headers"])
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/orders?role=user&role=admin", nil)
	if _, err := BuildDispatchRequest(req, route); err == nil {
		t.Fatalf("expected duplicate query rejection")
	}

	body := []byte{0x01, 0x02}
	req = httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	out, err = BuildDispatchRequest(req, registry.HTTPRoute{Method: http.MethodPost, EventType: "orders:create:v1:requested"})
	if err != nil {
		t.Fatalf("protobuf BuildDispatchRequest() error = %v", err)
	}
	if out.PayloadEncoding != "protobuf" || !bytes.Equal(out.PayloadBytes, body) || out.ResponseEncoding != "protobuf" {
		t.Fatalf("protobuf request = %+v bytes=%v", out, out.PayloadBytes)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader([]byte(`{"id":"ord_1"}`)))
	req.Header.Set("Content-Type", "application/json")
	out, err = BuildDispatchRequest(req, registry.HTTPRoute{Method: http.MethodPost, EventType: "orders:create:v1:requested"})
	if err != nil {
		t.Fatalf("json BuildDispatchRequest() error = %v", err)
	}
	if len(out.PayloadBytes) != 0 {
		t.Fatalf("json request retained raw payload bytes without IncludeRawBody: %d", len(out.PayloadBytes))
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader([]byte(`{"id":"ord_1"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/protobuf")
	out, err = BuildDispatchRequest(req, registry.HTTPRoute{
		Method:         http.MethodPost,
		EventType:      "orders:create:v1:requested",
		IncludeRawBody: true,
	})
	if err != nil {
		t.Fatalf("json BuildDispatchRequest() error = %v", err)
	}
	rawBody, _ := out.Payload.GetString("_raw_body")
	if out.ResponseEncoding != "protobuf" || rawBody == "" || len(out.PayloadBytes) == 0 {
		t.Fatalf("json request = %+v", out)
	}
	metadataJSON, err := json.Marshal(out.Metadata)
	if err != nil {
		t.Fatalf("metadata should be JSON-safe: %v", err)
	}
	if len(metadataJSON) == 0 {
		t.Fatal("metadata JSON should not be empty")
	}
}

func BenchmarkPayloadFromRequestJSONBody(b *testing.B) {
	body := []byte(`{"include_permissions":true,"view":"full"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/users/user-123/profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		payload, raw, requestEncoding, responseEncoding, err := payloadFromRequest(req)
		if err != nil {
			b.Fatal(err)
		}
		if requestEncoding != "json" || responseEncoding != "json" || len(raw) == 0 {
			b.Fatalf("encoding/raw mismatch: %s %s %d", requestEncoding, responseEncoding, len(raw))
		}
		if view, _ := payload.GetString("view"); view != "full" {
			b.Fatalf("payload view = %q", view)
		}
	}
}

func BenchmarkBuildDispatchRequestJSONBody(b *testing.B) {
	route := registry.HTTPRoute{
		Method:         http.MethodPost,
		Path:           "/v1/users/{id}/profile",
		EventType:      "user.profile.read",
		StaticPayload:  extension.Object{"source": extension.String("api")},
		IncludeHeaders: []string{"X-Request-ID", "X-Correlation-ID"},
	}
	body := []byte(`{"include_permissions":true,"view":"full"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/users/user-123/profile", bytes.NewReader(body))
		req.SetPathValue("id", "user-123")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "req-123")
		req.Header.Set("X-Correlation-ID", "corr-123")
		if _, err := BuildDispatchRequest(req, route); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlannedDispatchRequestJSONBody(b *testing.B) {
	route := registry.HTTPRoute{
		Method:         http.MethodPost,
		Path:           "/v1/users/{id}/profile",
		EventType:      "user.profile.read",
		StaticPayload:  extension.Object{"source": extension.String("api")},
		IncludeHeaders: []string{"X-Request-ID", "X-Correlation-ID"},
	}
	plan := CompileDispatchRoute(route)
	body := []byte(`{"include_permissions":true,"view":"full"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/users/user-123/profile", bytes.NewReader(body))
		req.SetPathValue("id", "user-123")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "req-123")
		req.Header.Set("X-Correlation-ID", "corr-123")
		if _, err := plan.Build(req); err != nil {
			b.Fatal(err)
		}
	}
}
