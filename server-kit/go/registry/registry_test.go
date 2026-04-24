package registry

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
)

func TestDispatchBytesKeepsTypedPayloadBinary(t *testing.T) {
	registry := New(nil, nil, nil)
	binding := protoapi.Binding{
		Request: func() proto.Message {
			return &testprotos.TestRequest{}
		},
		Response: func() proto.Message {
			return &testprotos.TestResponse{}
		},
	}
	err := registry.RegisterTypedWithOptions(
		"media:process_asset:v1:requested",
		binding,
		func(_ context.Context, request proto.Message) (proto.Message, error) {
			typed := request.(*testprotos.TestRequest)
			if typed.WorkspaceId != "wrk_123" {
				t.Fatalf("workspace_id = %q", typed.WorkspaceId)
			}
			if typed.Metadata.GetCorrelationId() != "corr_123" {
				t.Fatalf("metadata.correlation_id = %q", typed.Metadata.GetCorrelationId())
			}
			return &testprotos.TestResponse{ResourceId: "asset_123", Status: "complete"}, nil
		},
		bootstrap.ConcurrencyOptions{MaxConcurrent: 1},
	)
	if err != nil {
		t.Fatalf("RegisterTypedWithOptions() error = %v", err)
	}

	payload, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_123"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	responseBytes, ok, err := registry.DispatchBytes(context.Background(), "media:process_asset:v1:requested", payload, map[string]any{
		"correlation_id": "corr_123",
	})
	if err != nil {
		t.Fatalf("DispatchBytes() error = %v", err)
	}
	if !ok {
		t.Fatalf("expected handler to be found")
	}
	var response testprotos.TestResponse
	if err := proto.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("response Unmarshal() error = %v", err)
	}
	if response.ResourceId != "asset_123" || response.Status != "complete" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestTypedDispatchDefaultsResponseToRequestEncoding(t *testing.T) {
	registry := New(nil, nil, nil)
	binding := protoapi.Binding{
		Request: func() proto.Message {
			return &testprotos.TestRequest{}
		},
		Response: func() proto.Message {
			return &testprotos.TestResponse{}
		},
	}
	err := registry.RegisterTypedWithOptions(
		"media:process_asset:v1:requested",
		binding,
		func(context.Context, proto.Message) (proto.Message, error) {
			return &testprotos.TestResponse{ResourceId: "asset_123", Status: "complete"}, nil
		},
		bootstrap.ConcurrencyOptions{MaxConcurrent: 1},
	)
	if err != nil {
		t.Fatalf("RegisterTypedWithOptions() error = %v", err)
	}
	payload, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_123"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	result, ok, err := registry.DispatchInput(context.Background(), "media:process_asset:v1:requested", DispatchInput{
		PayloadBytes:    payload,
		PayloadEncoding: protoapi.PayloadEncodingProtobuf,
		Metadata:        map[string]any{"correlation_id": "corr_123"},
	})
	if err != nil {
		t.Fatalf("DispatchInput() error = %v", err)
	}
	if !ok {
		t.Fatalf("expected handler to be found")
	}
	if result.PayloadEncoding != protoapi.PayloadEncodingProtobuf || len(result.PayloadBytes) == 0 {
		t.Fatalf("expected protobuf response by default, got encoding=%q bytes=%d", result.PayloadEncoding, len(result.PayloadBytes))
	}
	if len(result.Payload) != 0 {
		t.Fatalf("protobuf response should not materialize map payload: %+v", result.Payload)
	}
}
