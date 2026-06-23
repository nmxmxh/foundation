package projectiongw

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// TestEncodeFrameCorrelationDefault covers encodeFrame directly: a blank
// correlation ID falls back to the "projectiongw" default, and the produced
// Frame carries a decodable events.Envelope plus the supplied epoch/watermark.
func TestEncodeFrameCorrelationDefault(t *testing.T) {
	mutations := []*foundationpb.RecordMutation{tickMutation("tick_1", 7, "OVS")}

	frame, err := encodeFrame(mutations, 3, "7", "   ")
	if err != nil {
		t.Fatalf("encodeFrame() err=%v", err)
	}
	if frame.Epoch != 3 || frame.Watermark != "7" {
		t.Fatalf("frame epoch/watermark = %d/%q, want 3/7", frame.Epoch, frame.Watermark)
	}
	if len(frame.Envelope) == 0 {
		t.Fatal("frame envelope should not be empty")
	}

	env, err := events.FromBinary(frame.Envelope)
	if err != nil {
		t.Fatalf("EnvelopeFromBinary() err=%v", err)
	}
	if env.CorrelationID != "projectiongw" {
		t.Fatalf("correlation = %q, want projectiongw default", env.CorrelationID)
	}

	// An explicit correlation ID is preserved verbatim.
	frame, err = encodeFrame(mutations, 1, "1", "corr-xyz")
	if err != nil {
		t.Fatalf("encodeFrame(explicit) err=%v", err)
	}
	env, err = events.FromBinary(frame.Envelope)
	if err != nil {
		t.Fatalf("EnvelopeFromBinary(explicit) err=%v", err)
	}
	if env.CorrelationID != "corr-xyz" {
		t.Fatalf("correlation = %q, want corr-xyz", env.CorrelationID)
	}
}

// TestNewGatewayNilStore covers the constructor guard.
func TestNewGatewayNilStore(t *testing.T) {
	if _, err := NewGateway(nil, 0); err == nil {
		t.Fatal("NewGateway(nil) err = nil, want ErrNilStore")
	}
}

// TestFilterScope pins the collection/tenant scoping the gateway enforces over a
// partition that may hold multiple collections: domain, collection, and a
// non-empty tenant mismatch are each dropped, while a matching scope (including a
// mutation with no organization stamped) passes through.
func TestFilterScope(t *testing.T) {
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
	mutations := []*foundationpb.RecordMutation{
		tickMutation("keep", 1, "OVS"),                                       // matches
		{Domain: "other", Collection: "ticks", OrganizationId: "org_1"},      // domain mismatch
		{Domain: "signals", Collection: "bars", OrganizationId: "org_1"},     // collection mismatch
		{Domain: "signals", Collection: "ticks", OrganizationId: "org_evil"}, // tenant mismatch
		{Domain: "signals", Collection: "ticks"},                             // no org → passes
	}

	out := filterScope(mutations, scope)
	if len(out) != 2 {
		t.Fatalf("filterScope kept %d, want 2 (%+v)", len(out), out)
	}
	if out[0].GetRecordId() != "keep" {
		t.Fatalf("filterScope[0] = %q, want keep", out[0].GetRecordId())
	}
}

// TestOnAppliedBroadcastsToSubscribers drives the accepted-delta fan-out path:
// with a live subscriber on the scope, onApplied encodes a frame from the
// accepted mutations and broadcasts it to the hub queue.
func TestOnAppliedBroadcastsToSubscribers(t *testing.T) {
	gw := newTestGateway(t)
	sub := gw.Hub().Subscribe(scope())
	defer sub.Cancel()

	gw.onApplied("signals", []hermes.AppliedMutation{{
		Operation: hermes.OperationUpsert,
		Version:   5,
		Record: database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
		},
	}})

	select {
	case frame := <-sub.Frames:
		if len(frame.Envelope) == 0 {
			t.Fatal("broadcast frame envelope should not be empty")
		}
	default:
		t.Fatal("onApplied did not broadcast a frame to the subscriber")
	}
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
