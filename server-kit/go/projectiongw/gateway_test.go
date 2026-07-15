package projectiongw

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    16,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	return gw
}

func tickMutation(recordID string, version uint64, symbol string) *foundationpb.RecordMutation {
	return &foundationpb.RecordMutation{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		Version:        version,
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: "org_1",
		RecordId:       recordID,
		Fields: []*foundationpb.FieldValue{
			{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: symbol}}},
		},
	}
}

func scope() *foundationpb.ProjectionScope {
	return &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
}

func TestGatewaySnapshotAfterApply(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	snapshot, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	muts := snapshot.GetBatch().GetMutations()
	if len(muts) != 1 || muts[0].GetRecordId() != "tick_1" {
		t.Fatalf("Snapshot mutations = %+v", muts)
	}
	if muts[0].GetOperation() != foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT {
		t.Fatalf("snapshot op = %v, want UPSERT", muts[0].GetOperation())
	}
	if snapshot.GetEpoch() == 0 {
		t.Fatalf("snapshot epoch should be non-zero after apply")
	}
}

func TestGatewayBroadcastsDeltaOnApply(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	select {
	case frame := <-sub.Frames:
		decoded, err := events.FromBinary(frame.Envelope)
		if err != nil {
			t.Fatalf("frame envelope decode err=%v", err)
		}
		var batch foundationpb.RecordMutationBatch
		if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
			t.Fatalf("batch unmarshal err=%v", err)
		}
		if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick_1" {
			t.Fatalf("delta mutations = %+v", batch.GetMutations())
		}
		if frame.Watermark != "1" {
			t.Fatalf("frame watermark = %q, want %q", frame.Watermark, "1")
		}
	case <-time.After(time.Second):
		t.Fatal("expected delta frame, got none")
	}
}

func TestGatewayDoesNotBroadcastDuplicates(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	mutation := tickMutation("tick_1", 1, "OVS")
	mutation.SourceId = "tick_1@1"
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}

	// First apply is accepted and broadcast.
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	select {
	case <-sub.Frames:
	case <-time.After(time.Second):
		t.Fatal("expected first delta")
	}

	// Re-applying the identical envelope is a duplicate; hermes is the source of
	// truth, so no delta must be broadcast.
	result, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope})
	if err != nil {
		t.Fatalf("ApplyEnvelopes() duplicate err=%v", err)
	}
	if result.Applied != 0 || result.Duplicates != 1 {
		t.Fatalf("expected duplicate apply, got applied=%d duplicates=%d", result.Applied, result.Duplicates)
	}
	select {
	case <-sub.Frames:
		t.Fatal("duplicate apply must not broadcast a delta")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGatewayBroadcastsInProcessApply(t *testing.T) {
	// The in-process projected runtime store writes via store.Apply (not
	// envelopes). The store observer must still surface a delta, so the live loop
	// works under a pure in-memory server with no Redis projector.
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	if _, err := store.Apply(t.Context(), "signals", hermes.Event{
		Operation: hermes.OperationUpsert,
		SourceID:  "tick_1@1",
		Version:   1,
		Record: database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
		},
	}); err != nil {
		t.Fatalf("store.Apply() err=%v", err)
	}

	select {
	case frame := <-sub.Frames:
		decoded, err := events.FromBinary(frame.Envelope)
		if err != nil {
			t.Fatalf("decode err=%v", err)
		}
		var batch foundationpb.RecordMutationBatch
		if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
			t.Fatalf("unmarshal err=%v", err)
		}
		if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick_1" {
			t.Fatalf("delta = %+v", batch.GetMutations())
		}
	case <-time.After(time.Second):
		t.Fatal("expected in-process apply to broadcast a delta")
	}
}

func TestGatewayScopeIsolation(t *testing.T) {
	gw := newTestGateway(t)

	// Subscriber on a different tenant must not receive org_1 deltas.
	other, err := gw.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_2", Domain: "signals", Collection: "ticks"})
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer other.Cancel()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	select {
	case <-other.Frames:
		t.Fatal("cross-tenant subscriber must not receive deltas")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGatewaySnapshotPaginates(t *testing.T) {
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithResolver(func(s *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		return "signals", hermes.QueryWithFilters(s.GetTenantId(), 10), nil
	}))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	ctx := t.Context()
	for i := 1; i <= 25; i++ {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert, SourceID: fmtTick(i), Version: uint64(i),
			Record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: fmtTick(i),
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
			},
		}); err != nil {
			t.Fatalf("Apply() err=%v", err)
		}
	}

	// Page through via the gateway cursor and assert full, non-overlapping coverage.
	seen := map[string]struct{}{}
	cursor := ""
	for pages := 0; ; pages++ {
		snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Cursor: cursor})
		if err != nil {
			t.Fatalf("Snapshot() err=%v", err)
		}
		for _, m := range snap.GetBatch().GetMutations() {
			if _, dup := seen[m.GetRecordId()]; dup {
				t.Fatalf("record %s on two pages", m.GetRecordId())
			}
			seen[m.GetRecordId()] = struct{}{}
		}
		if !snap.GetHasMore() {
			break
		}
		cursor = snap.GetNextCursor()
		if pages > 25 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 25 {
		t.Fatalf("paginated coverage = %d, want 25", len(seen))
	}
}

