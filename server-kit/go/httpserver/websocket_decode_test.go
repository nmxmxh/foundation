package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	kitcompress "github.com/nmxmxh/ovasabi_foundation/server-kit/go/compress"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// TestWSCompressedBinaryEnvelopeParity is a binary-lane parity test (TE-11): when
// WS compression is enabled, a compressed binary envelope must decode to the same
// request — and produce the same terminal :success — as the uncompressed binary
// frame. This exercises decodeWSEnvelope's decompress-then-decode fallback.
func TestWSCompressedBinaryEnvelopeParity(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:ping:v1:requested": func(context.Context, extension.Object) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	})
	s.wsCompressionEnabled = true

	conn := dialWS(t, srv, "format=binary")
	if _, _, err := conn.ReadMessage(); err != nil { // binary ack
		t.Fatalf("read ack: %v", err)
	}

	bin, err := requested("identity:ping:v1:requested", extension.Object{}).ToBinary()
	if err != nil {
		t.Fatalf("ToBinary: %v", err)
	}
	compressed, err := kitcompress.CompressGzip(bin, 6)
	if err != nil {
		t.Fatalf("CompressGzip: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, compressed); err != nil {
		t.Fatalf("write compressed: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	env, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.EventType != "identity:ping:v1:success" {
		t.Fatalf("response = %q, want :success — compressed lane did not reach the handler", env.EventType)
	}
}

// TestRegisterWSConnectionEnforcesCapacityOnRegister covers the capacity guard on
// the register path for a connection that did not pre-reserve a slot (TE-18 hard
// bound). With the slot count already at the max, registering an unreserved
// connection must be refused rather than overcommitting the server.
func TestRegisterWSConnectionEnforcesCapacityOnRegister(t *testing.T) {
	s := newSmokeServer(t)
	s.ConfigureWebSocket(true, 1, false) // capacity 1
	s.ws.connectionCnt.Add(1)            // the single slot is already taken

	conn := &wsConnection{id: "conn_unreserved", reserved: false}
	if s.registerWSConnection(context.Background(), conn) {
		t.Fatal("registering an unreserved connection past capacity should be refused")
	}
}

// TestRecentEventsWithoutBusReturnsEmpty covers the recent-events endpoint when no
// in-memory event bus is wired: it must return a well-formed empty event list
// rather than erroring.
func TestRecentEventsWithoutBusReturnsEmpty(t *testing.T) {
	s := newSmokeServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events/recent", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"events"`) {
		t.Fatalf("body missing events key: %s", rec.Body.String())
	}
}
