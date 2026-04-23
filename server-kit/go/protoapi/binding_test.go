package protoapi

import (
	"context"
	"testing"

	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestBindingDecodeRequestMapMergesEnvelopeMetadata(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	request, err := binding.DecodeRequestMap(map[string]any{
		"workspace_id": "wrk_123",
		"content_type": "image/jpeg",
		"size":         1024,
		"hash":         "sha256:abc",
		"metadata": map[string]any{
			"locale": "en-US",
		},
	}, map[string]any{
		"correlation_id": "corr_123",
		"request_id":     "req_123",
		"global_context": map[string]any{
			"source": "api",
		},
	})
	if err != nil {
		t.Fatalf("DecodeRequestMap failed: %v", err)
	}
	typed, ok := request.(*testprotos.TestRequest)
	if !ok {
		t.Fatalf("request type = %T", request)
	}
	if typed.Metadata == nil {
		t.Fatalf("metadata is nil")
	}
	if typed.Metadata.CorrelationId != "corr_123" {
		t.Fatalf("correlation_id = %q", typed.Metadata.CorrelationId)
	}
	if typed.Metadata.RequestId != "req_123" {
		t.Fatalf("request_id = %q", typed.Metadata.RequestId)
	}
	if typed.Metadata.Locale != "en-US" {
		t.Fatalf("locale = %q", typed.Metadata.Locale)
	}
	if typed.Metadata.GlobalContext == nil || typed.Metadata.GlobalContext.Source != "api" {
		t.Fatalf("global_context.source = %v", typed.Metadata.GlobalContext)
	}
}

func TestMessageToMapUsesProtoFieldNames(t *testing.T) {
	payload, err := MessageToMap(&testprotos.TestResponse{
		ResourceId: "asset_123",
		Status:     "complete",
	})
	if err != nil {
		t.Fatalf("MessageToMap failed: %v", err)
	}
	if payload["resource_id"] != "asset_123" {
		t.Fatalf("resource_id = %#v", payload["resource_id"])
	}
	if payload["status"] != "complete" {
		t.Fatalf("status = %#v", payload["status"])
	}
}

func TestResponseFromMap(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.Metadata{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	msg, err := binding.ResponseFromMap(map[string]any{
		"resource_id": "asset_123",
		"status":      "complete",
	})
	if err != nil {
		t.Fatalf("ResponseFromMap failed: %v", err)
	}
	response := msg.(*testprotos.TestResponse)
	if response.ResourceId != "asset_123" || response.Status != "complete" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestDecodeByEncoding(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	requestBytes, err := proto.Marshal(&testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			CorrelationId: "corr_bytes",
		},
		WorkspaceId: "wrk_123",
		ContentType: "image/jpeg",
		Size:        2048,
		Hash:        "sha256:def",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	msg, err := DecodeByEncoding(binding, PayloadEncodingProtobuf, nil, requestBytes, map[string]any{
		"request_id": "req_bytes",
	})
	if err != nil {
		t.Fatalf("DecodeByEncoding failed: %v", err)
	}
	typed := msg.(*testprotos.TestRequest)
	if typed.Metadata.GetCorrelationId() != "corr_bytes" || typed.Metadata.GetRequestId() != "req_bytes" {
		t.Fatalf("unexpected metadata: %+v", typed.Metadata)
	}
}

type anyProto interface {
	ProtoReflect() protoreflect.Message
}

type factory func() anyProto

func (f factory) toFactory() MessageFactory {
	return func() proto.Message {
		return f().(proto.Message)
	}
}

func TestTypedHandlerFuncTypeCompiles(_ *testing.T) {
	var _ TypedHandlerFunc = func(_ context.Context, _ proto.Message) (proto.Message, error) {
		return &testprotos.Metadata{}, nil
	}
}