func TestGatewaySnapshotIsAlwaysBounded(t *testing.T) {
	// A client cannot trigger an unbounded scan: an over-large (or zero) limit is
	// clamped to the gateway's max, keeping the read O(limit) — BoundedWork.
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithResolver(func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		// Deliberately unbounded resolver (Limit 0) to prove the gateway clamps it.
		return "signals", hermes.QueryWithFilters(scope.GetTenantId(), 0), nil
	}))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	ctx := t.Context()
	for i := range 40 {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert, SourceID: fmtTick(i), Version: uint64(i + 1),
			Record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: fmtTick(i),
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
			},
		}); err != nil {
			t.Fatalf("Apply() err=%v", err)
		}
	}

	// Client requests a huge limit; the gateway clamps to DefaultSnapshotLimit.
	snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Limit: 1 << 20})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	if got := len(snap.GetBatch().GetMutations()); got > DefaultSnapshotLimit {
		t.Fatalf("snapshot returned %d mutations, exceeds bound %d", got, DefaultSnapshotLimit)
	}
}

func fmtTick(i int) string { return "tick_" + strconv.Itoa(i) }

func TestGatewayRejectsInvalidScope(t *testing.T) {
	gw := newTestGateway(t)
	if _, err := gw.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"}); err == nil {
		t.Fatal("expected ErrScopeInvalid for missing collection")
	}
}

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

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projections/signals", nil))
	if rec.Code != 400 {
		t.Fatalf("bad path = %d, want 400", rec.Code)
	}

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

	page1, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Limit: 2})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	if len(page1.GetBatch().GetMutations()) != 2 || !page1.GetHasMore() || page1.GetNextCursor() == "" {
		t.Fatalf("page1 = %d muts, hasMore=%v cursor=%q", len(page1.GetBatch().GetMutations()), page1.GetHasMore(), page1.GetNextCursor())
	}

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

	_, err := gw.Snapshot(t.Context(), &foundationpb.ProjectionSnapshotRequest{
		Scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"},
	})
	if !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("Snapshot invalid scope err = %v, want ErrScopeInvalid", err)
	}
}

func TestSubscribeHandlerRejectsBadPathBeforeUpgrade(t *testing.T) {
	gw := newTestGateway(t)

	handler := orgContextHandler("org_1", gw.SubscribeHandler(HandlerConfig{}))
	req := httptest.NewRequest("GET", "/v1/projections/signals", nil)
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

	if (&Subscription{}).Drops() != nil {
		t.Fatal("zero-value Drops() should be nil")
	}

	NewHub(4).Broadcast("absent:key:scope", Frame{Watermark: "1"})
}

func TestHubBroadcastSignalsDrop(t *testing.T) {
	hub := NewHub(1)
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "d", Collection: "c"}
	sub := hub.Subscribe(scope)
	defer sub.Cancel()
	key := ScopeKey(scope)

	for i := range 3 {
		hub.Broadcast(key, Frame{Watermark: fmtTick(i)})
	}
	if got := sub.Dropped(); got < 2 {
		t.Fatalf("Dropped() = %d, want >= 2", got)
	}
	select {
	case <-sub.Drops():

	default:
		t.Fatal("expected a coalesced drop signal on Drops()")
	}
}

func TestSubscribeHandlerSendsResyncOnDrop(t *testing.T) {
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
	defer conn.Close()

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

func TestGatewayLazyWarmsColdScopeOnRead(t *testing.T) {
	ctx := t.Context()

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

	if _, err := gw3.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: &foundationpb.ProjectionScope{}}); err == nil {
		t.Fatal("invalid scope error = nil")
	}
}

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
		Scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"},
	})
	if !errors.Is(err, ErrScopeInvalid) {
		t.Fatalf("invalid scope err = %v, want ErrScopeInvalid", err)
	}
}

func TestSnapshotHonorsCancelledContext(t *testing.T) {
	gw := newTestGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err == nil {
		t.Fatal("cancelled context should surface an error from Snapshot")
	}
}

