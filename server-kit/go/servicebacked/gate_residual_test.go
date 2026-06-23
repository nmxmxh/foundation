//go:build servicebacked

package servicebacked

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
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
)

// TestServiceBackedProjectionGatewayResyncAndDisconnect validates the projection
// gateway's slow-consumer and client-disconnect handling against a Postgres-backed
// read model — the writer-side paths the unit floor leaves uncovered (they need a
// real socket, not httptest determinism). It also exercises, under real timing,
// the concurrent broadcast-while-cancelling interleaving that previously panicked
// the hub with "send on closed channel" (now fixed): the test must complete
// without a panic and with the subscription cleaned up.
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

	gw, err := projectiongw.NewGateway(store, 1) // queue size 1 → easy to overflow
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

	// --- Slow consumer: a subscriber that does not drain gets a resync control. ---
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

	// --- Disconnect during broadcast: close the client, keep broadcasting. The
	// writer's send to the dead socket fails and the subscription is torn down;
	// the concurrent Cancel + Broadcast must not panic. ---
	_ = conn.Close()
	for i := range 20000 {
		gw.Hub().Broadcast(key, projectiongw.Frame{Envelope: []byte("delta"), Watermark: fmtResidual(i), Epoch: 1})
	}
	waitForSubscribers(t, gw, key, 0)
	if n := gw.Hub().SubscriberCount(key); n != 0 {
		t.Fatalf("subscriber count after disconnect = %d, want 0", n)
	}
}

// TestServiceBackedWSEventForwardingViaRedis validates the httpserver WebSocket
// forwarding lane against a real Redis-backed event bus: a guest subscribes, an
// event published to Redis is delivered to the bus subscription and fanned out to
// the socket. This exercises ensureEventSubscription + forwardEventToConnections
// over real infrastructure rather than the in-memory bus the unit tests use.
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
	readResidualEnvelope(t, conn) // connection ack

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

	// Publish through the real Redis bus; the server's bus subscription must
	// forward it to the socket.
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
			return // forwarded over real Redis as expected
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
