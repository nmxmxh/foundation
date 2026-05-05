package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func TestBuildDispatchRequestDefaultsProtobufResponseForProtobufRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/media/assets", nil)
	request.Header.Set("Content-Type", "application/x-protobuf")

	dispatch, err := BuildDispatchRequest(request, registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/media/assets",
		EventType: "media:process_asset:v1:requested",
	})
	if err != nil {
		t.Fatalf("BuildDispatchRequest() error = %v", err)
	}
	if dispatch.PayloadEncoding != "protobuf" {
		t.Fatalf("PayloadEncoding = %q", dispatch.PayloadEncoding)
	}
	if dispatch.ResponseEncoding != "protobuf" {
		t.Fatalf("ResponseEncoding = %q", dispatch.ResponseEncoding)
	}
}

func TestBuildDispatchRequestUsesOneCorrelationID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/media/assets/asset_1", nil)
	request.Header.Set("X-Request-ID", "req_123")

	dispatch, err := BuildDispatchRequest(request, registry.HTTPRoute{
		Method:    http.MethodGet,
		Path:      "/v1/media/assets/{id}",
		EventType: "media:get_asset:v1:requested",
	})
	if err != nil {
		t.Fatalf("BuildDispatchRequest() error = %v", err)
	}
	if dispatch.CorrelationID != "req_123" {
		t.Fatalf("CorrelationID = %q, want req_123", dispatch.CorrelationID)
	}
	md := metadata.FromMap(dispatch.Metadata)
	if md.CorrelationID != dispatch.CorrelationID {
		t.Fatalf("metadata.correlation_id = %q, want %q", md.CorrelationID, dispatch.CorrelationID)
	}
	if md.RequestID != "req_123" {
		t.Fatalf("metadata.request_id = %q, want req_123", md.RequestID)
	}
}

func TestBuildDispatchRequestGeneratesOneCorrelationID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/media/assets", nil)

	dispatch, err := BuildDispatchRequest(request, registry.HTTPRoute{
		Method:    http.MethodGet,
		Path:      "/v1/media/assets",
		EventType: "media:list_assets:v1:requested",
	})
	if err != nil {
		t.Fatalf("BuildDispatchRequest() error = %v", err)
	}
	if dispatch.CorrelationID == "" {
		t.Fatal("expected generated correlation id")
	}
	md := metadata.FromMap(dispatch.Metadata)
	if md.CorrelationID != dispatch.CorrelationID {
		t.Fatalf("metadata.correlation_id = %q, want %q", md.CorrelationID, dispatch.CorrelationID)
	}
}
