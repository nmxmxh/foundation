package projectiongw

import (
	"context"
	"errors"
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

// TestNewGatewayForProjectedStore exercises the constructor the generated server
// uses: it resolves scopes against the projected store's own partition naming, so
// a snapshot finds the records the projected store materialized.
func TestNewGatewayForProjectedStore(t *testing.T) {
	projected, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		IndexedFields: []string{"symbol"}, MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	ctx := t.Context()
	if _, err := projected.UpsertRecord(ctx, database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
		Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
	}); err != nil {
		t.Fatalf("UpsertRecord() err=%v", err)
	}

	gw, err := NewGatewayForProjectedStore(projected, 0)
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() err=%v", err)
	}
	defer gw.Close()
	if gw.Hub() == nil {
		t.Fatal("Hub() should not be nil")
	}

	snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	if muts := snap.GetBatch().GetMutations(); len(muts) != 1 || muts[0].GetRecordId() != "tick_1" {
		t.Fatalf("snapshot = %+v", muts)
	}

	if _, err := NewGatewayForProjectedStore(nil, 0); !errors.Is(err, ErrNilStore) {
		t.Fatalf("nil projected store err = %v, want ErrNilStore", err)
	}
}

// TestWithHubSharesHub proves a Hub can be shared across gateways via the option.
func TestWithHubSharesHub(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", MaxRecords: 4, MaxBytes: 1 << 20})
	hub := NewHub(8)
	gw, err := NewGateway(store, 0, WithHub(hub), WithResolver(nil))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()
	if gw.Hub() != hub {
		t.Fatal("WithHub did not install the shared hub")
	}
}

func TestScopeKeyAndResolverEdges(t *testing.T) {
	if got := ScopeKey(nil); got != "::" {
		t.Fatalf("ScopeKey(nil) = %q, want ::", got)
	}
	// DomainResolver rejects an incomplete scope.
	if _, _, err := DomainResolver(10)(&foundationpb.ProjectionScope{TenantId: "org_1"}); !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("DomainResolver invalid scope err = %v, want ErrScopeInvalid", err)
	}
}

func TestWriteErrorClasses(t *testing.T) {
	cases := map[error]int{
		ErrUnauthenticated:        401,
		ErrScopeInvalid:           400,
		errors.New("other thing"): 400,
	}
	for err, want := range cases {
		rec := httptest.NewRecorder()
		writeError(rec, err)
		if rec.Code != want {
			t.Fatalf("writeError(%v) = %d, want %d", err, rec.Code, want)
		}
	}
}

func TestHandlerConfigCustomTenantAndPrefix(t *testing.T) {
	// Custom prefix is honored when parsing the scope path.
	hc := HandlerConfig{PathPrefix: "/custom/proj/", Tenant: SecurityTenantFunc}
	if hc.prefix() != "/custom/proj/" {
		t.Fatalf("prefix() = %q", hc.prefix())
	}
	req := httptest.NewRequest("GET", "/custom/proj/signals/ticks", nil)
	if _, err := hc.scopeFromRequest(req); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("scopeFromRequest without identity err = %v, want ErrUnauthenticated", err)
	}
}

func TestSnapshotHandlerRejectsBadPathAndMethod(t *testing.T) {
	gw := newTestGateway(t)
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))

	// Missing collection segment → 400.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projections/signals", nil))
	if rec.Code != 400 {
		t.Fatalf("bad path = %d, want 400", rec.Code)
	}
	// POST to the snapshot path → 405.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projections/signals/ticks", nil))
	if rec.Code != 405 {
		t.Fatalf("POST = %d, want 405", rec.Code)
	}
}

