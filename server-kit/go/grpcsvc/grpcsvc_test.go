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
