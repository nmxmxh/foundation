package projectiongw

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
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// TestProjectedStoreResolverRejectsInvalidScope covers the resolver guard wired by
// NewGatewayForProjectedStore: a snapshot whose scope is missing its collection is
// rejected with ErrScopeInvalid before any read, proving the projected-store
// gateway validates scope the same way the default resolver does.
func TestProjectedStoreResolverRejectsInvalidScope(t *testing.T) {
	projected, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		IndexedFields: []string{"symbol"}, MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	gw, err := NewGatewayForProjectedStore(projected, 0)
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() err=%v", err)
	}
	defer gw.Close()

	_, err = gw.Snapshot(t.Context(), &foundationpb.ProjectionSnapshotRequest{
		Scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"}, // no collection
	})
	if !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("invalid scope err = %v, want ErrScopeInvalid", err)
	}
}

// TestSnapshotHonorsCancelledContext covers the read-error propagation in Snapshot
// (TE-17 cancellation): a cancelled context is surfaced from the bounded
// SnapshotPage read rather than swallowed.
func TestSnapshotHonorsCancelledContext(t *testing.T) {
	gw := newTestGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err == nil {
		t.Fatal("cancelled context should surface an error from Snapshot")
	}
}

// TestSnapshotHandlerSurfacesReadError covers the SnapshotHandler error branch:
// when the underlying snapshot read fails (here via a cancelled request context),
// the handler writes a domain error response rather than a partial/empty 200.
func TestSnapshotHandlerSurfacesReadError(t *testing.T) {
	gw := newTestGateway(t)
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil).WithContext(ctx)
	// Re-apply the org context on top of the cancelled context for scope auth.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a non-200 error from the cancelled read", rec.Code)
	}
}

// TestSubscribeHandlerFailedUpgradeReleasesSubscription covers the cleanup path
// when the WebSocket upgrade fails after a subscription was created (TE-17): the
// handler must cancel the subscription so it does not leak. A plain GET (no
// Upgrade header) reaches Subscribe, then fails the handshake. The oracle is that
// no subscriber remains registered for the scope.
func TestSubscribeHandlerFailedUpgradeReleasesSubscription(t *testing.T) {
	gw := newTestGateway(t)
	handler := orgContextHandler("org_1", gw.SubscribeHandler(HandlerConfig{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil))

	// The handshake could not complete (not a 101 switching-protocols).
	if rec.Code == http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, expected a non-upgrade failure", rec.Code)
	}
	// No subscription may be left behind on the scope.
	key := ScopeKey(scope())
	if n := gw.Hub().SubscriberCount(key); n != 0 {
		t.Fatalf("subscribers after failed upgrade = %d, want 0 (subscription leaked)", n)
	}
}

// TestSubscribeHandlerWriteFailureTearsDown is both a writer-failure cleanup test
// (TE-17) and a concurrency regression test (TE-20/TE-30): when the client socket
// is gone, the server's next delta write fails and the subscription is torn down
// rather than leaked. Critically, the teardown (Subscription.Cancel closing the
// frames channel) runs concurrently with the flood of Hub.Broadcast sends — the
// exact interleaving that previously panicked with "send on closed channel" when
// Broadcast sent outside the hub lock. Run under -race, this guards that fix.
func TestSubscribeHandlerWriteFailureTearsDown(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20})
	gw, err := NewGateway(store, 1)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()
	srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/projections/signals/ticks", nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("ws dial err=%v", err)
	}

	key := ScopeKey(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"})
	deadline := time.Now().Add(time.Second)
	for gw.Hub().SubscriberCount(key) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Client vanishes, then a flood forces the server writer to attempt delivery to
	// the dead socket. The write fails and the connection is torn down.
	_ = conn.Close()
	for i := range 20000 {
		gw.Hub().Broadcast(key, Frame{Envelope: []byte("delta"), Watermark: fmtTick(i), Epoch: 1})
	}

	teardown := time.Now().Add(2 * time.Second)
	for gw.Hub().SubscriberCount(key) > 0 && time.Now().Before(teardown) {
		time.Sleep(time.Millisecond)
	}
	if n := gw.Hub().SubscriberCount(key); n != 0 {
		t.Fatalf("subscriber count after client write failure = %d, want 0", n)
	}
}
