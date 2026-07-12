//go:build servicebacked

package servicebacked

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpserver"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"

	// TestServiceBackedProjectionGatewayReadPath exercises the projection read-path
	// (projectiongw) on top of a Postgres-backed hermes store: the materialized read
	// model is rebuilt from Postgres, served as a bounded snapshot over HTTP, and a
	// subsequent accepted apply is streamed as a live delta over a real gorilla
	// WebSocket — the same seam the frontend projection source consumes.
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

func TestServiceBackedProjectionGatewayResyncAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	orgID := uniqueName(env.prefix, "projgw-residual-org")
	cleanupOrganization(t, ctx, state, orgID)

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"bucket"}, MaxRecords: 256, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, upErr := state.UpsertRecord(ctx, database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: orgID, RecordID: "tick-a",
		Data: serviceRecordData(map[string]any{"bucket": int64(1)}),
	}); upErr != nil {
		t.Fatalf("seed upsert: %v", upErr)
	}
	if _, rbErr := store.Rebuild(ctx, "signals", state, hermes.Query{OrganizationID: orgID}); rbErr != nil {
		t.Fatalf("Rebuild(): %v", rbErr)
	}

	gw, err := projectiongw.NewGateway(store, 1)
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	defer gw.Close()
	withOrg := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(security.ContextWithOrganizationID(r.Context(), orgID)))
		})
	}
	srv := httptest.NewServer(withOrg(gw.Handler(projectiongw.HandlerConfig{})))
	defer srv.Close()

	key := projectiongw.ScopeKey(&foundationpb.ProjectionScope{TenantId: orgID, Domain: "signals", Collection: "ticks"})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/projections/signals/ticks"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial error = %v", err)
	}
	waitForSubscribers(t, gw, key, 1)
	for i := range 20000 {
		gw.Hub().Broadcast(key, projectiongw.Frame{Envelope: []byte("delta"), Watermark: fmtResidual(i), Epoch: 1})
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	sawResync := false
	for range 80 {
		mt, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		if mt == websocket.TextMessage && strings.Contains(string(data), projectiongw.ControlResync) {
			sawResync = true
			break
		}
	}
	if !sawResync {
		t.Fatal("slow consumer did not receive a resync control frame")
	}

	_ = conn.Close()
	for i := range 20000 {
		gw.Hub().Broadcast(key, projectiongw.Frame{Envelope: []byte("delta"), Watermark: fmtResidual(i), Epoch: 1})
	}
	waitForSubscribers(t, gw, key, 0)
	if n := gw.Hub().SubscriberCount(key); n != 0 {
		t.Fatalf("subscriber count after disconnect = %d, want 0", n)
	}
}

func TestServiceBackedWSEventForwardingViaRedis(t *testing.T) {
	env := requireServiceEnv(t)
	client := openRedis(t, env)
	bus := events.NewRedisBus(client, uniqueName(env.prefix, "ws-fwd"), 32, nil)
	defer bus.Close()

	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	gh := graceful.NewHandler(
		graceful.WithLogger(log),
		graceful.WithService("ws-redis-fwd"),
		graceful.WithEventEmitter(graceful.NewInMemoryEventEmitter(bus)),
	)
	reg := registry.New(nil, gh, log)
	if regErr := reg.Register("system:websocket_subscribe:v1:requested",
		func(context.Context, extension.Object) (any, error) { return map[string]any{}, nil }); regErr != nil {
		t.Fatalf("register: %v", regErr)
	}
	s := httpserver.New(&httpserver.Config{Port: 0, AllowedOrigins: []string{"http://localhost"}}, reg, gh)
	s.AddUnauthenticatedWSEvent("system:websocket_subscribe:v1:requested")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?deviceId=dev_redis", nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial error = %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readResidualEnvelope(t, conn)

	subscribe := events.Envelope{
		EventType: "system:websocket_subscribe:v1:requested",
		Payload:   extension.Object{"pattern": extension.String("demo:*")},
		Metadata:  extension.Object{}, CorrelationID: "corr_redis_sub",
		Timestamp: time.Now().UTC(), SchemaVersion: events.EnvelopeSchemaVersion,
	}
	raw, _ := subscribe.ToJSON()
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	if sub := readResidualEnvelope(t, conn); sub.EventType != "system:websocket_subscribe:v1:success" {
		t.Fatalf("subscribe response = %q", sub.EventType)
	}

	if pubErr := bus.Publish(context.Background(), events.Envelope{
		EventType: "demo:thing:v1:success",
		Payload:   extension.Object{"hello": extension.String("redis")},
		Metadata:  extension.Object{}, CorrelationID: "corr_redis_fwd",
		Timestamp: time.Now().UTC(), SchemaVersion: events.EnvelopeSchemaVersion,
	}); pubErr != nil {
		t.Fatalf("publish: %v", pubErr)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		env := readResidualEnvelope(t, conn)
		if env.EventType == "demo:thing:v1:success" {
			return
		}
	}
	t.Fatal("event published to Redis was not forwarded to the websocket")
}

func waitForSubscribers(t *testing.T, gw *projectiongw.Gateway, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for gw.Hub().SubscriberCount(key) != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func readResidualEnvelope(t *testing.T, conn *websocket.Conn) events.Envelope {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	env, err := events.FromJSON(data)
	if err != nil {
		t.Fatalf("decode envelope: %v (raw=%s)", err, data)
	}
	return env
}

func fmtResidual(i int) string { return "wm-" + strconv.Itoa(i) }
