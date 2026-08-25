package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
)

func TestRegisterTypedFrameHandlersValidation(t *testing.T) {
	binding := frameTestBinding()
	handler := func(context.Context, proto.Message) (proto.Message, error) {
		return &testprotos.TestResponse{}, nil
	}

	if err := RegisterTypedFrameHandlers(nil, TypedServiceHandlers{
		"media:process_asset:v1:requested": {Binding: binding, Handler: handler},
	}); err == nil {
		t.Fatal("expected nil router to fail")
	}
	if err := RegisterTypedFrameHandlers(grpcsvc.NewRouter(), TypedServiceHandlers{
		"media:process_asset:v1:requested": {Binding: binding},
	}); err == nil {
		t.Fatal("expected nil handler to fail")
	}
	if err := RegisterTypedFrameHandlers(grpcsvc.NewRouter(), TypedServiceHandlers{
		"media:process_asset:v1:success": {Binding: binding, Handler: handler},
	}); err == nil {
		t.Fatal("expected non-requested event type to fail")
	}
	if err := RegisterTypedFrameHandlers(grpcsvc.NewRouter(), TypedServiceHandlers{
		"media:process_asset:v1:requested": {Handler: handler},
	}); err == nil {
		t.Fatal("expected invalid binding to fail")
	}
}

func TestRegisterTypedFrameHandlersDispatchPreservesFrameMetadata(t *testing.T) {
	router := grpcsvc.NewRouter()
	binding := frameTestBinding()
	err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: binding,
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				if typed.GetMetadata().GetCorrelationId() != "corr_frame" {
					t.Fatalf("metadata.correlation_id = %q", typed.GetMetadata().GetCorrelationId())
				}
				if typed.GetMetadata().GetGlobalContext().GetSource() != "payload" {
					t.Fatalf("metadata.global_context.source = %q", typed.GetMetadata().GetGlobalContext().GetSource())
				}
				return &testprotos.TestResponse{ResourceId: typed.GetWorkspaceId(), Status: "complete"}, nil
			},
		},
	}, ConcurrencyOptions{MaxConcurrent: 1, AcquireTimeout: time.Second})
	if err != nil {
		t.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}

	payload, err := proto.Marshal(&testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			GlobalContext: &testprotos.GlobalContext{Source: "payload"},
		},
		WorkspaceId: "wrk_frame",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	responseFrame, err := router.DispatchFrame(context.Background(), grpcsvc.Frame{
		EventType:     "media:process_asset:v1:requested",
		Payload:       payload,
		CorrelationID: "corr_frame",
		SchemaVersion: "schema_v1",
	})
	if err != nil {
		t.Fatalf("DispatchFrame() error = %v", err)
	}
	if responseFrame.EventType != "media:process_asset:v1:requested" ||
		responseFrame.CorrelationID != "corr_frame" ||
		responseFrame.SchemaVersion != "schema_v1" {
		t.Fatalf("frame metadata not preserved: %+v", responseFrame)
	}
	var response testprotos.TestResponse
	if err := proto.Unmarshal(responseFrame.Payload, &response); err != nil {
		t.Fatalf("response Unmarshal() error = %v", err)
	}
	if response.ResourceId != "wrk_frame" || response.Status != "complete" {
		t.Fatalf("unexpected response: %+v", &response)
	}
}

func TestRegisterTypedFrameHandlersMapsHandlerErrors(t *testing.T) {
	router := grpcsvc.NewRouter()
	expected := errors.New("typed handler failed")
	err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: frameTestBinding(),
			Handler: func(context.Context, proto.Message) (proto.Message, error) {
				return nil, expected
			},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}

	_, err = router.DispatchFrame(context.Background(), grpcsvc.Frame{
		EventType: "media:process_asset:v1:requested",
	})
	if !errors.Is(err, expected) {
		t.Fatalf("expected handler error, got %v", err)
	}
}

