package protoapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	PayloadEncodingJSON     = "json"
	PayloadEncodingProtobuf = "protobuf"
)

type MessageFactory func() proto.Message

type Binding struct {
	Request  MessageFactory
	Response MessageFactory
}

type TypedHandlerFunc func(context.Context, proto.Message) (proto.Message, error)

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
	return nil
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

func (b Binding) DecodeRequestMap(payload map[string]any, metadata map[string]any) (proto.Message, error) {
	msg, err := b.NewRequest()
	if err != nil {
		return nil, err
	}
	if err := decodeMapIntoMessage(msg, payload, metadata); err != nil {
		return nil, err
	}
	return msg, nil
}

func (b Binding) DecodeRequestBytes(payload []byte, metadata map[string]any) (proto.Message, error) {
	msg, err := b.NewRequest()
	if err != nil {
		return nil, err
	}
	if len(payload) > 0 {
		if err := proto.Unmarshal(payload, msg); err != nil {
			return nil, err
		}
	}
	if len(metadata) == 0 || !hasMetadataField(msg) {
		return msg, nil
	}
	asMap, err := MessageToMap(msg)
	if err != nil {
		return nil, err
	}
	if err := decodeMapIntoMessage(msg, asMap, metadata); err != nil {
		return nil, err
	}
	return msg, nil
}

func (b Binding) EncodeResponseMap(msg proto.Message) (map[string]any, error) {
	if msg == nil {
		return map[string]any{}, nil
	}
	return MessageToMap(msg)
}

func (b Binding) EncodeResponseBytes(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return []byte{}, nil
	}
	return proto.Marshal(msg)
}

func (b Binding) ResponseFromMap(payload map[string]any) (proto.Message, error) {
	msg, err := b.NewResponse()
	if err != nil {
		return nil, err
	}
	if err := decodeMapIntoMessage(msg, payload, nil); err != nil {
		return nil, err
	}
	return msg, nil
}

func MessageToMap(msg proto.Message) (map[string]any, error) {
	if msg == nil {
		return map[string]any{}, nil
	}
	raw, err := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Marshal(msg)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeMapIntoMessage(msg proto.Message, payload map[string]any, metadata map[string]any) error {
	working := cloneMap(payload)
	if len(working) == 0 {
		working = map[string]any{}
	}
	if hasMetadataField(msg) && len(metadata) > 0 {
		working["metadata"] = mergeMetadataMap(asMap(working["metadata"]), metadata)
	}
	raw, err := json.Marshal(working)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{
		DiscardUnknown: false,
	}.Unmarshal(raw, msg)
}

func hasMetadataField(msg proto.Message) bool {
	if msg == nil {
		return false
	}
	field := msg.ProtoReflect().Descriptor().Fields().ByName("metadata")
	return field != nil && field.Kind() == protoreflect.MessageKind
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = deepClone(value)
	}
	return out
}

func deepClone(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, deepClone(item))
		}
		return out
	default:
		return typed
	}
}

func asMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok || typed == nil {
		return map[string]any{}
	}
	return cloneMap(typed)
}

func mergeMetadataMap(current map[string]any, incoming map[string]any) map[string]any {
	merged := cloneMap(current)
	for key, value := range incoming {
		if nestedCurrent, ok := merged[key].(map[string]any); ok {
			if nestedIncoming, ok := value.(map[string]any); ok {
				merged[key] = mergeMetadataMap(nestedCurrent, nestedIncoming)
				continue
			}
		}
		merged[key] = deepClone(value)
	}
	return merged
}

func DecodeByEncoding(binding Binding, encoding string, payload map[string]any, payloadBytes []byte, metadata map[string]any) (proto.Message, error) {
	switch normalizeEncoding(encoding) {
	case PayloadEncodingProtobuf:
		return binding.DecodeRequestBytes(payloadBytes, metadata)
	case PayloadEncodingJSON:
		return binding.DecodeRequestMap(payload, metadata)
	default:
		return nil, fmt.Errorf("unsupported payload encoding %q", encoding)
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