func TestSnapshotHandlerSurfacesReadError(t *testing.T) {
	gw := newTestGateway(t)
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a non-200 error from the cancelled read", rec.Code)
	}
}

func TestSubscribeHandlerFailedUpgradeReleasesSubscription(t *testing.T) {
	gw := newTestGateway(t)
	handler := orgContextHandler("org_1", gw.SubscribeHandler(HandlerConfig{}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil))

	if rec.Code == http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, expected a non-upgrade failure", rec.Code)
	}

	key := ScopeKey(scope())
	if n := gw.Hub().SubscriberCount(key); n != 0 {
		t.Fatalf("subscribers after failed upgrade = %d, want 0 (subscription leaked)", n)
	}
}

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

// TestEncodeFrameCorrelationDefault covers encodeFrame directly: a blank
// correlation ID falls back to the "projectiongw" default, and the produced
// Frame carries a decodable events.Envelope plus the supplied epoch/watermark.
func TestEncodeFrameCorrelationDefault(t *testing.T) {
	mutations := []*foundationpb.RecordMutation{tickMutation("tick_1", 7, "OVS")}

	frame, err := encodeFrame(mutations, 3, "7", "   ")
	if err != nil {
		t.Fatalf("encodeFrame() err=%v", err)
	}
	if frame.Epoch != 3 || frame.Watermark != "7" {
		t.Fatalf("frame epoch/watermark = %d/%q, want 3/7", frame.Epoch, frame.Watermark)
	}
	if len(frame.Envelope) == 0 {
		t.Fatal("frame envelope should not be empty")
	}

	env, err := events.FromBinary(frame.Envelope)
	if err != nil {
		t.Fatalf("EnvelopeFromBinary() err=%v", err)
	}
	if env.CorrelationID != "projectiongw" {
		t.Fatalf("correlation = %q, want projectiongw default", env.CorrelationID)
	}

	// An explicit correlation ID is preserved verbatim.
	frame, err = encodeFrame(mutations, 1, "1", "corr-xyz")
	if err != nil {
		t.Fatalf("encodeFrame(explicit) err=%v", err)
	}
	env, err = events.FromBinary(frame.Envelope)
	if err != nil {
		t.Fatalf("EnvelopeFromBinary(explicit) err=%v", err)
	}
	if env.CorrelationID != "corr-xyz" {
		t.Fatalf("correlation = %q, want corr-xyz", env.CorrelationID)
	}
}

// TestNewGatewayNilStore covers the constructor guard.
func TestNewGatewayNilStore(t *testing.T) {
	if _, err := NewGateway(nil, 0); err == nil {
		t.Fatal("NewGateway(nil) err = nil, want ErrNilStore")
	}
}

// TestFilterScope pins the collection/tenant scoping the gateway enforces over a
// partition that may hold multiple collections: domain, collection, and a
// non-empty tenant mismatch are each dropped, while a matching scope (including a
// mutation with no organization stamped) passes through.
func TestFilterScope(t *testing.T) {
	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
	mutations := []*foundationpb.RecordMutation{
		tickMutation("keep", 1, "OVS"),                                       // matches
		{Domain: "other", Collection: "ticks", OrganizationId: "org_1"},      // domain mismatch
		{Domain: "signals", Collection: "bars", OrganizationId: "org_1"},     // collection mismatch
		{Domain: "signals", Collection: "ticks", OrganizationId: "org_evil"}, // tenant mismatch
		{Domain: "signals", Collection: "ticks"},                             // no org → passes
	}

	out := filterScope(mutations, scope)
	if len(out) != 2 {
		t.Fatalf("filterScope kept %d, want 2 (%+v)", len(out), out)
	}
	if out[0].GetRecordId() != "keep" {
		t.Fatalf("filterScope[0] = %q, want keep", out[0].GetRecordId())
	}
}

// TestOnAppliedBroadcastsToSubscribers drives the accepted-delta fan-out path:
// with a live subscriber on the scope, onApplied encodes a frame from the
// accepted mutations and broadcasts it to the hub queue.
func TestOnAppliedBroadcastsToSubscribers(t *testing.T) {
	gw := newTestGateway(t)
	sub := gw.Hub().Subscribe(scope())
	defer sub.Cancel()

	gw.onApplied("signals", []hermes.AppliedMutation{{
		Operation: hermes.OperationUpsert,
		Version:   5,
		Record: database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
		},
	}})

	select {
	case frame := <-sub.Frames:
		if len(frame.Envelope) == 0 {
			t.Fatal("broadcast frame envelope should not be empty")
		}
	default:
		t.Fatal("onApplied did not broadcast a frame to the subscriber")
	}
}
