package protoapi

import (
	"context"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"math"
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
	
	for b.Loop() {
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

// Coverage for the proto<->extension scalar conversion surface and the
// higher-level Binding methods. The generated test protos only exercise
// string/int64/message fields, so the scalar matrix (bool, all int/uint widths,
// fixed/sint/sfixed, float/double, bytes, enum) is driven through a synthetic
// dynamicpb message built at test time — no codegen needed. Oracles assert the
// round-tripped Kind and value (TE-03); cases span each proto scalar kind plus
// the boundary and wrong-type rejections (TE-04).

func scalarField(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   new(name),
		Number: new(num),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   typ.Enum(),
	}
}

// allScalarsDescriptor builds a proto3 message with one field of every scalar
// kind plus an enum, used to exercise the conversion helpers exhaustively.
func allScalarsDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	enumField := scalarField("color", 16, descriptorpb.FieldDescriptorProto_TYPE_ENUM)
	enumField.TypeName = new(".protoapi.dyn.v1.AllScalars.Color")
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("dyn.proto"),
		Syntax:  new("proto3"),
		Package: new("protoapi.dyn.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("AllScalars"),
			Field: []*descriptorpb.FieldDescriptorProto{
				scalarField("b", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				scalarField("s", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				scalarField("by", 3, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
				scalarField("i32", 4, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				scalarField("i64", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				scalarField("u32", 6, descriptorpb.FieldDescriptorProto_TYPE_UINT32),
				scalarField("u64", 7, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				scalarField("f32", 8, descriptorpb.FieldDescriptorProto_TYPE_FLOAT),
				scalarField("f64", 9, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
				scalarField("fx32", 10, descriptorpb.FieldDescriptorProto_TYPE_FIXED32),
				scalarField("fx64", 11, descriptorpb.FieldDescriptorProto_TYPE_FIXED64),
				scalarField("s32", 12, descriptorpb.FieldDescriptorProto_TYPE_SINT32),
				scalarField("s64", 13, descriptorpb.FieldDescriptorProto_TYPE_SINT64),
				scalarField("sf32", 14, descriptorpb.FieldDescriptorProto_TYPE_SFIXED32),
				scalarField("sf64", 15, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64),
				enumField,
				func() *descriptorpb.FieldDescriptorProto {
					f := scalarField("tags", 17, descriptorpb.FieldDescriptorProto_TYPE_STRING)
					f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
					return f
				}(),
			},
			EnumType: []*descriptorpb.EnumDescriptorProto{{
				Name: new("Color"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: new("UNKNOWN"), Number: proto.Int32(0)},
					{Name: new("RED"), Number: proto.Int32(1)},
				},
			}},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return fd.Messages().Get(0)
}

func TestScalarConversionRoundTripsEveryProtoKind(t *testing.T) {
	md := allScalarsDescriptor(t)
	dm := dynamicpb.NewMessage(md)
	in := extension.Object{
		"b":     extension.Bool(true),
		"s":     extension.String("hi"),
		"by":    extension.Bytes([]byte{1, 2, 3}),
		"i32":   extension.Int(-5),
		"i64":   extension.Int(-9),
		"u32":   extension.Uint(7),
		"u64":   extension.Uint(8),
		"f32":   extension.Float(1.5),
		"f64":   extension.Float(2.5),
		"fx32":  extension.Uint(10),
		"fx64":  extension.Uint(11),
		"s32":   extension.Int(-12),
		"s64":   extension.Int(-13),
		"sf32":  extension.Int(-14),
		"sf64":  extension.Int(-15),
		"color": extension.String("RED"),
	}
	if err := objectIntoMessage(dm, in, true); err != nil {
		t.Fatalf("objectIntoMessage: %v", err)
	}
	out := messageToObject(dm)

	if v, ok := out["b"].BoolValue(); !ok || !v {
		t.Fatalf("b = (%v,%v)", v, ok)
	}
	if v, ok := out["s"].StringValue(); !ok || v != "hi" {
		t.Fatalf("s = (%q,%v)", v, ok)
	}
	if v, ok := out["by"].BytesValue(); !ok || string(v) != "\x01\x02\x03" {
		t.Fatalf("by = (%v,%v)", v, ok)
	}
	for _, name := range []string{"i32", "i64", "s32", "s64", "sf32", "sf64"} {
		if out[name].Kind() != extension.KindInt {
			t.Fatalf("%s kind = %d want KindInt", name, out[name].Kind())
		}
	}
	if v, _ := out["i32"].IntValue(); v != -5 {
		t.Fatalf("i32 = %d want -5", v)
	}
	for _, name := range []string{"u32", "u64", "fx32", "fx64"} {
		if out[name].Kind() != extension.KindUint {
			t.Fatalf("%s kind = %d want KindUint", name, out[name].Kind())
		}
	}
	if v, _ := out["u32"].UintValue(); v != 7 {
		t.Fatalf("u32 = %d want 7", v)
	}
	if v, ok := out["f32"].FloatValue(); !ok || v != 1.5 {
		t.Fatalf("f32 = (%v,%v)", v, ok)
	}
	if v, ok := out["f64"].FloatValue(); !ok || v != 2.5 {
		t.Fatalf("f64 = (%v,%v)", v, ok)
	}
	if v, ok := out["color"].StringValue(); !ok || v != "RED" {
		t.Fatalf("color = (%q,%v)", v, ok)
	}
}

func TestScalarConversionRejectsBoundaryAndWrongTypes(t *testing.T) {
	md := allScalarsDescriptor(t)
	fields := md.Fields()
	fld := func(name string) protoreflect.FieldDescriptor {
		f := fields.ByName(protoreflect.Name(name))
		if f == nil {
			t.Fatalf("field %q not found", name)
		}
		return f
	}

	rejections := []struct {
		name  string
		field string
		value extension.Value
	}{
		{"int32 overflow", "i32", extension.Int(math.MaxInt32 + 1)},
		{"uint32 overflow", "u32", extension.Uint(math.MaxUint32 + 1)},
		{"uint64 from negative", "u64", extension.Int(-1)},
		{"int64 from non-whole float", "i64", extension.Float(3.5)},
		{"bool wrong type", "b", extension.String("x")},
		{"string wrong type", "s", extension.Int(1)},
		{"bytes wrong type", "by", extension.Int(1)},
		{"double wrong type", "f64", extension.String("x")},
		{"int64 wrong type", "i64", extension.String("x")},
		{"enum unknown value", "color", extension.String("PURPLE")},
		{"enum wrong type", "color", extension.Int(1)},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scalarToProtoValue(fld(tc.field), tc.value); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}

	// Numeric coercions that must succeed exercise the cross-kind branches of
	// int64FromValue / uint64FromValue / float64FromValue.
	accepts := []struct {
		name  string
		field string
		value extension.Value
	}{
		{"int64 from uint", "i64", extension.Uint(5)},
		{"int64 from whole float", "i64", extension.Float(3)},
		{"uint64 from whole float", "u64", extension.Float(4)},
		{"double from int", "f64", extension.Int(3)},
		{"float from uint", "f32", extension.Uint(2)},
	}
	for _, tc := range accepts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scalarToProtoValue(fld(tc.field), tc.value); err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.name, err)
			}
		})
	}
}

func newTestBinding() Binding {
	return Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
}

func TestBindingValidateAndReuseMode(t *testing.T) {
	if err := newTestBinding().Validate(); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	resp := func() proto.Message { return &testprotos.TestResponse{} }
	req := func() proto.Message { return &testprotos.TestRequest{} }
	nilMsg := func() proto.Message { return nil }
	bad := []Binding{
		{Response: resp},                  // nil request factory
		{Request: req},                    // nil response factory
		{Request: nilMsg, Response: resp}, // request factory returns nil
		{Request: req, Response: nilMsg},  // response factory returns nil
		{Request: req, Response: resp, ProtobufDecodeReuse: "bogus"}, // invalid reuse mode
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Fatalf("bad binding %d passed validation", i)
		}
	}
	if newTestBinding().AllowsProtobufDecodeReuse() {
		t.Fatal("default binding should not allow decode reuse")
	}
	reuse := Binding{Request: req, Response: resp, ProtobufDecodeReuse: ProtobufDecodeReuseCompleteMessages}
	if !reuse.AllowsProtobufDecodeReuse() {
		t.Fatal("complete-messages binding should allow decode reuse")
	}
}

func TestBindingEncodeResponseHandlesNilAndPopulated(t *testing.T) {
	b := newTestBinding()
	if obj, err := b.EncodeResponseObject(nil); err != nil || len(obj) != 0 {
		t.Fatalf("EncodeResponseObject(nil) = (%v,%v)", obj, err)
	}
	if raw, err := b.EncodeResponseBytes(nil); err != nil || len(raw) != 0 {
		t.Fatalf("EncodeResponseBytes(nil) = (%v,%v)", raw, err)
	}
	respMsg := &testprotos.TestResponse{ResourceId: "r1", Status: "ok"}
	obj, err := b.EncodeResponseObject(respMsg)
	if err != nil {
		t.Fatalf("EncodeResponseObject error: %v", err)
	}
	if s, ok := obj.GetString("resource_id"); !ok || s != "r1" {
		t.Fatalf("resource_id = (%q,%v)", s, ok)
	}
	raw, err := b.EncodeResponseBytes(respMsg)
	if err != nil || len(raw) == 0 {
		t.Fatalf("EncodeResponseBytes = (%v,%v)", raw, err)
	}
	back, err := b.ResponseFromObject(obj)
	if err != nil {
		t.Fatalf("ResponseFromObject error: %v", err)
	}
	typed, ok := back.(*testprotos.TestResponse)
	if !ok || typed.ResourceId != "r1" {
		t.Fatalf("round-tripped response = %#v", back)
	}
}

func TestBindingDecodeRequestBytesObjectMergesMetadataAndRejectsGarbage(t *testing.T) {
	b := newTestBinding()
	src := &testprotos.TestRequest{WorkspaceId: "w1", Size: 5}
	raw, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg, err := b.DecodeRequestBytesObject(raw, protoObject(t, map[string]any{"correlation_id": "c1"}))
	if err != nil {
		t.Fatalf("DecodeRequestBytesObject: %v", err)
	}
	typed := msg.(*testprotos.TestRequest)
	if typed.WorkspaceId != "w1" {
		t.Fatalf("workspace = %q", typed.WorkspaceId)
	}
	if typed.Metadata == nil || typed.Metadata.CorrelationId != "c1" {
		t.Fatalf("metadata not merged: %#v", typed.Metadata)
	}
	if _, err := b.DecodeRequestBytesObject([]byte{0xff, 0xff, 0xff}, nil); err == nil {
		t.Fatal("garbage protobuf should fail to decode")
	}
}

func TestBindingDecodeAndResponseErrorOnMissingFactory(t *testing.T) {
	if _, err := (Binding{Response: func() proto.Message { return &testprotos.TestResponse{} }}).DecodeRequestObject(nil, nil); err == nil {
		t.Fatal("DecodeRequestObject with nil request factory should error")
	}
	if _, err := (Binding{Request: func() proto.Message { return &testprotos.TestRequest{} }}).ResponseFromObject(nil); err == nil {
		t.Fatal("ResponseFromObject with nil response factory should error")
	}
}
