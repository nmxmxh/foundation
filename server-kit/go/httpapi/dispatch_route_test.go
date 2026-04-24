package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
