package discovergw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
	"google.golang.org/protobuf/proto"
)

const publicOrg = "org_public"

func newPublicGateway(t *testing.T) *projectiongw.Gateway {
	t.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:       "discover",
		Domain:     "discover",
		Collection: "chefs",
		MaxRecords: 16,
		MaxBytes:   1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := projectiongw.NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	return gw
}

func chefMutation(recordID string, version uint64, open string) *foundationpb.RecordMutation {
	return &foundationpb.RecordMutation{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		Version:        version,
		Domain:         "discover",
		Collection:     "chefs",
		OrganizationId: publicOrg,
		RecordId:       recordID,
		Fields: []*foundationpb.FieldValue{
			{Name: "open", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: open}}},
		},
	}
}

func applyChef(t *testing.T, gw *projectiongw.Gateway, correlation string, mutation *foundationpb.RecordMutation) {
	t.Helper()
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation}, correlation)
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(context.Background(), "discover", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
}

func TestNewHandlerFailsClosed(t *testing.T) {
	gw := newPublicGateway(t)
	scopes := projectiongw.ScopeAllowlist{"discover": {"chefs"}}
	if _, err := NewHandler(nil, Config{PublicOrganizationID: publicOrg, Scopes: scopes}); !errors.Is(err, ErrNilGateway) {
		t.Fatalf("nil gateway err = %v, want ErrNilGateway", err)
	}
	if _, err := NewHandler(gw, Config{Scopes: scopes}); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("no org err = %v, want ErrNoOrganization", err)
	}
	if _, err := NewHandler(gw, Config{PublicOrganizationID: publicOrg}); !errors.Is(err, ErrEmptyAllowlist) {
		t.Fatalf("empty allowlist err = %v, want ErrEmptyAllowlist", err)
	}
}

// TestAnonymousSnapshotAndLiveDelta is the whole point of the package end to
// end: a request with NO identity at all snapshots a published public scope,
// subscribes over the multiplexed WebSocket, and receives a live delta — while
// an unpublished scope on the same mount stays 403.
func TestAnonymousSnapshotAndLiveDelta(t *testing.T) {
	gw := newPublicGateway(t)
	applyChef(t, gw, "corr-snapshot", chefMutation("chef_1", 1, "true"))

	handler, err := NewHandler(gw, Config{
		PublicOrganizationID: publicOrg,
		Scopes:               projectiongw.ScopeAllowlist{"discover": {"chefs"}},
	})
	if err != nil {
		t.Fatalf("NewHandler() err=%v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Anonymous snapshot of the published scope.
	resp, err := http.Get(srv.URL + "/v1/discover/discover/chefs")
	if err != nil {
		t.Fatalf("snapshot err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", resp.StatusCode)
	}
	var snap foundationpb.ProjectionSnapshot
	raw := make([]byte, 1<<16)
	n, _ := resp.Body.Read(raw)
	if err := proto.Unmarshal(raw[:n], &snap); err != nil {
		t.Fatalf("snapshot decode err=%v", err)
	}
	muts := snap.GetBatch().GetMutations()
	if len(muts) != 1 || muts[0].GetRecordId() != "chef_1" || muts[0].GetOrganizationId() != publicOrg {
		t.Fatalf("snapshot mutations = %+v", muts)
	}

	// An unpublished scope on the same mount is forbidden.
	forbidden, err := http.Get(srv.URL + "/v1/discover/profile/profiles")
	if err != nil {
		t.Fatalf("forbidden read err=%v", err)
	}
	_ = forbidden.Body.Close()
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("unpublished scope status = %d, want 403", forbidden.StatusCode)
	}

	// Anonymous multiplexed live stream.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/discover/"
	conn, wsResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if wsResp != nil {
		defer func() { _ = wsResp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial err=%v", err)
	}
	defer conn.Close()
	subscribe := `{"type":"subscribe","scopes":[{"domain":"discover","collection":"chefs"}]}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscribe)); err != nil {
		t.Fatalf("subscribe write err=%v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount(publicOrg+":discover:chefs") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("anonymous subscription not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	applyChef(t, gw, "corr-delta", chefMutation("chef_1", 2, "false"))

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("delta read err=%v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("delta frame type = %d, want binary", msgType)
	}
	decoded, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("delta decode err=%v", err)
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
		t.Fatalf("batch decode err=%v", err)
	}
	if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetVersion() != 2 {
		t.Fatalf("delta = %+v", batch.GetMutations())
	}
}
