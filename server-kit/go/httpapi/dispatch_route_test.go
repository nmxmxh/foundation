package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		if req.EventType != "orders:create:v1:requested" || req.Payload["name"] != "Ada" {
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
		StaticPayload:  map[string]any{"source": "route", "order_id": "static"},
		IncludeHeaders: []string{"X-Trace-ID", " "},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/orders/ord_1?workspace_id=wrk_1", nil)
	req.SetPathValue("order_id", "ord_1")
	req.Header.Set("X-Trace-ID", "trace_1")
	out, err := BuildDispatchRequest(req, route)
	if err != nil {
		t.Fatalf("BuildDispatchRequest() error = %v", err)
	}
	if out.Payload["order_id"] != "ord_1" || out.Payload["source"] != "route" || out.Payload["workspace_id"] != "wrk_1" {
		t.Fatalf("payload = %+v", out.Payload)
	}
	if out.Payload["_request_headers"].(map[string]any)["x-trace-id"] != "trace_1" {
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
	req.Header.Set("Accept", "application/protobuf")
	out, err = BuildDispatchRequest(req, registry.HTTPRoute{
		Method:         http.MethodPost,
		EventType:      "orders:create:v1:requested",
		IncludeRawBody: true,
	})
	if err != nil {
		t.Fatalf("json BuildDispatchRequest() error = %v", err)
	}
	if out.ResponseEncoding != "protobuf" || out.Payload["_raw_body"] == "" {
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
