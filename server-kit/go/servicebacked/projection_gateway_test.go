//go:build servicebacked

package servicebacked

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// TestServiceBackedProjectionGatewayReadPath exercises the projection read-path
// (projectiongw) on top of a Postgres-backed hermes store: the materialized read
// model is rebuilt from Postgres, served as a bounded snapshot over HTTP, and a
// subsequent accepted apply is streamed as a live delta over a real gorilla
// WebSocket — the same seam the frontend projection source consumes.
func TestServiceBackedProjectionGatewayReadPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	orgID := uniqueName(env.prefix, "projgw-org")
	cleanupOrganization(t, ctx, state, orgID)

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket", "source"},
		MaxRecords:    64,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// Seed Postgres with two records and rebuild the read model from durable state.
	for i, rec := range []struct {
		id     string
		bucket int64
	}{{"tick-a", 1}, {"tick-b", 2}} {
		if _, upErr := state.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: orgID,
			RecordID:       rec.id,
			Data:           serviceRecordData(map[string]any{"bucket": rec.bucket, "source": "postgres"}),
		}); upErr != nil {
			t.Fatalf("postgres upsert %d failed: %v", i, upErr)
		}
	}
	if result, rbErr := store.Rebuild(ctx, "signals", state, hermes.Query{OrganizationID: orgID}); rbErr != nil || result.Applied != 2 {
		t.Fatalf("Rebuild() result=%+v err=%v, want Applied=2", result, rbErr)
	}

	gw, err := projectiongw.NewGateway(store, 16)
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	defer gw.Close()

	// Inject the authenticated organization the same way the security middleware
	// would, so the identity-scoped gateway resolves this tenant's projection.
	withOrg := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(security.ContextWithOrganizationID(r.Context(), orgID)))
		})
	}
	srv := httptest.NewServer(withOrg(gw.Handler(projectiongw.HandlerConfig{})))
	defer srv.Close()

	// --- Snapshot over HTTP: the rebuilt read model is served as proto. ---
	snapResp, err := http.Get(srv.URL + "/v1/projections/signals/ticks")
	if err != nil {
		t.Fatalf("snapshot GET error = %v", err)
	}
	defer func() { _ = snapResp.Body.Close() }()
	if snapResp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", snapResp.StatusCode)
	}
	snapshot := decodeSnapshot(t, snapResp)
	if got := snapshotRecordIDs(snapshot); !containsAll(got, "tick-a", "tick-b") {
		t.Fatalf("snapshot record ids = %v, want tick-a and tick-b", got)
	}

	// --- Live delta over WebSocket: a new accepted apply must stream out. ---
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/signals/ticks"
	conn, wsResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if wsResp != nil {
		defer func() { _ = wsResp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial error = %v", err)
	}
	defer conn.Close()
	time.Sleep(75 * time.Millisecond) // let the subscription register before applying

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:       "service-backed:tick-live",
		Version:        3,
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: orgID,
		RecordId:       "tick-live",
		CorrelationId:  "corr-projgw-live",
		Fields: []*foundationpb.FieldValue{
			projectionField("bucket", int64(3)),
			projectionField("source", "live"),
		},
	}}, "corr-projgw-live")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	if _, applyErr := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); applyErr != nil {
		t.Fatalf("ApplyEnvelopes() error = %v", applyErr)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read error = %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("delta frame type = %d, want binary", msgType)
	}
	decoded, err := events.FromBinary(data)
	if err != nil {
		t.Fatalf("delta frame decode error = %v", err)
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
		t.Fatalf("delta batch decode error = %v", err)
	}
	if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick-live" {
		t.Fatalf("delta mutations = %+v, want single tick-live", batch.GetMutations())
	}

	// The accepted apply must also be visible in a fresh snapshot, proving the
	// delta and the read model stayed consistent.
	reSnap, err := http.Get(srv.URL + "/v1/projections/signals/ticks")
	if err != nil {
		t.Fatalf("re-snapshot GET error = %v", err)
	}
	defer func() { _ = reSnap.Body.Close() }()
	if ids := snapshotRecordIDs(decodeSnapshot(t, reSnap)); !containsAll(ids, "tick-a", "tick-b", "tick-live") {
		t.Fatalf("re-snapshot record ids = %v, want all three", ids)
	}
}

func decodeSnapshot(t *testing.T, resp *http.Response) *foundationpb.ProjectionSnapshot {
	t.Helper()
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if readErr != nil {
			break
		}
	}
	var snapshot foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("snapshot decode error = %v", err)
	}
	return &snapshot
}

func snapshotRecordIDs(snapshot *foundationpb.ProjectionSnapshot) []string {
	ids := make([]string, 0, len(snapshot.GetBatch().GetMutations()))
	for _, m := range snapshot.GetBatch().GetMutations() {
		ids = append(ids, m.GetRecordId())
	}
	return ids
}

func containsAll(haystack []string, wanted ...string) bool {
	for _, w := range wanted {
		found := false
		for _, h := range haystack {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
