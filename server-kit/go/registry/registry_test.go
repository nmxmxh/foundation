package registry

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"google.golang.org/protobuf/proto"
)

func TestRegisterAndDispatchMapHandler(t *testing.T) {
	registry := NewWithOptions(nil, nil, nil, Options{DispatchWorkers: -1})
	if registry.dispatchWorkers != 1 {
		t.Fatalf("dispatchWorkers default = %d", registry.dispatchWorkers)
	}
	err := registry.RegisterWithOptions("orders:create:v1:requested", func(ctx context.Context, payload map[string]any) (any, error) {
		if payload["name"] != "Ada" {
			t.Fatalf("payload = %+v", payload)
		}
		return map[string]any{"ok": true}, nil
	}, bootstrap.ConcurrencyOptions{})
	if err != nil {
		t.Fatalf("RegisterWithOptions() error = %v", err)
	}
	result, ok, err := registry.Dispatch(context.Background(), "orders:create:v1:requested", map[string]any{"name": "Ada"})
	if err != nil || !ok {
		t.Fatalf("Dispatch() ok=%v err=%v", ok, err)
	}
	if result["ok"] != true {
		t.Fatalf("unexpected result: %+v", result)
	}
	events := registry.RegisteredEventTypes()
	if len(events) != 1 || events[0] != "orders:create:v1:requested" {
		t.Fatalf("registered events = %+v", events)
	}
}

func TestRegisterValidationFailures(t *testing.T) {
	registry := New(nil, nil, nil)
	cases := []struct {
		name      string
		eventType string
		handler   bootstrap.HandlerFunc
	}{
		{"blank", " ", func(context.Context, map[string]any) (any, error) { return nil, nil }},
		{"nil handler", "orders:create:v1:requested", nil},
		{"terminal state", "orders:create:v1:success", func(context.Context, map[string]any) (any, error) { return nil, nil }},
		{"invalid contract", "orders:create:bad state:requested", func(context.Context, map[string]any) (any, error) { return nil, nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := registry.RegisterWithOptions(tc.eventType, tc.handler, bootstrap.ConcurrencyOptions{}); err == nil {
				t.Fatalf("expected registration error")
			}
		})
	}
}

func TestRegisterTypedValidationFailures(t *testing.T) {
	registry := New(nil, nil, nil)
	validBinding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	handler := func(context.Context, proto.Message) (proto.Message, error) { return &testprotos.TestResponse{}, nil }
	cases := []struct {
		name      string
		eventType string
		binding   protoapi.Binding
		handler   bootstrap.TypedHandlerFunc
	}{
		{"blank", " ", validBinding, handler},
		{"nil handler", "media:process_asset:v1:requested", validBinding, nil},
		{"terminal state", "media:process_asset:v1:success", validBinding, handler},
		{"invalid contract", "media:process bad:v1:requested", validBinding, handler},
		{"invalid binding", "media:process_asset:v1:requested", protoapi.Binding{}, handler},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := registry.RegisterTypedWithOptions(tc.eventType, tc.binding, tc.handler, bootstrap.ConcurrencyOptions{}); err == nil {
				t.Fatalf("expected typed registration error")
			}
		})
	}
}

