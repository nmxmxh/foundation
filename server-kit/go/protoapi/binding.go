package protoapi

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

const (
	PayloadEncodingJSON     = "json"
	PayloadEncodingProtobuf = "protobuf"
)

type MessageFactory func() proto.Message

type ProtobufDecodeReuseMode string

const (
	ProtobufDecodeReuseDisabled         ProtobufDecodeReuseMode = ""
	ProtobufDecodeReuseCompleteMessages ProtobufDecodeReuseMode = "complete_messages"
)

type Binding struct {
	Request             MessageFactory
	Response            MessageFactory
	ProtobufDecodeReuse ProtobufDecodeReuseMode
}

type TypedHandlerFunc func(context.Context, proto.Message) (proto.Message, error)

type DecodeRequestBytesIntoOptions struct {
	// CompleteMessage enables protobuf Merge reuse. Only use it when the payload
	// contract does not rely on absent fields clearing prior state, or after the
	// caller has explicitly cleared every field that may be absent.
	CompleteMessage bool
}

func (b Binding) Validate() error {
	if b.Request == nil {
		return errors.New("request message factory is required")
	}
	if b.Response == nil {
		return errors.New("response message factory is required")
	}
	if b.Request() == nil {
		return errors.New("request message factory returned nil")
	}
	if b.Response() == nil {
		return errors.New("response message factory returned nil")
	}
	switch b.ProtobufDecodeReuse {
	case ProtobufDecodeReuseDisabled, ProtobufDecodeReuseCompleteMessages:
	default:
		return fmt.Errorf("unsupported protobuf decode reuse mode %q", b.ProtobufDecodeReuse)
	}
	return nil
}

func (b Binding) AllowsProtobufDecodeReuse() bool {
	return b.ProtobufDecodeReuse == ProtobufDecodeReuseCompleteMessages
}

func (b Binding) NewRequest() (proto.Message, error) {
	if b.Request == nil {
		return nil, errors.New("request message factory is required")
	}
	msg := b.Request()
	if msg == nil {
		return nil, errors.New("request message factory returned nil")
	}
	return msg, nil
}

func (b Binding) NewResponse() (proto.Message, error) {
	if b.Response == nil {
		return nil, errors.New("response message factory is required")
	}
	msg := b.Response()
	if msg == nil {
		return nil, errors.New("response message factory returned nil")
	}
	return msg, nil
}

func (b Binding) DecodeRequestObject(payload extension.Object, metadata extension.Object) (proto.Message, error) {
	msg, err := b.NewRequest()
	if err != nil {
		return nil, err
	}
	if err := decodeObjectIntoMessage(msg, payload, metadata); err != nil {
		return nil, err
	}
	return msg, nil
}

func (b Binding) DecodeRequestBytesObject(payload []byte, metadata extension.Object) (proto.Message, error) {
	msg, err := b.NewRequest()
	if err != nil {
		return nil, err
	}
	return b.DecodeRequestBytesIntoObject(msg, payload, metadata, DecodeRequestBytesIntoOptions{})
}

func (b Binding) DecodeRequestBytesIntoObject(target proto.Message, payload []byte, metadata extension.Object, opts DecodeRequestBytesIntoOptions) (proto.Message, error) {
	if target == nil {
		return nil, errors.New("target request message is required")
	}
	if !opts.CompleteMessage || len(payload) == 0 {
		proto.Reset(target)
	}
	if len(payload) > 0 {
		if opts.CompleteMessage {
			if err := (proto.UnmarshalOptions{Merge: true}).Unmarshal(payload, target); err != nil {
				return nil, err
			}
		} else if err := proto.Unmarshal(payload, target); err != nil {
			return nil, err
		}
	}
	if len(metadata) == 0 || !hasMetadataField(target) {
		return target, nil
	}
	if err := mergeMetadataIntoMessage(target.ProtoReflect(), metadata); err != nil {
		return nil, err
	}
	return target, nil
}

func (b Binding) EncodeResponseObject(msg proto.Message) (extension.Object, error) {
	if msg == nil {
		return extension.Object{}, nil
	}
	return MessageToObject(msg)
}

func (b Binding) EncodeResponseBytes(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return []byte{}, nil
	}
	return proto.Marshal(msg)
}

func (b Binding) ResponseFromObject(payload extension.Object) (proto.Message, error) {
	msg, err := b.NewResponse()
	if err != nil {
		return nil, err
	}
	if err := decodeObjectIntoMessage(msg, payload, nil); err != nil {
		return nil, err
	}
	return msg, nil
}

