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

func TestBindingValidationAndNilResponses(t *testing.T) {
	valid := Binding{
		Request:  factory(func() anyProto { return &testprotos.Metadata{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if _, err := (Binding{}).NewRequest(); err == nil {
		t.Fatalf("expected missing request factory error")
	}
	if _, err := (Binding{}).NewResponse(); err == nil {
		t.Fatalf("expected missing response factory error")
	}
	if err := (Binding{Request: func() proto.Message { return nil }, Response: valid.Response}).Validate(); err == nil {
		t.Fatalf("expected nil request validation error")
	}
	if err := (Binding{Request: valid.Request, Response: func() proto.Message { return nil }}).Validate(); err == nil {
		t.Fatalf("expected nil response validation error")
	}
	if err := (Binding{Request: valid.Request, Response: valid.Response, ProtobufDecodeReuse: "bad"}).Validate(); err == nil {
		t.Fatalf("expected unsupported protobuf decode reuse mode error")
	}
	if err := (Binding{Request: valid.Request}).Validate(); err == nil {
		t.Fatalf("expected missing response factory error")
	}
	if _, err := (Binding{Request: func() proto.Message { return nil }}).NewRequest(); err == nil {
		t.Fatalf("expected nil request factory result error")
	}
	if _, err := (Binding{Response: func() proto.Message { return nil }}).NewResponse(); err == nil {
		t.Fatalf("expected nil response factory result error")
	}
	if got, err := valid.EncodeResponseMap(nil); err != nil || len(got) != 0 {
		t.Fatalf("EncodeResponseMap(nil) = %+v err=%v", got, err)
	}
	if got, err := valid.EncodeResponseBytes(nil); err != nil || len(got) != 0 {
		t.Fatalf("EncodeResponseBytes(nil) = %+v err=%v", got, err)
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

func TestDecodeRequestBytesIntoCompleteReuseOptIn(t *testing.T) {
	binding := Binding{
		Request:             factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response:            factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
		ProtobufDecodeReuse: ProtobufDecodeReuseCompleteMessages,
	}
	first, err := proto.Marshal(&testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			CorrelationId: "corr_1",
			RequestId:     "req_1",
			Locale:        "en",
			GlobalContext: &testprotos.GlobalContext{Source: "api", DeviceId: "device_1"},
		},
		WorkspaceId: "wrk_1",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:one",
	})
	if err != nil {
		t.Fatalf("Marshal() first error = %v", err)
	}
	second, err := proto.Marshal(&testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			CorrelationId: "corr_2",
			RequestId:     "req_2",
			Locale:        "fr",
			GlobalContext: &testprotos.GlobalContext{Source: "worker", DeviceId: "device_2"},
		},
		WorkspaceId: "wrk_2",
		ContentType: "application/json",
		Size:        32,
		Hash:        "sha256:two",
	})
	if err != nil {
		t.Fatalf("Marshal() second error = %v", err)
	}

	target := &testprotos.TestRequest{Metadata: &testprotos.Metadata{GlobalContext: &testprotos.GlobalContext{}}}
	msg, err := binding.DecodeRequestBytesInto(target, first, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true})
	if err != nil {
		t.Fatalf("DecodeRequestBytesInto() first error = %v", err)
	}
	if msg != target || target.GetMetadata().GetCorrelationId() != "corr_1" || target.GetHash() != "sha256:one" {
		t.Fatalf("first decode mismatch: msg=%p target=%+v", msg, target)
	}
	msg, err = binding.DecodeRequestBytesInto(target, second, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true})
	if err != nil {
		t.Fatalf("DecodeRequestBytesInto() second error = %v", err)
	}
	if msg != target {
		t.Fatalf("expected caller-owned target to be reused")
	}
	if target.GetMetadata().GetCorrelationId() != "corr_2" ||
		target.GetMetadata().GetGlobalContext().GetDeviceId() != "device_2" ||
		target.GetWorkspaceId() != "wrk_2" ||
		target.GetContentType() != "application/json" ||
		target.GetSize() != 32 ||
		target.GetHash() != "sha256:two" {
		t.Fatalf("second decode mismatch: %+v", target)
	}
}

