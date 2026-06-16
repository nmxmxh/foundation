package protoapi

// Coverage for the proto<->extension scalar conversion surface and the
// higher-level Binding methods. The generated test protos only exercise
// string/int64/message fields, so the scalar matrix (bool, all int/uint widths,
// fixed/sint/sfixed, float/double, bytes, enum) is driven through a synthetic
// dynamicpb message built at test time — no codegen needed. Oracles assert the
// round-tripped Kind and value (TE-03); cases span each proto scalar kind plus
// the boundary and wrong-type rejections (TE-04).

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
)

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