func TestSnapshotLimitAndCursorPlumbing(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20})
	gw, err := NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()
	ctx := t.Context()
	for i := 1; i <= 3; i++ {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert, SourceID: fmtTick(i), Version: uint64(i),
			Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: fmtTick(i),
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}}},
		}); err != nil {
			t.Fatalf("apply err=%v", err)
		}
	}
	// limit=2 → page of 2, HasMore + NextCursor set.
	page1, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Limit: 2})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	if len(page1.GetBatch().GetMutations()) != 2 || !page1.GetHasMore() || page1.GetNextCursor() == "" {
		t.Fatalf("page1 = %d muts, hasMore=%v cursor=%q", len(page1.GetBatch().GetMutations()), page1.GetHasMore(), page1.GetNextCursor())
	}
	// next page via cursor.
	page2, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Limit: 2, Cursor: page1.GetNextCursor()})
	if err != nil {
		t.Fatalf("Snapshot(cursor) err=%v", err)
	}
	if len(page2.GetBatch().GetMutations()) != 1 || page2.GetHasMore() {
		t.Fatalf("page2 = %d muts, hasMore=%v", len(page2.GetBatch().GetMutations()), page2.GetHasMore())
	}
}

func TestSnapshotHandlerHonorsQueryParams(t *testing.T) {
	gw := newTestGateway(t)
	env, _ := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "c")
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", []events.Envelope{env}); err != nil {
		t.Fatalf("apply err=%v", err)
	}
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projections/signals/ticks?since=0&limit=10", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Projection-Epoch") == "" {
		t.Fatal("missing epoch header")
	}
}

func TestSnapshotRejectsInvalidScope(t *testing.T) {
	gw := newTestGateway(t)
	// Missing collection → resolver returns ErrScopeInvalid → Snapshot errors.
	_, err := gw.Snapshot(t.Context(), &foundationpb.ProjectionSnapshotRequest{
		Scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"},
	})
	if !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("Snapshot invalid scope err = %v, want ErrScopeInvalid", err)
	}
}

func TestSubscribeHandlerRejectsBadPathBeforeUpgrade(t *testing.T) {
	gw := newTestGateway(t)
	// A WS-upgrade request to a malformed path is rejected (400) before upgrade.
	handler := orgContextHandler("org_1", gw.SubscribeHandler(HandlerConfig{}))
	req := httptest.NewRequest("GET", "/v1/projections/signals", nil) // missing collection
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad WS path = %d, want 400", rec.Code)
	}
}

func TestSubscriptionDroppedZeroValue(t *testing.T) {
	if got := (&Subscription{}).Dropped(); got != 0 {
		t.Fatalf("zero-value Dropped() = %d, want 0", got)
	}
	// A zero-value Subscription has no drop channel; Drops() is nil (blocks
	// forever in a select), the intended no-op.
	if (&Subscription{}).Drops() != nil {
		t.Fatal("zero-value Drops() should be nil")
	}
	// Broadcast to a key with no subscribers is a no-op (no panic, no target).
	NewHub(4).Broadcast("absent:key:scope", Frame{Watermark: "1"})
}

// TestHubBroadcastSignalsDrop is the deterministic unit covering the drop path:
// once a subscriber's bounded queue is full, further frames are shed (Dropped
// increments) and a coalesced signal lands on Drops() so the writer can emit a
// resync without waiting for a deliverable frame.
func TestHubBroadcastSignalsDrop(t *testing.T) {
	hub := NewHub(1) // queue size 1
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "d", Collection: "c"}
	sub := hub.Subscribe(scope)
	defer sub.Cancel()
	key := ScopeKey(scope)

	// First frame fills the queue; the next two overflow → 2 drops.
	for i := range 3 {
		hub.Broadcast(key, Frame{Watermark: fmtTick(i)})
	}
	if got := sub.Dropped(); got < 2 {
		t.Fatalf("Dropped() = %d, want >= 2", got)
	}
	select {
	case <-sub.Drops():
		// signalled as expected
	default:
		t.Fatal("expected a coalesced drop signal on Drops()")
	}
}

