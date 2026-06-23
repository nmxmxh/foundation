package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"google.golang.org/protobuf/proto"
)

// TestDispatchProtobufResponseLane covers the protobuf request/response lane end to
// end (TE-11 binary parity): a typed handler registered with a proto binding,
// driven over an HTTP route with application/x-protobuf content negotiation, must
// decode the protobuf request, run, and serialize a protobuf response — not fall
// back to JSON. This exercises performDispatch's protobuf-request path and
// executeDispatch's protobuf-response write.
func TestDispatchProtobufResponseLane(t *testing.T) {
	const eventType = "media:process_asset:v1:requested"

	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(graceful.WithLogger(log), graceful.WithService("proto-test"))
	reg := registry.New(nil, gh, log)

	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	ran := false
	if err := reg.RegisterTypedWithOptions(eventType, binding,
		func(_ context.Context, request proto.Message) (proto.Message, error) {
			ran = true
			req := request.(*testprotos.TestRequest)
			return &testprotos.TestResponse{ResourceId: req.GetWorkspaceId() + ":processed"}, nil
		}, bootstrap.ConcurrencyOptions{}); err != nil {
		t.Fatalf("RegisterTypedWithOptions() err=%v", err)
	}

	s := New(&Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
	s.SetHTTPRoutes([]registry.HTTPRoute{{
		Method: http.MethodPost, Path: "/v1/asset", EventType: eventType,
	}})

	body, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_123"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/asset", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Accept", "application/x-protobuf")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !ran {
		t.Fatal("typed handler did not run")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Fatalf("response content-type = %q, want application/x-protobuf", ct)
	}
	var resp testprotos.TestResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a protobuf TestResponse: %v", err)
	}
	if resp.GetResourceId() != "wrk_123:processed" {
		t.Fatalf("resource_id = %q, want wrk_123:processed", resp.GetResourceId())
	}
}