func TestDecodeRequestBytesIntoAbsentFieldCaveatAndResetLane(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	withHash, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_old",
		Hash:        "sha256:old",
	})
	if err != nil {
		t.Fatalf("Marshal() withHash error = %v", err)
	}
	withoutHash, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_new",
	})
	if err != nil {
		t.Fatalf("Marshal() withoutHash error = %v", err)
	}

	target := &testprotos.TestRequest{}
	if _, err := binding.DecodeRequestBytesInto(target, withHash, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
		t.Fatalf("DecodeRequestBytesInto() withHash error = %v", err)
	}
	if _, err := binding.DecodeRequestBytesInto(target, withoutHash, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
		t.Fatalf("DecodeRequestBytesInto() withoutHash merge error = %v", err)
	}
	if target.GetWorkspaceId() != "wrk_new" || target.GetHash() != "sha256:old" {
		t.Fatalf("merge reuse caveat mismatch: %+v", target)
	}

	if _, err := binding.DecodeRequestBytesInto(target, withoutHash, nil, DecodeRequestBytesIntoOptions{}); err != nil {
		t.Fatalf("DecodeRequestBytesInto() reset lane error = %v", err)
	}
	if target.GetWorkspaceId() != "wrk_new" || target.GetHash() != "" {
		t.Fatalf("reset lane should clear absent hash: %+v", target)
	}
	if _, err := binding.DecodeRequestBytesInto(nil, withoutHash, nil, DecodeRequestBytesIntoOptions{}); err == nil {
		t.Fatalf("expected nil target error")
	}
}

func TestDecodeByEncodingJSONUnsupportedAndInvalidBytes(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	msg, err := DecodeByEncoding(binding, "", map[string]any{"workspace_id": "wrk_json"}, nil, nil)
	if err != nil {
		t.Fatalf("JSON DecodeByEncoding() error = %v", err)
	}
	if msg.(*testprotos.TestRequest).WorkspaceId != "wrk_json" {
		t.Fatalf("JSON request mismatch: %+v", msg)
	}
	if _, err := DecodeByEncoding(binding, PayloadEncodingProtobuf, nil, []byte("bad"), nil); err == nil {
		t.Fatalf("expected invalid protobuf bytes error")
	}
	if _, err := DecodeByEncoding(binding, "xml", nil, nil, nil); err == nil {
		t.Fatalf("expected unsupported encoding error")
	}
	if _, err := binding.DecodeRequestMap(map[string]any{"metadata": func() {}}, nil); err == nil {
		t.Fatalf("expected map decode marshal error")
	}
	if _, err := binding.ResponseFromMap(map[string]any{"resource_id": 10}); err == nil {
		t.Fatalf("expected response decode type error")
	}
}

func TestMapCloneHelpersAndMetadataDetection(t *testing.T) {
	original := map[string]any{
		"nested": map[string]any{"k": "v"},
		"items":  []any{map[string]any{"x": "y"}},
	}
	cloned := cloneMap(original)
	cloned["nested"].(map[string]any)["k"] = "changed"
	if original["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("cloneMap did not deep clone nested maps")
	}
	if len(asMap(nil)) != 0 || len(asMap("bad")) != 0 {
		t.Fatalf("asMap should return empty map for non-maps")
	}
	if !hasMetadataField(&testprotos.TestRequest{}) {
		t.Fatalf("TestRequest should have metadata field")
	}
	if hasMetadataField(&testprotos.TestResponse{}) || hasMetadataField(nil) {
		t.Fatalf("metadata field detection mismatch")
	}
	merged := mergeMetadataMap(map[string]any{
		"global_context": map[string]any{"source": "web", "device_id": "old"},
	}, map[string]any{
		"global_context": map[string]any{"device_id": "new"},
		"request_id":     "req",
	})
	if merged["request_id"] != "req" || merged["global_context"].(map[string]any)["source"] != "web" || merged["global_context"].(map[string]any)["device_id"] != "new" {
		t.Fatalf("metadata merge mismatch: %+v", merged)
	}
}

