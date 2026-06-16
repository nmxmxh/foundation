package protoapi

// Branch coverage for the remaining setFieldValue, messageToObject, and
// metadata-merge paths: null-clear, unsupported repeated/map fields, a
// message-kind field given a non-object, the enum-unknown-number and
// valid/invalid nested-message branches of protoValueToExtension, and the
// nil/empty guards on the higher-level decode helpers.

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
)

func TestSetFieldValueNullClearsAndRejectsRepeatedAndNonObject(t *testing.T) {
	md := allScalarsDescriptor(t)
	dm := dynamicpb.NewMessage(md)
	// Populate then clear via a null value.
	if err := objectIntoMessage(dm, extension.Object{"s": extension.String("set")}, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := objectIntoMessage(dm, extension.Object{"s": extension.Null()}, true); err != nil {
		t.Fatalf("null clear: %v", err)
	}
	if v, _ := messageToObject(dm)["s"].StringValue(); v != "" {
		t.Fatalf("field not cleared: %q", v)
	}
	// Repeated fields are unsupported by typed object binding.
	if err := objectIntoMessage(dm, extension.Object{"tags": extension.String("x")}, true); err == nil {
		t.Fatal("repeated field should be rejected")
	}
}

func TestSetFieldValueRejectsNonObjectForMessageField(t *testing.T) {
	b := newTestBinding()
	// metadata is a message-kind field; a scalar value must be rejected.
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
	// A number with no matching enum value falls back to an integer.
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
	// Valid nested message materialises as an object.
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
	// Unset nested message materialises as null.
	empty := &testprotos.TestRequest{}
	obj2, _ := MessageToObject(empty)
	if obj2["metadata"].Kind() != extension.KindNull {
		t.Fatalf("unset metadata kind = %d want KindNull", obj2["metadata"].Kind())
	}
}

func TestMetadataMergeGuardsMissingFieldAndEmptyMetadata(t *testing.T) {
	// A message without a metadata field has no descriptor and no-ops on merge.
	resp := (&testprotos.TestResponse{}).ProtoReflect()
	if metadataFieldDescriptorForMessage(resp) != nil {
		t.Fatal("TestResponse should have no metadata field descriptor")
	}
	if err := mergeMetadataIntoMessage(resp, extension.Object{"x": extension.String("y")}); err != nil {
		t.Fatalf("merge into metadata-less message should no-op, got %v", err)
	}
	// Empty metadata is a no-op even when the field exists.
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
