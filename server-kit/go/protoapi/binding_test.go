package protoapi

import (
	"context"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"testing"
)

func protoObject(t *testing.T, values map[string]any) extension.Object {
	t.Helper()
	value, err := extension.FromJSON(values)
	if err != nil {
		t.Fatalf("extension.FromJSON() error = %v", err)
	}
	object, ok := value.ObjectValue()
	if !ok {
		t.Fatalf("value is not object: %T", value)
	}
	return object
}

func TestBindingDecodeRequestObjectMergesEnvelopeMetadata(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	request, err := binding.DecodeRequestObject(protoObject(t, map[string]any{
		"workspace_id": "wrk_123",
		"content_type": "image/jpeg",
		"size":         float64(1024),
		"hash":         "sha256:abc",
		"metadata": map[string]any{
			"locale": "en-US",
		},
	}), protoObject(t, map[string]any{
		"correlation_id": "corr_123",
		"request_id":     "req_123",
		"global_context": map[string]any{
			"source": "api",
		},
	}))
	if err != nil {
		t.Fatalf("DecodeRequestObject failed: %v", err)
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

func TestBindingDecodeRequestObjectFiltersMetadataForTargetMessage(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	request, err := binding.DecodeRequestObject(protoObject(t, map[string]any{
		"workspace_id": "wrk_123",
		"content_type": "image/jpeg",
	}), protoObject(t, map[string]any{
		"correlation_id":      "corr_123",
		"policy_snapshot_ref": "policy_ignored",
		"global_context": map[string]any{
			"source":       "api",
			"jurisdiction": "ignored",
		},
	}))
	if err != nil {
		t.Fatalf("DecodeRequestObject failed: %v", err)
	}
	typed := request.(*testprotos.TestRequest)
	if typed.Metadata.GetCorrelationId() != "corr_123" {
		t.Fatalf("correlation_id = %q", typed.Metadata.GetCorrelationId())
	}
	if typed.Metadata.GetGlobalContext().GetSource() != "api" {
		t.Fatalf("global_context.source = %q", typed.Metadata.GetGlobalContext().GetSource())
	}
}

func TestMessageToObjectUsesProtoFieldNames(t *testing.T) {
	payload, err := MessageToObject(&testprotos.TestResponse{
		ResourceId: "asset_123",
		Status:     "complete",
	})
	if err != nil {
		t.Fatalf("MessageToObject failed: %v", err)
	}
	if got, ok := payload.GetString("resource_id"); !ok || got != "asset_123" {
		t.Fatalf("resource_id = %q ok=%v", got, ok)
	}
	if got, ok := payload.GetString("status"); !ok || got != "complete" {
		t.Fatalf("status = %q ok=%v", got, ok)
	}
}

func TestResponseFromObject(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.Metadata{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	msg, err := binding.ResponseFromObject(protoObject(t, map[string]any{
		"resource_id": "asset_123",
		"status":      "complete",
	}))
	if err != nil {
		t.Fatalf("ResponseFromObject failed: %v", err)
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
	if got, err := valid.EncodeResponseObject(nil); err != nil || len(got) != 0 {
		t.Fatalf("EncodeResponseObject(nil) = %+v err=%v", got, err)
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
	msg, err := DecodeObjectByEncoding(binding, PayloadEncodingProtobuf, nil, requestBytes, protoObject(t, map[string]any{
		"request_id": "req_bytes",
	}))
	if err != nil {
		t.Fatalf("DecodeObjectByEncoding failed: %v", err)
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
	msg, err := binding.DecodeRequestBytesIntoObject(target, first, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true})
	if err != nil {
		t.Fatalf("DecodeRequestBytesIntoObject() first error = %v", err)
	}
	if msg != target || target.GetMetadata().GetCorrelationId() != "corr_1" || target.GetHash() != "sha256:one" {
		t.Fatalf("first decode mismatch: msg=%p target=%+v", msg, target)
	}
	msg, err = binding.DecodeRequestBytesIntoObject(target, second, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true})
	if err != nil {
		t.Fatalf("DecodeRequestBytesIntoObject() second error = %v", err)
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
	if _, err := binding.DecodeRequestBytesIntoObject(target, withHash, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
		t.Fatalf("DecodeRequestBytesIntoObject() withHash error = %v", err)
	}
	if _, err := binding.DecodeRequestBytesIntoObject(target, withoutHash, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
		t.Fatalf("DecodeRequestBytesIntoObject() withoutHash merge error = %v", err)
	}
	if target.GetWorkspaceId() != "wrk_new" || target.GetHash() != "sha256:old" {
		t.Fatalf("merge reuse caveat mismatch: %+v", target)
	}

	if _, err := binding.DecodeRequestBytesIntoObject(target, withoutHash, nil, DecodeRequestBytesIntoOptions{}); err != nil {
		t.Fatalf("DecodeRequestBytesIntoObject() reset lane error = %v", err)
	}
	if target.GetWorkspaceId() != "wrk_new" || target.GetHash() != "" {
		t.Fatalf("reset lane should clear absent hash: %+v", target)
	}
	if _, err := binding.DecodeRequestBytesIntoObject(nil, withoutHash, nil, DecodeRequestBytesIntoOptions{}); err == nil {
		t.Fatalf("expected nil target error")
	}
}

func TestDecodeByEncodingJSONUnsupportedAndInvalidBytes(t *testing.T) {
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	msg, err := DecodeObjectByEncoding(binding, "", protoObject(t, map[string]any{"workspace_id": "wrk_json"}), nil, nil)
	if err != nil {
		t.Fatalf("JSON DecodeObjectByEncoding() error = %v", err)
	}
	if msg.(*testprotos.TestRequest).WorkspaceId != "wrk_json" {
		t.Fatalf("JSON request mismatch: %+v", msg)
	}
	if _, err := DecodeObjectByEncoding(binding, PayloadEncodingProtobuf, nil, []byte("bad"), nil); err == nil {
		t.Fatalf("expected invalid protobuf bytes error")
	}
	if _, err := DecodeObjectByEncoding(binding, "xml", nil, nil, nil); err == nil {
		t.Fatalf("expected unsupported encoding error")
	}
	if _, err := binding.ResponseFromObject(extension.Object{"resource_id": extension.Int(10)}); err == nil {
		t.Fatalf("expected response decode type error")
	}
}

func TestReflectionBindingMetadataDetectionAndUnknownFields(t *testing.T) {
	if !hasMetadataField(&testprotos.TestRequest{}) {
		t.Fatalf("TestRequest should have metadata field")
	}
	if hasMetadataField(&testprotos.TestResponse{}) || hasMetadataField(nil) {
		t.Fatalf("metadata field detection mismatch")
	}
	binding := Binding{
		Request:  factory(func() anyProto { return &testprotos.TestRequest{} }).toFactory(),
		Response: factory(func() anyProto { return &testprotos.TestResponse{} }).toFactory(),
	}
	if _, err := binding.DecodeRequestObject(extension.Object{"unknown": extension.String("bad")}, nil); err == nil {
		t.Fatalf("expected unknown payload field error")
	}
	request, err := binding.DecodeRequestObject(nil, extension.Object{
		"policy_snapshot_ref": extension.String("ignored"),
		"global_context": extension.ObjectValue(extension.Object{
			"source":       extension.String("web"),
			"jurisdiction": extension.String("ignored"),
		}),
	})
	if err != nil {
		t.Fatalf("metadata overlay error = %v", err)
	}
	typed := request.(*testprotos.TestRequest)
	if typed.Metadata.GetGlobalContext().GetSource() != "web" {
		t.Fatalf("metadata overlay did not preserve known nested field: %+v", typed.Metadata)
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
	_ = (&testprotos.GlobalContext{}).ProtoReflect().Descriptor()

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
	_ = (&testprotos.Metadata{}).ProtoReflect().Descriptor()

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
	_ = (&testprotos.TestRequest{}).ProtoReflect().Descriptor()

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
	_ = (&testprotos.TestResponse{}).ProtoReflect().Descriptor()
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
		if _, err := binding.DecodeRequestBytesIntoObject(target, payload, nil, DecodeRequestBytesIntoOptions{CompleteMessage: true}); err != nil {
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
func TestSetFieldValueNullClearsAndRejectsRepeatedAndNonObject(t *testing.T) {
	md := allScalarsDescriptor(t)
	dm := dynamicpb.NewMessage(md)

	if err := objectIntoMessage(dm, extension.Object{"s": extension.String("set")}, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := objectIntoMessage(dm, extension.Object{"s": extension.Null()}, true); err != nil {
		t.Fatalf("null clear: %v", err)
	}
	if v, _ := messageToObject(dm)["s"].StringValue(); v != "" {
		t.Fatalf("field not cleared: %q", v)
	}

	if err := objectIntoMessage(dm, extension.Object{"tags": extension.String("x")}, true); err == nil {
		t.Fatal("repeated field should be rejected")
	}
}

func TestSetFieldValueRejectsNonObjectForMessageField(t *testing.T) {
	b := newTestBinding()

	if _, err := b.DecodeRequestObject(protoObject(t, map[string]any{"metadata": "not-an-object"}), nil); err == nil {
		t.Fatal("message-kind field given a string should error")
	}
}

func TestDecodeRequestObjectRejectsUnknownField(t *testing.T) {
	b := newTestBinding()
	if _, err := b.DecodeRequestObject(protoObject(t, map[string]any{"nope_field": "x"}), nil); err == nil {
		t.Fatal("unknown field should be rejected under strict decode")
	}
}

func TestProtoValueToExtensionHandlesUnknownEnumNumber(t *testing.T) {
	md := allScalarsDescriptor(t)
	dm := dynamicpb.NewMessage(md)
	colorField := md.Fields().ByName("color")

	dm.Set(colorField, protoreflect.ValueOfEnum(99))
	out := messageToObject(dm)
	if out["color"].Kind() != extension.KindInt {
		t.Fatalf("unknown enum number kind = %d want KindInt", out["color"].Kind())
	}
	if v, _ := out["color"].IntValue(); v != 99 {
		t.Fatalf("unknown enum number = %d want 99", v)
	}
}

func TestMessageToObjectNilAndNestedMessageValidity(t *testing.T) {
	if obj, err := MessageToObject(nil); err != nil || len(obj) != 0 {
		t.Fatalf("MessageToObject(nil) = (%v,%v)", obj, err)
	}

	populated := &testprotos.TestRequest{Metadata: &testprotos.Metadata{CorrelationId: "c1"}}
	obj, err := MessageToObject(populated)
	if err != nil {
		t.Fatalf("MessageToObject error: %v", err)
	}
	meta, ok := obj.GetObject("metadata")
	if !ok {
		t.Fatal("nested metadata not an object")
	}
	if cid, _ := meta.GetString("correlation_id"); cid != "c1" {
		t.Fatalf("correlation_id = %q", cid)
	}

	empty := &testprotos.TestRequest{}
	obj2, _ := MessageToObject(empty)
	if obj2["metadata"].Kind() != extension.KindNull {
		t.Fatalf("unset metadata kind = %d want KindNull", obj2["metadata"].Kind())
	}
}

func TestMetadataMergeGuardsMissingFieldAndEmptyMetadata(t *testing.T) {

	resp := (&testprotos.TestResponse{}).ProtoReflect()
	if metadataFieldDescriptorForMessage(resp) != nil {
		t.Fatal("TestResponse should have no metadata field descriptor")
	}
	if err := mergeMetadataIntoMessage(resp, extension.Object{"x": extension.String("y")}); err != nil {
		t.Fatalf("merge into metadata-less message should no-op, got %v", err)
	}

	req := (&testprotos.TestRequest{}).ProtoReflect()
	if err := mergeMetadataIntoMessage(req, nil); err != nil {
		t.Fatalf("empty metadata merge should no-op, got %v", err)
	}
}

func TestDecodeRequestBytesIntoObjectRequiresTarget(t *testing.T) {
	b := newTestBinding()
	if _, err := b.DecodeRequestBytesIntoObject(nil, []byte{}, nil, DecodeRequestBytesIntoOptions{}); err == nil {
		t.Fatal("nil target should be rejected")
	}
}