func MessageToObject(msg proto.Message) (extension.Object, error) {
	if msg == nil {
		return extension.Object{}, nil
	}
	return messageToObject(msg.ProtoReflect()), nil
}

func decodeObjectIntoMessage(msg proto.Message, payload extension.Object, metadata extension.Object) error {
	if msg == nil {
		return errors.New("message is required")
	}
	reflected := msg.ProtoReflect()
	if err := objectIntoMessage(reflected, payload, true); err != nil {
		return err
	}
	return mergeMetadataIntoMessage(reflected, metadata)
}

func hasMetadataField(msg proto.Message) bool {
	return metadataFieldDescriptor(msg) != nil
}

func metadataFieldDescriptor(msg proto.Message) protoreflect.FieldDescriptor {
	if msg == nil {
		return nil
	}
	field := msg.ProtoReflect().Descriptor().Fields().ByName("metadata")
	if field == nil || field.Kind() != protoreflect.MessageKind {
		return nil
	}
	return field
}

func DecodeObjectByEncoding(binding Binding, encoding string, payload extension.Object, payloadBytes []byte, metadata extension.Object) (proto.Message, error) {
	switch normalizeEncoding(encoding) {
	case PayloadEncodingProtobuf:
		return binding.DecodeRequestBytesObject(payloadBytes, metadata)
	case PayloadEncodingJSON:
		return binding.DecodeRequestObject(payload, metadata)
	default:
		return nil, fmt.Errorf("unsupported payload encoding %q", encoding)
	}
}

func objectIntoMessage(msg protoreflect.Message, payload extension.Object, strictUnknown bool) error {
	if len(payload) == 0 {
		return nil
	}
	fields := msg.Descriptor().Fields()
	for key, value := range payload {
		field := fields.ByJSONName(key)
		if field == nil {
			field = fields.ByName(protoreflect.Name(key))
		}
		if field == nil {
			if strictUnknown {
				return fmt.Errorf("unknown protobuf field %q", key)
			}
			continue
		}
		if err := setFieldValue(msg, field, value, strictUnknown); err != nil {
			return fmt.Errorf("field %s: %w", field.FullName(), err)
		}
	}
	return nil
}

func mergeMetadataIntoMessage(msg protoreflect.Message, metadata extension.Object) error {
	if len(metadata) == 0 {
		return nil
	}
	field := metadataFieldDescriptorForMessage(msg)
	if field == nil {
		return nil
	}
	return objectIntoMessage(msg.Mutable(field).Message(), metadata, false)
}

func metadataFieldDescriptorForMessage(msg protoreflect.Message) protoreflect.FieldDescriptor {
	if !msg.IsValid() {
		return nil
	}
	field := msg.Descriptor().Fields().ByName("metadata")
	if field == nil || field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
		return nil
	}
	return field
}

func setFieldValue(msg protoreflect.Message, field protoreflect.FieldDescriptor, value extension.Value, strictUnknown bool) error {
	if field.IsList() || field.IsMap() {
		return errors.New("repeated and map protobuf fields are not supported by typed object binding yet")
	}
	if value.Kind() == extension.KindNull {
		msg.Clear(field)
		return nil
	}
	if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
		object, ok := value.ObjectValue()
		if !ok {
			return errors.New("expected object")
		}
		return objectIntoMessage(msg.Mutable(field).Message(), object, strictUnknown)
	}
	converted, err := scalarToProtoValue(field, value)
	if err != nil {
		return err
	}
	msg.Set(field, converted)
	return nil
}

