package grpcsvc

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const bufSize = 1024 * 1024

func TestDispatchOverBufconn(t *testing.T) {
	conn, cleanup := startTestServer(t, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()

	res, err := Dispatch(context.Background(), conn, Envelope{
		EventType:     "order:create:v1:requested",
		Payload:       map[string]any{"id": "ord_1"},
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if res.EventType != "order:create:v1:success" || res.Payload["id"] != "ord_1" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestDispatchRequiresAuth(t *testing.T) {
	listener := bufconn.Listen(bufSize)
	router := testRouter(t)
	server := NewServer(router, ServerOptions{AuthToken: "secret"})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	conn, err := grpc.NewClient("bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = Dispatch(context.Background(), conn, Envelope{EventType: "order:create:v1:requested"})
	if err == nil {
		t.Fatalf("expected unauthorized dispatch to fail")
	}
}

func TestServeStopsWhenContextIsCanceled(t *testing.T) {
	listener := bufconn.Listen(bufSize)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- Serve(ctx, listener, testRouter(t), ServerOptions{})
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		_ = listener.Close()
		t.Fatal("Serve did not stop after context cancellation")
	}

	var svc RuntimeServiceServer = &runtimeService{}
	svc.mustEmbedRuntimeServiceServer()
}

func TestDispatchRejectsOversizedMessage(t *testing.T) {
	conn, cleanup := startTestServer(t, ServerOptions{AuthToken: "secret", MaxMessageBytes: 256})
	defer cleanup()

	_, err := Dispatch(context.Background(), conn, Envelope{
		EventType: "order:create:v1:requested",
		Payload:   map[string]any{"body": string(make([]byte, 1024))},
	})
	if err == nil {
		t.Fatalf("expected oversized message to fail")
	}
}

func TestDispatchFrameOverBufconn(t *testing.T) {
	conn, cleanup := startTestServer(t, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()

	res, err := DispatchFrame(context.Background(), conn, Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	})
	if err != nil {
		t.Fatalf("DispatchFrame() error = %v", err)
	}
	if res.EventType != "order:create:v1:frame:success" || !bytes.Equal(res.Payload, []byte(`{"id":"ord_1"}`)) {
		t.Fatalf("unexpected response: %+v", res)
	}
	if res.CorrelationID != "corr_1" || res.SchemaVersion != "1.0" {
		t.Fatalf("frame identity was not preserved: %+v", res)
	}
}

func TestClientDispatchMethodsOverBufconn(t *testing.T) {
	conn, cleanup := startTestServer(t, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()
	client := NewClient(conn, ClientOptions{MaxMessageBytes: 64 * 1024})

	res, err := client.Dispatch(context.Background(), Envelope{
		EventType:     "order:create:v1:requested",
		Payload:       map[string]any{"id": "ord_2"},
		CorrelationID: "corr_2",
	})
	if err != nil {
		t.Fatalf("client Dispatch() error = %v", err)
	}
	if res.EventType != "order:create:v1:success" || res.Payload["id"] != "ord_2" {
		t.Fatalf("unexpected response: %+v", res)
	}

	frame, err := client.DispatchFrame(context.Background(), Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_2"}`),
		CorrelationID: "corr_2",
	})
	if err != nil {
		t.Fatalf("client DispatchFrame() error = %v", err)
	}
	if frame.EventType != "order:create:v1:frame:success" || frame.CorrelationID != "corr_2" {
		t.Fatalf("unexpected frame response: %+v", frame)
	}
	if _, err := client.DispatchFrame(context.Background(), Frame{EventType: ""}); err == nil {
		t.Fatalf("expected client frame validation error")
	}
}

func TestRouterValidationAndDirectFrameErrors(t *testing.T) {
	var nilRouter *Router
	if err := nilRouter.Register("order:create:v1:requested", func(context.Context, Envelope) (Envelope, error) { return Envelope{}, nil }); err == nil {
		t.Fatalf("expected nil router register error")
	}
	if err := nilRouter.RegisterFrame("order:create:v1:frame", func(context.Context, Frame) (Frame, error) { return Frame{}, nil }); err == nil {
		t.Fatalf("expected nil router register frame error")
	}
	if _, err := nilRouter.DispatchFrame(context.Background(), Frame{EventType: "order:create:v1:frame"}); err == nil {
		t.Fatalf("expected nil router dispatch frame error")
	}

	router := NewRouter()
	if err := router.Register("", func(context.Context, Envelope) (Envelope, error) { return Envelope{}, nil }); err == nil {
		t.Fatalf("expected blank handler registration error")
	}
	if err := router.Register("order:create:v1:requested", nil); err == nil {
		t.Fatalf("expected nil handler registration error")
	}
	handler := func(context.Context, Envelope) (Envelope, error) { return Envelope{}, nil }
	if err := router.Register("order:create:v1:requested", handler); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := router.Register("order:create:v1:requested", handler); err == nil {
		t.Fatalf("expected duplicate handler registration error")
	}

	if err := router.RegisterFrame("", func(context.Context, Frame) (Frame, error) { return Frame{}, nil }); err == nil {
		t.Fatalf("expected blank frame handler registration error")
	}
	if err := router.RegisterFrame("order:create:v1:frame", nil); err == nil {
		t.Fatalf("expected nil frame handler registration error")
	}
	frameHandler := func(context.Context, Frame) (Frame, error) { return Frame{EventType: "ok"}, nil }
	if err := router.RegisterFrame("order:create:v1:frame", frameHandler); err != nil {
		t.Fatalf("RegisterFrame() error = %v", err)
	}
	if err := router.RegisterFrame("order:create:v1:frame", frameHandler); err == nil {
		t.Fatalf("expected duplicate frame handler registration error")
	}
	if _, err := router.DispatchFrame(context.Background(), Frame{EventType: "missing"}); err == nil {
		t.Fatalf("expected missing frame handler error")
	}

	if _, err := (*DirectFrameClient)(nil).DispatchFrame(context.Background(), Frame{EventType: "order:create:v1:frame"}); err == nil {
		t.Fatalf("expected nil direct client error")
	}
	client := NewDirectFrameClient(router, ServerOptions{MaxCorrelationBytes: 3})
	if _, err := client.DispatchFrame(context.Background(), Frame{EventType: "order:create:v1:frame", CorrelationID: "too-long"}); err == nil {
		t.Fatalf("expected correlation bound error")
	}
	bound, err := NewBoundFrameClient(router, "order:create:v1:frame", ServerOptions{MaxCorrelationBytes: 3})
	if err != nil {
		t.Fatalf("NewBoundFrameClient() error = %v", err)
	}
	if _, err := bound.DispatchFrame(context.Background(), Frame{EventType: "missing"}); err == nil {
		t.Fatalf("expected bound client event mismatch")
	}
	if _, err := bound.DispatchFrame(context.Background(), Frame{EventType: "order:create:v1:frame", CorrelationID: "too-long"}); err == nil {
		t.Fatalf("expected bound client correlation bound error")
	}
	if _, err := NewBoundFrameClient(router, "missing", ServerOptions{}); err == nil {
		t.Fatalf("expected missing bound route error")
	}
}

func TestDispatchHandlersDecodeRouterAndInterceptorPaths(t *testing.T) {
	service := &runtimeService{router: NewRouter()}
	if _, err := dispatchHandler(service, context.Background(), func(any) error { return nil }, nil); err == nil {
		t.Fatalf("expected missing envelope handler error")
	}
	if _, err := dispatchFrameHandler(service, context.Background(), func(any) error { return nil }, nil); err == nil {
		t.Fatalf("expected missing frame handler error")
	}
	if _, err := dispatchHandler(&runtimeService{}, context.Background(), func(v any) error {
		*(v.(*Envelope)) = Envelope{EventType: "order:create:v1:requested"}
		return nil
	}, nil); err == nil {
		t.Fatalf("expected nil router dispatch error")
	}
	if _, err := dispatchHandler(service, context.Background(), func(any) error { return context.Canceled }, nil); err == nil {
		t.Fatalf("expected decode error")
	}

	if err := service.router.Register("order:create:v1:requested", func(context.Context, Envelope) (Envelope, error) {
		return Envelope{EventType: "ok"}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	called := false
	out, err := dispatchHandler(service, context.Background(), func(v any) error {
		*(v.(*Envelope)) = Envelope{EventType: "order:create:v1:requested"}
		return nil
	}, func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		called = info.FullMethod == dispatchMethod
		return handler(ctx, req)
	})
	if err != nil || !called || out.(Envelope).EventType != "ok" {
		t.Fatalf("intercepted dispatch out=%+v called=%v err=%v", out, called, err)
	}
}

func TestCodecValidationHelpers(t *testing.T) {
	if _, err := (binaryFrameCodec{}).Marshal("not-frame"); err == nil {
		t.Fatalf("expected marshal type error")
	}
	if err := (binaryFrameCodec{}).Unmarshal(nil, (*Frame)(nil)); err == nil {
		t.Fatalf("expected unmarshal target error")
	}
	if _, err := UnmarshalFrameView([]byte{0}); err == nil {
		t.Fatalf("expected truncated view error")
	}
	if first(nil) != "" || first([]string{"a", "b"}) != "a" {
		t.Fatalf("first helper failed")
	}
}

func TestFrameCodecRejectsTruncatedInput(t *testing.T) {
	var frame Frame
	if err := (binaryFrameCodec{}).Unmarshal([]byte{0, 0, 0, 8, 1}, &frame); err == nil {
		t.Fatalf("expected truncated binary frame to fail")
	}
}

func TestFrameCodecRejectsTrailingBytes(t *testing.T) {
	raw := AppendMarshalFrame(nil, Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	})
	raw = append(raw, 0)

	var frame Frame
	if err := (binaryFrameCodec{}).Unmarshal(raw, &frame); err == nil {
		t.Fatalf("expected trailing bytes to fail")
	}
	if _, err := UnmarshalFrameView(raw); err == nil {
		t.Fatalf("expected trailing bytes to fail for borrowed view")
	}
}

func TestDirectFrameClientValidatesEnvelopeBoundsBeforeDispatch(t *testing.T) {
	client := NewDirectFrameClient(testRouter(t), ServerOptions{
		MaxEventTypeBytes:   len("order:create:v1:frame") - 1,
		MaxCorrelationBytes: 64,
	})
	_, err := client.DispatchFrame(context.Background(), Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	})
	if err == nil {
		t.Fatalf("expected oversized event type to fail")
	}
}

func TestUnmarshalFrameViewSharesBackingBytes(t *testing.T) {
	raw := AppendMarshalFrame(nil, Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	})
	view, err := UnmarshalFrameView(raw)
	if err != nil {
		t.Fatalf("UnmarshalFrameView() error = %v", err)
	}
	if !bytes.Equal(view.EventType, []byte("order:create:v1:frame")) {
		t.Fatalf("event type = %q", string(view.EventType))
	}
	if !bytes.Equal(view.CorrelationID, []byte("corr_1")) || !bytes.Equal(view.SchemaVersion, []byte("1.0")) {
		t.Fatalf("frame view identity was not preserved: correlation=%q schema=%q", view.CorrelationID, view.SchemaVersion)
	}
	if len(view.Payload) == 0 || &view.Payload[0] != &raw[4+len("order:create:v1:frame")+4] {
		t.Fatalf("payload does not share backing frame bytes")
	}
}

func TestFrameStringTableBoundsControlVocabulary(t *testing.T) {
	table := newFrameStringTable(1)
	if got := table.internBytes(nil); got != "" {
		t.Fatalf("empty interned string = %q", got)
	}
	if got := table.internBytes([]byte("schema-v1")); got != "schema-v1" {
		t.Fatalf("interned string = %q", got)
	}
	if got := len(table.values.Load().(map[string]string)); got != 1 {
		t.Fatalf("intern table size = %d, want 1", got)
	}
	if got := table.internBytes([]byte("schema-v1")); got != "schema-v1" {
		t.Fatalf("reused interned string = %q", got)
	}
	if got := table.internBytes([]byte("overflow")); got != "overflow" {
		t.Fatalf("overflow string = %q", got)
	}
	if got := len(table.values.Load().(map[string]string)); got != 1 {
		t.Fatalf("bounded intern table size = %d, want 1", got)
	}
}

func BenchmarkDispatchOverBufconn(b *testing.B) {
	conn, cleanup := startTestServer(b, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()

	req := Envelope{
		EventType:     "order:create:v1:requested",
		Payload:       map[string]any{"id": "ord_1"},
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Dispatch(context.Background(), conn, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDispatchFrameOverBufconn(b *testing.B) {
	conn, cleanup := startTestServer(b, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()

	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DispatchFrame(context.Background(), conn, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClientDispatchFrameOverBufconn(b *testing.B) {
	conn, cleanup := startTestServer(b, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()
	client := NewClient(conn, ClientOptions{MaxMessageBytes: 64 * 1024})

	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DispatchFrame(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterDispatchFrameDirect(b *testing.B) {
	router := testRouter(b)
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := router.DispatchFrame(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDirectFrameClientDispatch(b *testing.B) {
	client := NewDirectFrameClient(testRouter(b), ServerOptions{})
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DispatchFrame(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBoundFrameClientDispatch(b *testing.B) {
	client, err := NewBoundFrameClient(testRouter(b), "order:create:v1:frame", ServerOptions{})
	if err != nil {
		b.Fatal(err)
	}
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DispatchFrame(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBoundFrameClientDispatchTrusted(b *testing.B) {
	client, err := NewBoundFrameClient(testRouter(b), "order:create:v1:frame", ServerOptions{})
	if err != nil {
		b.Fatal(err)
	}
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DispatchFrameTrusted(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBinaryFrameCodecRoundTrip(b *testing.B) {
	codec := binaryFrameCodec{}
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	var out Frame
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw, err := codec.Marshal(req)
		if err != nil {
			b.Fatal(err)
		}
		if err := codec.Unmarshal(raw, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBinaryFrameAppendRoundTrip(b *testing.B) {
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	var out Frame
	buf := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = AppendMarshalFrame(buf[:0], req)
		if err := (binaryFrameCodec{}).Unmarshal(buf, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBinaryFrameAppendViewRoundTrip(b *testing.B) {
	req := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	buf := make([]byte, 0, 256)
	var view FrameView
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = AppendMarshalFrame(buf[:0], req)
		var err error
		view, err = UnmarshalFrameView(buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(view.EventType) == 0 {
			b.Fatal("empty event type")
		}
	}
}

func BenchmarkGeneratedProtoMarshalAppendRoundTrip(b *testing.B) {
	req := &testprotos.TestRequest{
		Metadata: &testprotos.Metadata{
			CorrelationId: "corr_1",
			RequestId:     "req_1",
		},
		WorkspaceId: "wrk_1",
		ContentType: "application/octet-stream",
		Size:        16,
		Hash:        "sha256:abc",
	}
	opts := proto.MarshalOptions{}
	buf := make([]byte, 0, opts.Size(req))
	var out testprotos.TestRequest
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		raw, err := opts.MarshalAppend(buf, req)
		if err != nil {
			b.Fatal(err)
		}
		out.Reset()
		if err := proto.Unmarshal(raw, &out); err != nil {
			b.Fatal(err)
		}
	}
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

func startTestServer(tb testingTB, opts ServerOptions) (*grpc.ClientConn, func()) {
	tb.Helper()
	listener := bufconn.Listen(bufSize)
	router := testRouter(tb)
	server := NewServer(router, opts)
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	conn, err := Dial(ctx, "bufnet", ClientOptions{
		AuthToken:       opts.AuthToken,
		MaxMessageBytes: opts.MaxMessageBytes,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	cancel()
	if err != nil {
		tb.Fatalf("Dial() error = %v", err)
	}
	return conn, func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
}

func testRouter(tb testingTB) *Router {
	tb.Helper()
	router := NewRouter()
	if err := router.Register("order:create:v1:requested", func(_ context.Context, req Envelope) (Envelope, error) {
		return Envelope{
			EventType:     "order:create:v1:success",
			Payload:       req.Payload,
			CorrelationID: req.CorrelationID,
			SchemaVersion: req.SchemaVersion,
		}, nil
	}); err != nil {
		tb.Fatalf("Register() error = %v", err)
	}
	if err := router.RegisterFrame("order:create:v1:frame", func(_ context.Context, req Frame) (Frame, error) {
		return Frame{
			EventType:     "order:create:v1:frame:success",
			Payload:       req.Payload,
			CorrelationID: req.CorrelationID,
			SchemaVersion: req.SchemaVersion,
		}, nil
	}); err != nil {
		tb.Fatalf("RegisterFrame() error = %v", err)
	}
	return router
}
