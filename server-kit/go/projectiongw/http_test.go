package projectiongw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// TestSecurityTenantFunc proves projection access is identity-scoped: the tenant
// is the authenticated organization from request context, and a request without
// one is rejected rather than defaulted.
func TestSecurityTenantFunc(t *testing.T) {
	t.Run("authenticated organization resolves the tenant", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/projections/signals/ticks", nil)
		req = req.WithContext(security.ContextWithOrganizationID(req.Context(), "org_1"))
		tenant, err := SecurityTenantFunc(req)
		if err != nil || tenant != "org_1" {
			t.Fatalf("SecurityTenantFunc() = %q, %v; want org_1, nil", tenant, err)
		}
	})

	t.Run("no identity is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/projections/signals/ticks", nil)
		if _, err := SecurityTenantFunc(req); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("SecurityTenantFunc() err = %v; want ErrUnauthenticated", err)
		}
	})
}


func TestScopeAllowlistAllows(t *testing.T) {
	var unrestricted ScopeAllowlist
	if !unrestricted.Allows("signals", "ticks") {
		t.Fatal("nil allowlist must allow every scope")
	}
	failClosed := ScopeAllowlist{}
	if failClosed.Allows("signals", "ticks") {
		t.Fatal("empty non-nil allowlist must allow nothing")
	}
	published := ScopeAllowlist{"signals": {"ticks"}}
	if !published.Allows("signals", "ticks") || published.Allows("signals", "quotes") || published.Allows("menu", "ticks") {
		t.Fatal("allowlist must match exactly the published scopes")
	}
}

func TestPublicTenantFunc(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/discover/signals/ticks", nil)
	tenant, err := PublicTenantFunc(" org_public ")(req)
	if err != nil || tenant != "org_public" {
		t.Fatalf("PublicTenantFunc() = %q, %v; want org_public, nil", tenant, err)
	}
	if _, err := PublicTenantFunc("  ")(req); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("empty public org must disable the mount, got err=%v", err)
	}
}

// TestSnapshotHandlerEnforcesAllowlist proves an allowlisted mount serves
// exactly the published scopes: the published one snapshots normally, an
// unpublished one is 403, even though both exist for the tenant.
func TestSnapshotHandlerEnforcesAllowlist(t *testing.T) {
	gw := newTestGateway(t)
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	handler := gw.Handler(HandlerConfig{
		Tenant:    PublicTenantFunc("org_1"),
		Allowlist: ScopeAllowlist{"signals": {"ticks"}},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("published scope status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/signals/quotes", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unpublished scope status = %d, want 403", rec.Code)
	}
}

// TestMultiplexHandlerEnforcesAllowlist proves the allowlist gates subscribe
// control frames: an unpublished scope is answered with a scoped error control
// frame and never reaches the hub, while a published one in the same command
// subscribes normally and receives deltas.
func TestMultiplexHandlerEnforcesAllowlist(t *testing.T) {
	gw := newTestGateway(t)
	srv := httptest.NewServer(gw.Handler(HandlerConfig{
		Tenant:    PublicTenantFunc("org_1"),
		Allowlist: ScopeAllowlist{"signals": {"ticks"}},
	}))
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

	// The forbidden scope answers with a scoped control error…
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read err=%v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("frame type = %d, want text control frame", msgType)
	}
	var control ControlFrame
	if err := json.Unmarshal(payload, &control); err != nil {
		t.Fatalf("control decode err=%v", err)
	}
	if control.Type != ControlError || control.Collection != "quotes" {
		t.Fatalf("control frame = %+v, want scoped error for quotes", control)
	}

	// …and never reaches the hub, while the published scope subscribes.
	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount("org_1:signals:ticks") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("published scope not subscribed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gw.Hub().SubscriberCount("org_1:signals:quotes") != 0 {
		t.Fatal("forbidden scope must not reach the hub")
	}

	// The published scope streams deltas as usual.
	if _, err := gw.ApplyEnvelopes(context.Background(), "signals", mustEnvelope(t, tickMutation("tick_2", 2, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("delta read err=%v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("delta frame type = %d, want binary", msgType)
	}
}
