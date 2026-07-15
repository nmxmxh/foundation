package projectiongw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"google.golang.org/protobuf/proto"
)

// orgContextHandler injects an authenticated organization so SecurityTenantFunc
// resolves a tenant — mimicking the auth middleware that runs upstream.
func orgContextHandler(org string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(security.ContextWithOrganizationID(r.Context(), org)))
	})
}

func TestSnapshotHandlerServesProto(t *testing.T) {
	gw := newTestGateway(t)
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Fatalf("content-type = %q", ct)
	}
	var snap foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got := snap.GetBatch().GetMutations(); len(got) != 1 || got[0].GetRecordId() != "tick_1" {
		t.Fatalf("snapshot mutations = %+v", got)
	}
}

func TestSnapshotHandlerRejectsUnauthenticated(t *testing.T) {
	gw := newTestGateway(t)
	handler := gw.Handler(HandlerConfig{}) // no org in context
	req := httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSubscribeHandlerStreamsDeltaOverGorilla(t *testing.T) {
	gw := newTestGateway(t)
	srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/signals/ticks"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial err=%v", err)
	}
	defer conn.Close()

	// Apply after the subscription is live; the delta must arrive as a binary frame.
	time.Sleep(50 * time.Millisecond)
	if _, err := gw.ApplyEnvelopes(context.Background(), "signals", mustEnvelope(t, tickMutation("tick_9", 9, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read err=%v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("frame type = %d, want binary", msgType)
	}
	decoded, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("frame decode err=%v", err)
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
		t.Fatalf("batch decode err=%v", err)
	}
	if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick_9" {
		t.Fatalf("delta = %+v", batch.GetMutations())
	}
}

func mustEnvelope(t *testing.T, muts ...*foundationpb.RecordMutation) []events.Envelope {
	t.Helper()
	env, err := hermes.NewProjectionEnvelope(muts, "corr-e2e")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	return []events.Envelope{env}
}

