package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// An application registers no handler for connection control, because
// connection control is not an application concern.
//
// This is the case the existing subscribe tests do not cover: they register
// no-op handlers for both control events, so dispatch succeeded and execution
// fell through to the branch that records the subscription. Every application
// that was not told to do the same got handler_not_found, the recording branch
// was never reached, and the push lane was silently dead — no subscription, so
// no forwarded event, ever.
func TestWSSubscribeWithoutApplicationHandlers(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{})
	conn := dialWS(t, srv, "deviceId=dev_nohandlers")
	_ = readEnv(t, conn) // ack

	// A connection is created holding two default subscriptions, so the
	// assertions below are relative to that baseline rather than to zero.
	baseline := wsSubscriptionCount(s, "dev_nohandlers")

	sendEnv(t, conn, requested(wsEventSubscribe,
		extension.Object{"pattern": extension.String("orders:*")}))

	response := readEnv(t, conn)
	if response.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q, want :success — connection control must "+
			"never require an application handler", response.EventType)
	}
	if got := wsSubscriptionCount(s, "dev_nohandlers"); got != baseline+1 {
		t.Fatalf("subscriptions = %d, want %d: the pattern was acknowledged but not recorded",
			got, baseline+1)
	}

	sendEnv(t, conn, requested(wsEventUnsubscribe,
		extension.Object{"pattern": extension.String("orders:*")}))
	if response = readEnv(t, conn); response.EventType != "system:websocket_unsubscribe:v1:success" {
		t.Fatalf("unsubscribe response = %q, want :success", response.EventType)
	}
	if got := wsSubscriptionCount(s, "dev_nohandlers"); got != baseline {
		t.Fatalf("subscriptions after unsubscribe = %d, want the %d it started with", got, baseline)
	}
}

// The push lane, end to end, with no application handlers registered: subscribe
// and then receive a forwarded bus event.
func TestWSForwardingWorksWithoutApplicationHandlers(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{})
	conn := dialWS(t, srv, "deviceId=dev_forward")
	_ = readEnv(t, conn) // ack

	sendEnv(t, conn, requested(wsEventSubscribe,
		extension.Object{"pattern": extension.String("billing:*")}))
	if response := readEnv(t, conn); response.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q", response.EventType)
	}

	s.forwardEventToConnections(context.Background(), events.Envelope{
		EventType:     "billing:invoice:v1:created",
		Payload:       extension.Object{"amount": extension.Int(100)},
		Metadata:      userMetadata("", "dev_forward"),
		CorrelationID: "corr_nohandlers",
		Timestamp:     time.Now().UTC(),
		SchemaVersion: events.EnvelopeSchemaVersion,
	})

	if forwarded := readEnv(t, conn); forwarded.EventType != "billing:invoice:v1:created" {
		t.Fatalf("forwarded event = %q, want the subscribed one", forwarded.EventType)
	}
}

func TestControlEventsAreRecognised(t *testing.T) {
	for _, eventType := range []string{wsEventSubscribe, wsEventUnsubscribe} {
		if !isWSControlEvent(eventType) {
			t.Fatalf("%s manages the connection and must not be dispatched", eventType)
		}
	}
	for _, eventType := range []string{
		"orders:create:v1:requested",
		"identity:authenticate_connection:v1:requested",
		"system:websocket_subscribe:v1:success",
		"",
	} {
		if isWSControlEvent(eventType) {
			t.Fatalf("%q is not connection control and must reach dispatch", eventType)
		}
	}
}

// A connection-auth command still works and still wins, because a token
// refresh on a live socket goes through it.
func TestConnectionAuthCommandOverridesUpgradeClaims(t *testing.T) {
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"user_id": "user_42", "organization_id": "org_9", "role_id": "admin"}, nil
		},
	})
	conn := dialWS(t, srv, "deviceId=dev_override")
	_ = readEnv(t, conn) // ack

	if wsConnectionAuthenticated(s, "dev_override") {
		t.Fatal("a socket with no verified claims starts as a guest")
	}

	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	if response := readEnv(t, conn); response.EventType != "identity:authenticate_connection:v1:success" {
		t.Fatalf("auth response = %q", response.EventType)
	}
	if !wsConnectionAuthenticated(s, "dev_override") {
		t.Fatal("the connection-auth command must still upgrade the socket")
	}
}

// The disconnect hook exists so state tied to connection lifetime — presence in
// a room, a held lock, a live cursor — can be released when the socket ends,
// rather than expiring on a timer that makes every reader a little wrong.
func TestDisconnectHookIsCalledForAnAuthenticatedSocket(t *testing.T) {
	closed := make(chan ConnectionClosed, 1)
	srv, s := newWSTestServer(t, map[string]wsHandler{
		"identity:authenticate_connection:v1:requested": func(_ context.Context, _ extension.Object) (any, error) {
			return map[string]any{"user_id": "user_77", "organization_id": "org_3"}, nil
		},
	})
	s.OnConnectionClosed(func(_ context.Context, event ConnectionClosed) {
		closed <- event
	})

	conn := dialWS(t, srv, "deviceId=dev_close")
	_ = readEnv(t, conn) // ack
	sendEnv(t, conn, requested("identity:authenticate_connection:v1:requested", extension.Object{}))
	_ = readEnv(t, conn) // auth success

	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case event := <-closed:
		if event.UserID != "user_77" {
			t.Fatalf("closed.UserID = %q, want the authenticated account", event.UserID)
		}
		if event.DeviceID != "dev_close" {
			t.Fatalf("closed.DeviceID = %q", event.DeviceID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the disconnect hook was never called")
	}
}

// An anonymous socket holds no application state, so there is nothing to
// release and the hook is not called for one.
func TestDisconnectHookIsSilentForAGuest(t *testing.T) {
	called := make(chan ConnectionClosed, 1)
	srv, s := newWSTestServer(t, map[string]wsHandler{})
	s.OnConnectionClosed(func(_ context.Context, event ConnectionClosed) {
		called <- event
	})

	conn := dialWS(t, srv, "deviceId=dev_guest")
	_ = readEnv(t, conn) // ack
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case event := <-called:
		t.Fatalf("hook called for a guest connection: %+v", event)
	case <-time.After(time.Second):
	}
}

func TestDisconnectBudgetIsBounded(t *testing.T) {
	if WSDisconnectBudget <= 0 {
		t.Fatal("disconnect cleanup runs off the request context and must be bounded")
	}
}