func TestDispatchInputMissesAndErrors(t *testing.T) {
	registry := New(nil, nil, nil)
	if _, ok, err := registry.Dispatch(context.Background(), "orders:create:bad state:requested", nil); err == nil || ok {
		t.Fatalf("expected invalid event error")
	}
	if _, ok, err := registry.Dispatch(context.Background(), "orders:create:v1:requested", nil); err != nil || ok {
		t.Fatalf("missing handler ok=%v err=%v", ok, err)
	}
	if err := registry.Register("orders:create:v1:requested", func(context.Context, map[string]any) (any, error) {
		return nil, errors.New("boom")
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, ok, err := registry.DispatchInput(context.Background(), "orders:create:v1:requested", DispatchInput{
		PayloadEncoding: protoapi.PayloadEncodingProtobuf,
	}); err == nil || !ok {
		t.Fatalf("expected protobuf unsupported error, ok=%v err=%v", ok, err)
	}
	if _, ok, err := registry.Dispatch(context.Background(), "orders:create:v1:requested", nil); err == nil || !ok {
		t.Fatalf("expected handler error, ok=%v err=%v", ok, err)
	}
}

func TestDispatchInputStreamResponseAndEncodingHelpers(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.Register("reports:stream:v1:requested", func(context.Context, map[string]any) (any, error) {
		return "stream-handle", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, ok, err := registry.DispatchInput(context.Background(), "reports:stream:v1:requested", DispatchInput{
		Payload:          nil,
		PayloadEncoding:  "",
		ResponseEncoding: "custom",
		Metadata:         nil,
	})
	if err != nil || !ok {
		t.Fatalf("DispatchInput() ok=%v err=%v", ok, err)
	}
	if result.Stream != "stream-handle" || result.PayloadEncoding != protoapi.PayloadEncodingJSON {
		t.Fatalf("unexpected stream result: %+v", result)
	}
	if got := normalizeEncoding("custom"); got != "custom" {
		t.Fatalf("normalizeEncoding custom = %q", got)
	}
	if got := normalizeResponseEncoding("", protoapi.PayloadEncodingProtobuf); got != protoapi.PayloadEncodingProtobuf {
		t.Fatalf("normalizeResponseEncoding = %q", got)
	}
}

func TestDispatchBytesMissAndDecodeError(t *testing.T) {
	registry := New(nil, nil, nil)
	if got, ok, err := registry.DispatchBytes(context.Background(), "media:process_asset:v1:requested", nil, nil); err != nil || ok || got != nil {
		t.Fatalf("missing DispatchBytes() got=%v ok=%v err=%v", got, ok, err)
	}
	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	if err := registry.RegisterTypedWithOptions(
		"media:process_asset:v1:requested",
		binding,
		func(context.Context, proto.Message) (proto.Message, error) {
			return &testprotos.TestResponse{}, nil
		},
		bootstrap.ConcurrencyOptions{},
	); err != nil {
		t.Fatalf("RegisterTypedWithOptions() error = %v", err)
	}
	if got, ok, err := registry.DispatchBytes(context.Background(), "media:process_asset:v1:requested", []byte{0xff}, nil); err == nil || !ok || got != nil {
		t.Fatalf("expected DispatchBytes decode error, got=%v ok=%v err=%v", got, ok, err)
	}
}

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
		t.Fatalf("unexpected response: %+v", &response)
	}
}

func TestTypedRegistryAndFrameDispatchParity(t *testing.T) {
	registry := New(nil, nil, nil)
	router := grpcsvc.NewRouter()
	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	handlers := bootstrap.TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: binding,
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				return &testprotos.TestResponse{
					ResourceId: typed.GetWorkspaceId() + ":" + typed.GetMetadata().GetCorrelationId(),
					Status:     "complete",
				}, nil
			},
		},
	}
	if err := bootstrap.RegisterTypedHandlers(registry, handlers); err != nil {
		t.Fatalf("RegisterTypedHandlers() error = %v", err)
	}
	if err := bootstrap.RegisterTypedFrameHandlers(router, handlers); err != nil {
		t.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}

	payload, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_parity"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	registryBytes, ok, err := registry.DispatchBytes(
		context.Background(),
		"media:process_asset:v1:requested",
		payload,
		map[string]any{"correlation_id": "corr_parity"},
	)
	if err != nil || !ok {
		t.Fatalf("DispatchBytes() ok=%v err=%v", ok, err)
	}
	frame, err := router.DispatchFrame(context.Background(), grpcsvc.Frame{
		EventType:     "media:process_asset:v1:requested",
		Payload:       payload,
		CorrelationID: "corr_parity",
		SchemaVersion: "schema_v1",
	})
	if err != nil {
		t.Fatalf("DispatchFrame() error = %v", err)
	}
	if frame.CorrelationID != "corr_parity" || frame.SchemaVersion != "schema_v1" {
		t.Fatalf("frame metadata not preserved: %+v", frame)
	}
	if !bytes.Equal(registryBytes, frame.Payload) {
		t.Fatalf("registry/frame response payload mismatch: registry=%x frame=%x", registryBytes, frame.Payload)
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

func TestListenValidationAndMemoryDispatch(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.Listen(context.Background(), "orders:create:v1:requested"); err == nil {
		t.Fatalf("expected missing redis client error")
	}

	client := redis.NewMemoryClient("test")
	registry = NewWithOptions(client, nil, nil, Options{DispatchWorkers: 2})
	if err := registry.Listen(context.Background()); err == nil {
		t.Fatalf("expected missing pattern error")
	}

	seen := make(chan map[string]any, 1)
	if err := registry.Register("orders:create:v1:requested", func(ctx context.Context, payload map[string]any) (any, error) {
		md := metadata.FromContext(ctx)
		if md.CorrelationID != "corr_123" {
			t.Fatalf("metadata correlation = %q", md.CorrelationID)
		}
		seen <- payload
		return map[string]any{"ok": true}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Listen(ctx, "orders:create:v1:requested"); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	env := eventcontract.Envelope{
		EventType:       "orders:create:v1:requested",
		Payload:         map[string]any{"id": "ord_1"},
		CorrelationID:   "corr_123",
		PayloadEncoding: protoapi.PayloadEncodingJSON,
	}
	raw, err := env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	if err := client.Publish(ctx, "orders:create:v1:requested", raw); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	select {
	case payload := <-seen:
		if payload["id"] != "ord_1" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for dispatch")
	}
}

func TestDispatchEnvelopeIgnoresDecodeMissAndProtobufLegacy(t *testing.T) {
	registry := New(nil, nil, nil)
	registry.dispatchEnvelope(context.Background(), []byte("not-json"))

	env := eventcontract.Envelope{EventType: "orders:create:v1:requested", Payload: map[string]any{"id": "ord_1"}}
	raw, err := env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	registry.dispatchEnvelope(context.Background(), raw)

	if err := registry.Register("orders:create:v1:requested", func(context.Context, map[string]any) (any, error) {
		t.Fatalf("legacy handler should not receive protobuf payload")
		return nil, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	env.PayloadEncoding = protoapi.PayloadEncodingProtobuf
	env.PayloadBytes = []byte{0x00, 0x01}
	raw, err = env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	registry.dispatchEnvelope(context.Background(), raw)
}

func TestDispatchEnvelopeTypedDecodeAndHandlerErrors(t *testing.T) {
	registry := New(nil, nil, nil)
	binding := protoapi.Binding{
		Request: func() proto.Message {
			return &testprotos.TestRequest{}
		},
		Response: func() proto.Message {
			return &testprotos.TestResponse{}
		},
	}
	calls := 0
	err := registry.RegisterTypedWithOptions(
		"media:process_asset:v1:requested",
		binding,
		func(context.Context, proto.Message) (proto.Message, error) {
			calls++
			return nil, errors.New("typed boom")
		},
		bootstrap.ConcurrencyOptions{MaxConcurrent: 1},
	)
	if err != nil {
		t.Fatalf("RegisterTypedWithOptions() error = %v", err)
	}

	env := eventcontract.Envelope{
		EventType:       "media:process_asset:v1:requested",
		PayloadEncoding: protoapi.PayloadEncodingProtobuf,
		PayloadBytes:    []byte{0xff},
		CorrelationID:   "corr_bad",
	}
	raw, err := env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	registry.dispatchEnvelope(context.Background(), raw)
	if calls != 0 {
		t.Fatalf("typed handler should not run after decode failure")
	}

	payload, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_1"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	env.PayloadBytes = payload
	raw, err = env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	registry.dispatchEnvelope(context.Background(), raw)
	if calls != 1 {
		t.Fatalf("typed handler calls = %d", calls)
	}
}