func TestRegisterTypedFrameHandlersOptInProtobufDecodeReuse(t *testing.T) {
	router := grpcsvc.NewRouter()
	const eventType = "media:process_asset:v1:requested"
	binding := frameTestBinding()
	binding.ProtobufDecodeReuse = protoapi.ProtobufDecodeReuseCompleteMessages
	// Reuse is asserted across a run rather than demanded on one pair: the
	// adapter pools messages in a sync.Pool, which does not promise Get returns
	// a previously Put object. A GC between two dispatches drains it and the
	// pointers legitimately differ. See the same reasoning in
	// registry.TestTypedDispatchOptInProtobufDecodeReuse.
	calls := 0
	reuseSeen := false
	seen := map[*testprotos.TestRequest]struct{}{}
	err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		eventType: {
			Binding: binding,
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				if _, ok := seen[typed]; ok {
					reuseSeen = true
				}
				seen[typed] = struct{}{}

				// Residue from a previous decode surfaces here as the wrong
				// workspace, hash, or size — the property that makes reuse
				// unsafe if it breaks.
				wantWorkspace, wantHash, wantSize := "wrk_1", "sha256:one", int64(16)
				if calls%2 == 1 {
					wantWorkspace, wantHash, wantSize = "wrk_2", "sha256:two", int64(32)
				}
				if typed.GetWorkspaceId() != wantWorkspace || typed.GetHash() != wantHash || typed.GetSize() != wantSize {
					t.Errorf("call %d decoded %+v, want workspace=%s hash=%s size=%d",
						calls, typed, wantWorkspace, wantHash, wantSize)
				}
				calls++
				return &testprotos.TestResponse{ResourceId: typed.GetWorkspaceId(), Status: "complete"}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}
	firstPayload, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_1",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:one",
	})
	if err != nil {
		t.Fatalf("Marshal() first error = %v", err)
	}
	secondPayload, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_2",
		ContentType: "application/octet-stream",
		Size:        32,
		Hash:        "sha256:two",
	})
	if err != nil {
		t.Fatalf("Marshal() second error = %v", err)
	}
	const dispatches = 64
	for i := range dispatches {
		payload := firstPayload
		if i%2 == 1 {
			payload = secondPayload
		}
		if _, err := router.DispatchFrame(context.Background(), grpcsvc.Frame{EventType: eventType, Payload: payload}); err != nil {
			t.Fatalf("DispatchFrame() %d error = %v", i, err)
		}
	}
	if calls != dispatches {
		t.Fatalf("handler calls = %d, want %d", calls, dispatches)
	}
	if !reuseSeen {
		t.Fatalf("no pooled message was ever reused across %d dispatches; the frame adapter is not pooling", dispatches)
	}
}

func BenchmarkTypedFrameAdapterDispatch(b *testing.B) {
	router := grpcsvc.NewRouter()
	if err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: frameTestBinding(),
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				return &testprotos.TestResponse{ResourceId: typed.GetWorkspaceId(), Status: "complete"}, nil
			},
		},
	}); err != nil {
		b.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}
	payload, err := proto.Marshal(&testprotos.TestRequest{WorkspaceId: "wrk_bench"})
	if err != nil {
		b.Fatalf("Marshal() error = %v", err)
	}
	frame := grpcsvc.Frame{
		EventType:     "media:process_asset:v1:requested",
		Payload:       payload,
		CorrelationID: "corr_bench",
		SchemaVersion: "schema_v1",
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := router.DispatchFrame(context.Background(), frame); err != nil {
			b.Fatalf("DispatchFrame() error = %v", err)
		}
	}
}

func BenchmarkTypedFrameAdapterDispatchNoMetadata(b *testing.B) {
	router := grpcsvc.NewRouter()
	if err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: frameTestBinding(),
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				return &testprotos.TestResponse{ResourceId: typed.GetWorkspaceId(), Status: "complete"}, nil
			},
		},
	}); err != nil {
		b.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}
	payload, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_bench",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:bench",
	})
	if err != nil {
		b.Fatalf("Marshal() error = %v", err)
	}
	frame := grpcsvc.Frame{
		EventType: "media:process_asset:v1:requested",
		Payload:   payload,
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := router.DispatchFrame(context.Background(), frame); err != nil {
			b.Fatalf("DispatchFrame() error = %v", err)
		}
	}
}

func BenchmarkTypedFrameAdapterDispatchReuse(b *testing.B) {
	router := grpcsvc.NewRouter()
	binding := frameTestBinding()
	binding.ProtobufDecodeReuse = protoapi.ProtobufDecodeReuseCompleteMessages
	if err := RegisterTypedFrameHandlers(router, TypedServiceHandlers{
		"media:process_asset:v1:requested": {
			Binding: binding,
			Handler: func(_ context.Context, request proto.Message) (proto.Message, error) {
				typed := request.(*testprotos.TestRequest)
				return &testprotos.TestResponse{ResourceId: typed.GetWorkspaceId(), Status: "complete"}, nil
			},
		},
	}); err != nil {
		b.Fatalf("RegisterTypedFrameHandlers() error = %v", err)
	}
	payload, err := proto.Marshal(&testprotos.TestRequest{
		WorkspaceId: "wrk_bench",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:bench",
	})
	if err != nil {
		b.Fatalf("Marshal() error = %v", err)
	}
	frame := grpcsvc.Frame{
		EventType: "media:process_asset:v1:requested",
		Payload:   payload,
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := router.DispatchFrame(context.Background(), frame); err != nil {
			b.Fatalf("DispatchFrame() error = %v", err)
		}
	}
}

func frameTestBinding() protoapi.Binding {
	return protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
}