// TestMultiplexHandlerStreamsTwoScopesOverOneConn covers the multiplexed
// stream: one WebSocket at the gateway root carries deltas for every scope the
// client subscribes to via control frames, each frame routable by the
// scope its mutations carry. This is what keeps an N-collection screen at one
// socket instead of N.
func TestMultiplexHandlerStreamsTwoScopesOverOneConn(t *testing.T) {
	gw := newTestGateway(t)
	srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial err=%v", err)
	}
	defer conn.Close()

	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":"ticks"},{"domain":"signals","collection":"quotes"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}

	// Wait until both hub subscriptions are registered before broadcasting.
	deadline := time.Now().Add(2 * time.Second)
	for {
		ticks := gw.Hub().SubscriberCount("org_1:signals:ticks")
		quotes := gw.Hub().SubscriberCount("org_1:signals:quotes")
		if ticks == 1 && quotes == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriptions not registered: ticks=%d quotes=%d", ticks, quotes)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// ticks delta flows through the real apply path…
	if _, err := gw.ApplyEnvelopes(context.Background(), "signals", mustEnvelope(t, tickMutation("tick_7", 7, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	// …and the quotes delta is broadcast directly on its scope topic.
	quote := tickMutation("quote_1", 1, "OVS")
	quote.Collection = "quotes"
	frame, err := encodeFrame([]*foundationpb.RecordMutation{quote}, 1, "1", "test")
	if err != nil {
		t.Fatalf("encodeFrame() err=%v", err)
	}
	gw.Hub().Broadcast("org_1:signals:quotes", frame)

	got := map[string]string{}
	for len(got) < 2 {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ws read err=%v (received so far: %v)", err, got)
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		decoded, err := events.FromBinary(data)
		if err != nil {
			t.Fatalf("frame decode err=%v", err)
		}
		var batch foundationpb.RecordMutationBatch
		if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
			t.Fatalf("batch decode err=%v", err)
		}
		for _, m := range batch.GetMutations() {
			got[m.GetCollection()] = m.GetRecordId()
		}
	}
	if got["ticks"] != "tick_7" || got["quotes"] != "quote_1" {
		t.Fatalf("multiplexed frames = %v", got)
	}

	// Unsubscribing one scope releases only that hub subscription.
	unsubscribe := `{"type":"unsubscribe","scopes":[{"domain":"signals","collection":"quotes"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(unsubscribe)); err != nil {
		t.Fatalf("unsubscribe write err=%v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount("org_1:signals:quotes") != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("quotes subscription not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gw.Hub().SubscriberCount("org_1:signals:ticks") != 1 {
		t.Fatalf("ticks subscription must survive the quotes unsubscribe")
	}
}

// TestMultiplexHandlerRejectsUnauthenticated keeps the multiplexed root as
// gated as the per-scope endpoints: no authenticated tenant, no upgrade.
func TestMultiplexHandlerRejectsUnauthenticated(t *testing.T) {
	gw := newTestGateway(t)
	srv := httptest.NewServer(gw.Handler(HandlerConfig{}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		conn.Close()
		t.Fatalf("expected unauthenticated multiplex upgrade to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %+v", resp)
	}
	_ = resp.Body.Close()
}

// TestSubscribeHandlerStreamsDelta is the happy-path WebSocket subscription: a
// client subscribes, the gateway applies a record, and the accepted delta is
// written to the socket as a binary frame carrying a decodable envelope. This
// exercises SubscribeHandler's frame-write path end to end through the store's
// observer seam (apply -> onApplied -> hub -> socket).
func TestSubscribeHandlerStreamsDelta(t *testing.T) {
	gw := newTestGateway(t)
	srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/projections/signals/ticks", nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial err=%v", err)
	}
	defer conn.Close()

	// Wait for the subscription to register so the apply's broadcast has a target.
	key := ScopeKey(scope())
	deadline := time.Now().Add(time.Second)
	for gw.Hub().SubscriberCount(key) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() err=%v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("delta frame type = %d, want binary", msgType)
	}
	if _, err := events.FromBinary(data); err != nil {
		t.Fatalf("delta frame not a decodable envelope: %v", err)
	}
}

// dialMultiplex opens an authenticated multiplexed stream at the gateway root
// and returns the connection, closing the response body on cleanup.
func dialMultiplex(t *testing.T, gw *Gateway) (*httptest.Server, *websocket.Conn) {
	t.Helper()
	srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	if err != nil {
		srv.Close()
		t.Fatalf("ws dial err=%v", err)
	}
	t.Cleanup(func() { conn.Close(); srv.Close() })
	return srv, conn
}

// TestMultiplexHandlerRejectsInvalidScopeWithControlError proves a bad scope in
// a subscribe command is answered with a scoped "error" control frame rather
// than tearing the whole multiplexed connection down — the other scopes on the
// socket must keep flowing.
func TestMultiplexHandlerRejectsInvalidScopeWithControlError(t *testing.T) {
	gw := newTestGateway(t)
	_, conn := dialMultiplex(t, gw)

	// Empty collection fails validateScope; the domain is echoed back on the notice.
	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":""}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("expected a control-error frame, got read err=%v", err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var frame ControlFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("control frame decode err=%v", err)
		}
		if frame.Type != ControlError {
			t.Fatalf("control frame type = %q, want %q", frame.Type, ControlError)
		}
		if frame.Domain != "signals" || frame.Collection != "" {
			t.Fatalf("control-error scope = %q/%q, want signals/<empty>", frame.Domain, frame.Collection)
		}
		break
	}
	// No hub subscription should have been created for the rejected scope.
	if n := gw.Hub().SubscriberCount("org_1:signals:"); n != 0 {
		t.Fatalf("rejected scope registered %d subscriptions, want 0", n)
	}
}

// TestMultiplexHandlerIgnoresMalformedFrames proves the reader loop survives a
// binary frame and an unparseable text frame — both are dropped without
// tearing the stream down — so a following valid subscribe still registers.
func TestMultiplexHandlerIgnoresMalformedFrames(t *testing.T) {
	gw := newTestGateway(t)
	_, conn := dialMultiplex(t, gw)

	// A binary frame (wrong message type) and garbage text are both ignored.
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("binary write err=%v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not json")); err != nil {
		t.Fatalf("garbage write err=%v", err)
	}
	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":"ticks"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount("org_1:signals:ticks") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("subscribe after malformed frames did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMultiplexHandlerSubscribeIsIdempotent proves subscribing the same scope
// twice yields exactly one hub subscription — a client that re-sends its scope
// set on reconnect must not double-subscribe.
func TestMultiplexHandlerSubscribeIsIdempotent(t *testing.T) {
	gw := newTestGateway(t)
	_, conn := dialMultiplex(t, gw)

	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":"ticks"},{"domain":"signals","collection":"ticks"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount("org_1:signals:ticks") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A second subscribe for the already-subscribed scope must not add another.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("re-subscribe write err=%v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := gw.Hub().SubscriberCount("org_1:signals:ticks"); n != 1 {
		t.Fatalf("duplicate subscribe registered %d subscriptions, want 1", n)
	}
}

// TestMultiplexHandlerSendsScopedResyncOnDrop drives the per-scope pump's
// slow-consumer path: when the hub sheds frames for a scope, the client
// receives a resync control frame tagged with that scope so it reconciles only
// the gapped collection.
func TestMultiplexHandlerSendsScopedResyncOnDrop(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20})
	gw, err := NewGateway(store, 1) // queue of 1 drops readily under a burst
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()
	_, conn := dialMultiplex(t, gw)

	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":"ticks"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}
	key := "org_1:signals:ticks"
	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount(key) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription not registered")
		}
		time.Sleep(time.Millisecond)
	}
	for i := range 20000 {
		gw.Hub().Broadcast(key, Frame{Envelope: []byte("delta"), Watermark: fmtTick(i), Epoch: 1})
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	sawResync := false
	for range 60 {
		msgType, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var frame ControlFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		if frame.Type == ControlResync && frame.Collection == "ticks" {
			sawResync = true
			break
		}
	}
	if !sawResync {
		t.Fatal("expected a scoped resync control frame after dropped deltas")
	}
}

// TestMultiplexHandlerTeardownReleasesOnClientClose proves a client that closes
// mid-stream releases every scope's hub subscription: the per-scope pump's
// write fails, cancels the shared context, and the deferred cleanup cancels the
// subscriptions. Without this, a dropped browser tab would leak fan-out state.
func TestMultiplexHandlerTeardownReleasesOnClientClose(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20})
	gw, err := NewGateway(store, 1)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()
	_, conn := dialMultiplex(t, gw)

	subscribe := `{"type":"subscribe","scopes":[{"domain":"signals","collection":"ticks"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}
	key := "org_1:signals:ticks"
	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount(key) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription not registered")
		}
		time.Sleep(time.Millisecond)
	}

	_ = conn.Close()
	// A burst against the gone client forces the pump's write to fail.
	for i := range 20000 {
		gw.Hub().Broadcast(key, Frame{Envelope: []byte("delta"), Watermark: fmtTick(i), Epoch: 1})
	}

	teardown := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount(key) > 0 {
		if time.Now().After(teardown) {
			t.Fatalf("subscriber count after client close = %d, want 0", gw.Hub().SubscriberCount(key))
		}
		time.Sleep(time.Millisecond)
	}
}