// TestSubscribeHandlerSendsResyncOnDrop forces the hub to shed frames for a
// non-draining consumer and asserts the gateway emits a resync control (text)
// frame so the client knows to reconcile. Bounded read deadline, no fixed sleep.
func TestSubscribeHandlerSendsResyncOnDrop(t *testing.T) {
	store, _ := hermes.NewStore(hermes.ProjectionSpec{Name: "signals", Domain: "signals", Collection: "ticks", IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20})
	gw, err := NewGateway(store, 1) // queue size 1 → easy to overflow
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
	defer conn.Close()

	// Wait for the subscription to register, then flood frames directly through
	// the hub. Broadcasting is a channel send (~ns); the server writer does one
	// socket syscall per frame (~µs), so a tight burst deterministically
	// overflows the size-1 queue → drops → resync. (Driving via store.Apply is
	// too slow to outrun the writer, which is why the prior flood was flaky.)
	key := ScopeKey(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"})
	deadline := time.Now().Add(time.Second)
	for gw.Hub().SubscriberCount(key) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := range 20000 {
		gw.Hub().Broadcast(key, Frame{Envelope: []byte("delta"), Watermark: fmtTick(i), Epoch: 1})
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	sawResync := false
	for range 60 {
		msgType, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		if msgType == websocket.TextMessage && strings.Contains(string(data), ControlResync) {
			sawResync = true
			break
		}
	}
	if !sawResync {
		t.Fatal("expected a resync control frame after dropped deltas")
	}
}

// TestGatewayLazyWarmsColdScopeOnRead is the config-free end state: a scope
// whose data lives only in the durable base (raw SQL seed — never warmed,
// never written through the projected store) resolves on the FIRST snapshot
// request. The gateway catches ErrProjectionNotFound, warms through
// ProjectedRuntimeStore.WarmScope (which self-backfills an empty mirror via
// ScopeBackfill), and retries — so eager warm configuration is no longer
// required for correctness, and "projection not found" stops being the
// steady-state answer for seeded data.
func TestGatewayLazyWarmsColdScopeOnRead(t *testing.T) {
	ctx := t.Context()

	// Leg 1: rows exist only in the base mirror (out-of-band seed).
	base := database.NewMemoryDB()
	if _, err := base.UpsertRecord(ctx, database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_seed",
		Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
	}); err != nil {
		t.Fatalf("seed UpsertRecord() err=%v", err)
	}
	projected, err := hermes.WrapRuntimeStore(base, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	gw, err := NewGatewayForProjectedStore(projected, 0)
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() err=%v", err)
	}
	defer gw.Close()

	snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("cold-scope Snapshot() err=%v, want lazy warm to resolve it", err)
	}
	if muts := snap.GetBatch().GetMutations(); len(muts) != 1 || muts[0].GetRecordId() != "tick_seed" {
		t.Fatalf("lazy-warmed snapshot = %+v, want the seeded record", muts)
	}

	// Leg 2: empty mirror + ScopeBackfill — the warm pulls rows from the app's
	// authoritative tables on the same first read.
	backfilled, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
		ScopeBackfill: func(_ context.Context, domain, collection, organizationID string, visit database.RecordVisitor) error {
			return visit(database.DomainRecord{
				Domain: domain, Collection: collection, OrganizationID: organizationID, RecordID: "tick_backfill",
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("BKF")}},
			})
		},
	})
	if err != nil {
		t.Fatalf("backfill WrapRuntimeStore() err=%v", err)
	}
	gw2, err := NewGatewayForProjectedStore(backfilled, 0)
	if err != nil {
		t.Fatalf("backfill gateway err=%v", err)
	}
	defer gw2.Close()
	snap, err = gw2.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("backfill Snapshot() err=%v", err)
	}
	if muts := snap.GetBatch().GetMutations(); len(muts) != 1 || muts[0].GetRecordId() != "tick_backfill" {
		t.Fatalf("backfilled snapshot = %+v, want the backfilled record", muts)
	}

	// Leg 3: a scope with genuinely no data anywhere serves EMPTY, not an
	// error — "projection not found" is no longer the steady-state answer.
	empty, err := hermes.WrapRuntimeStore(database.NewMemoryDB(), hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		t.Fatalf("empty WrapRuntimeStore() err=%v", err)
	}
	gw3, err := NewGatewayForProjectedStore(empty, 0)
	if err != nil {
		t.Fatalf("empty gateway err=%v", err)
	}
	defer gw3.Close()
	snap, err = gw3.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("empty-scope Snapshot() err=%v, want empty snapshot", err)
	}
	if muts := snap.GetBatch().GetMutations(); len(muts) != 0 {
		t.Fatalf("empty-scope snapshot = %+v, want no mutations", muts)
	}

	// Invalid scopes still fail fast — lazy warm never runs for them.
	if _, err := gw3.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: &foundationpb.ProjectionScope{}}); err == nil {
		t.Fatal("invalid scope error = nil")
	}
}