func scalarToProtoValue(field protoreflect.FieldDescriptor, value extension.Value) (protoreflect.Value, error) {
	switch field.Kind() {
	case protoreflect.BoolKind:
		raw, ok := value.BoolValue()
		if !ok {
			return protoreflect.Value{}, errors.New("expected bool")
		}
		return protoreflect.ValueOfBool(raw), nil
	case protoreflect.StringKind:
		raw, ok := value.StringValue()
		if !ok {
			return protoreflect.Value{}, errors.New("expected string")
		}
		return protoreflect.ValueOfString(raw), nil
	case protoreflect.BytesKind:
		raw, ok := value.BytesValue()
		if !ok {
			return protoreflect.Value{}, errors.New("expected bytes")
		}
		return protoreflect.ValueOfBytes(raw), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		raw, ok := int64FromValue(value)
		if !ok || raw < math.MinInt32 || raw > math.MaxInt32 {
			return protoreflect.Value{}, errors.New("expected int32")
		}
		return protoreflect.ValueOfInt32(int32(raw)), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		raw, ok := int64FromValue(value)
		if !ok {
			return protoreflect.Value{}, errors.New("expected int64")
		}
		return protoreflect.ValueOfInt64(raw), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		raw, ok := uint64FromValue(value)
		if !ok || raw > math.MaxUint32 {
			return protoreflect.Value{}, errors.New("expected uint32")
		}
		return protoreflect.ValueOfUint32(uint32(raw)), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		raw, ok := uint64FromValue(value)
		if !ok {
			return protoreflect.Value{}, errors.New("expected uint64")
		}
		return protoreflect.ValueOfUint64(raw), nil
	case protoreflect.FloatKind:
		raw, ok := float64FromValue(value)
		if !ok {
			return protoreflect.Value{}, errors.New("expected float")
		}
		return protoreflect.ValueOfFloat32(float32(raw)), nil
	case protoreflect.DoubleKind:
		raw, ok := float64FromValue(value)
		if !ok {
			return protoreflect.Value{}, errors.New("expected double")
		}
		return protoreflect.ValueOfFloat64(raw), nil
	case protoreflect.EnumKind:
		raw, ok := value.StringValue()
		if !ok {
			return protoreflect.Value{}, errors.New("expected enum string")
		}
		enum := field.Enum().Values().ByName(protoreflect.Name(raw))
		if enum == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum value %q", raw)
		}
		return protoreflect.ValueOfEnum(enum.Number()), nil
	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported protobuf field kind %s", field.Kind())
	}
}

func int64FromValue(value extension.Value) (int64, bool) {
	if raw, ok := value.IntValue(); ok {
		return raw, true
	}
	if raw, ok := value.UintValue(); ok && raw <= math.MaxInt64 {
		return int64(raw), true
	}
	if raw, ok := value.FloatValue(); ok && math.Trunc(raw) == raw && raw >= math.MinInt64 && raw <= math.MaxInt64 {
		return int64(raw), true
	}
	return 0, false
}

func uint64FromValue(value extension.Value) (uint64, bool) {
	if raw, ok := value.UintValue(); ok {
		return raw, true
	}
	if raw, ok := value.IntValue(); ok && raw >= 0 {
		return uint64(raw), true
	}
	if raw, ok := value.FloatValue(); ok && math.Trunc(raw) == raw && raw >= 0 && raw <= math.MaxUint64 {
		return uint64(raw), true
	}
	return 0, false
}

func float64FromValue(value extension.Value) (float64, bool) {
	if raw, ok := value.FloatValue(); ok {
		return raw, true
	}
	if raw, ok := value.IntValue(); ok {
		return float64(raw), true
	}
	if raw, ok := value.UintValue(); ok {
		return float64(raw), true
	}
	return 0, false
}

func messageToObject(msg protoreflect.Message) extension.Object {
	if !msg.IsValid() {
		return extension.Object{}
	}
	fields := msg.Descriptor().Fields()
	out := make(extension.Object, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.IsList() || field.IsMap() {
			continue
		}
		out[string(field.Name())] = protoValueToExtension(field, msg.Get(field))
	}
	return out
}

func protoValueToExtension(field protoreflect.FieldDescriptor, value protoreflect.Value) extension.Value {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return extension.Bool(value.Bool())
	case protoreflect.StringKind:
		return extension.String(value.String())
	case protoreflect.BytesKind:
		return extension.Bytes(value.Bytes())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return extension.Int(value.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return extension.Uint(value.Uint())
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return extension.Float(value.Float())
	case protoreflect.EnumKind:
		enum := field.Enum().Values().ByNumber(value.Enum())
		if enum == nil {
			return extension.Int(int64(value.Enum()))
		}
		return extension.String(string(enum.Name()))
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if !value.Message().IsValid() {
			return extension.Null()
		}
		return extension.ObjectValue(messageToObject(value.Message()))
	default:
		return extension.Null()
	}
}

func normalizeEncoding(value string) string {
	switch value {
	case "", PayloadEncodingJSON:
		return PayloadEncodingJSON
	case PayloadEncodingProtobuf:
		return PayloadEncodingProtobuf
	default:
		return value
	}
}
