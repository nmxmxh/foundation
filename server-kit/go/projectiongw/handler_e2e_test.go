package projectiongw

import (
	"context"
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