func TestGeneratedTestProtoAccessors(t *testing.T) {
	gc := &testprotos.GlobalContext{UserId: "user", Source: "api", DeviceId: "device"}
	if gc.String() == "" || gc.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("global context reflection failed")
	}
	if gc.GetUserId() != "user" || gc.GetSource() != "api" || gc.GetDeviceId() != "device" {
		t.Fatalf("global context getters failed")
	}
	gc.Reset()
	if gc.GetUserId() != "" {
		t.Fatalf("reset global context should clear fields")
	}
	if (*testprotos.GlobalContext)(nil).GetUserId() != "" || (*testprotos.GlobalContext)(nil).GetSource() != "" || (*testprotos.GlobalContext)(nil).GetDeviceId() != "" {
		t.Fatalf("nil global context getters failed")
	}
	_, _ = (&testprotos.GlobalContext{}).Descriptor()

	md := &testprotos.Metadata{CorrelationId: "corr", RequestId: "req", Locale: "en", GlobalContext: &testprotos.GlobalContext{Source: "api"}}
	if md.String() == "" || md.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("metadata reflection failed")
	}
	if md.GetCorrelationId() != "corr" || md.GetRequestId() != "req" || md.GetLocale() != "en" || md.GetGlobalContext().GetSource() != "api" {
		t.Fatalf("metadata getters failed")
	}
	md.Reset()
	if md.GetCorrelationId() != "" {
		t.Fatalf("reset metadata should clear fields")
	}
	if (*testprotos.Metadata)(nil).GetCorrelationId() != "" || (*testprotos.Metadata)(nil).GetRequestId() != "" || (*testprotos.Metadata)(nil).GetLocale() != "" || (*testprotos.Metadata)(nil).GetGlobalContext() != nil {
		t.Fatalf("nil metadata getters failed")
	}
	_, _ = (&testprotos.Metadata{}).Descriptor()

	req := &testprotos.TestRequest{Metadata: &testprotos.Metadata{CorrelationId: "corr"}, WorkspaceId: "wrk", ContentType: "image/png", Size: 7, Hash: "sha"}
	if req.String() == "" || req.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("request reflection failed")
	}
	if req.GetMetadata().GetCorrelationId() != "corr" || req.GetWorkspaceId() != "wrk" || req.GetContentType() != "image/png" || req.GetSize() != 7 || req.GetHash() != "sha" {
		t.Fatalf("request getters failed")
	}
	req.Reset()
	if req.GetWorkspaceId() != "" {
		t.Fatalf("reset request should clear fields")
	}
	if (*testprotos.TestRequest)(nil).GetMetadata() != nil || (*testprotos.TestRequest)(nil).GetWorkspaceId() != "" || (*testprotos.TestRequest)(nil).GetContentType() != "" || (*testprotos.TestRequest)(nil).GetSize() != 0 || (*testprotos.TestRequest)(nil).GetHash() != "" {
		t.Fatalf("nil request getters failed")
	}
	_, _ = (&testprotos.TestRequest{}).Descriptor()

	resp := &testprotos.TestResponse{ResourceId: "res", Status: "ok"}
	if resp.String() == "" || resp.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("response reflection failed")
	}
	if resp.GetResourceId() != "res" || resp.GetStatus() != "ok" {
		t.Fatalf("response getters failed")
	}
	resp.Reset()
	if resp.GetResourceId() != "" {
		t.Fatalf("reset response should clear fields")
	}
	if (*testprotos.TestResponse)(nil).GetResourceId() != "" || (*testprotos.TestResponse)(nil).GetStatus() != "" {
		t.Fatalf("nil response getters failed")
	}
	_, _ = (&testprotos.TestResponse{}).Descriptor()
}

func BenchmarkDecodeRequestBytesIntoCompleteReuse(b *testing.B) {
	binding := Binding{
		Request:             factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response:            factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
		ProtobufDecodeReuse: ProtobufDecodeReuseCompleteMessages,
	}
	payload, err := proto.Marshal(&testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			CorrelationId: "corr_1",
			RequestId:     "req_1",
			Locale:        "en",
			GlobalContext: &testprotos.GlobalContext{Source: "api", DeviceId: "device_1"},
		},
		WorkspaceId: "wrk_1",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:abc",
	})
	if err != nil {
		b.Fatal(err)
	}
	target := &testprotos.TestRequest{Metadata: &testprotos.Metadata{GlobalContext: &testprotos.GlobalContext{}}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := binding.DecodeRequestBytesInto(target, payload, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
			b.Fatal(err)
		}
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
