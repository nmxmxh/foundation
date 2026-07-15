package projectiongw

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func benchStore(b *testing.B, records int) *hermes.Store {
	b.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: records + 1, MaxBytes: 1 << 30,
	})
	if err != nil {
		b.Fatalf("NewStore() err=%v", err)
	}
	ctx := context.Background()
	for i := range records {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert,
			SourceID:  fmt.Sprintf("tick_%d", i),
			Version:   uint64(i + 1),
			Record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
				RecordID: fmt.Sprintf("tick_%d", i),
				Data:     database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
			},
		}); err != nil {
			b.Fatalf("Apply() err=%v", err)
		}
	}
	return store
}

// BenchmarkSnapshotProjection measures the full snapshot build (records -> upsert
// mutations) over a warm projection.
func BenchmarkSnapshotProjection(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			store := benchStore(b, n)
			ctx := context.Background()
			query := hermes.QueryWithFilters("org_1", 0)
			b.ReportAllocs()
			for b.Loop() {
				snap, err := store.SnapshotProjection(ctx, "signals", query, hermes.Fence{}, 0)
				if err != nil || len(snap.Mutations) != n {
					b.Fatalf("snapshot len=%d err=%v", len(snap.Mutations), err)
				}
			}
		})
	}
}

// BenchmarkSnapshotLimited measures the snapshot path the gateway actually uses
// (NewGatewayForProjectedStore caps at DefaultSnapshotLimit), which engages
// hermes's ordered-index early-stop instead of an unbounded full scan.
func BenchmarkSnapshotLimited(b *testing.B) {
	store := benchStore(b, 10000)
	ctx := context.Background()
	query := hermes.QueryWithFilters("org_1", 1024) // gateway default limit
	b.ReportAllocs()
	for b.Loop() {
		snap, err := store.SnapshotProjection(ctx, "signals", query, hermes.Fence{}, 0)
		if err != nil || len(snap.Mutations) != 1024 {
			b.Fatalf("limited len=%d err=%v", len(snap.Mutations), err)
		}
	}
}

// BenchmarkSnapshotIncremental measures an incremental snapshot that returns only
// the tail of a large projection — the "don't resend whole state" path.
func BenchmarkSnapshotIncremental(b *testing.B) {
	store := benchStore(b, 10000)
	ctx := context.Background()
	query := hermes.QueryWithFilters("org_1", 0)
	since := uint64(9990) // only the last 10 records changed since
	b.ReportAllocs()
	for b.Loop() {
		snap, err := store.SnapshotProjection(ctx, "signals", query, hermes.Fence{}, since)
		if err != nil || len(snap.Mutations) != 10 {
			b.Fatalf("incremental len=%d err=%v", len(snap.Mutations), err)
		}
	}
}

// BenchmarkSnapshotIncrementalLimited measures the gateway's real incremental
// path: limited (ordered-index early-stop) + watermark filter, returning just the
// changed tail of a large projection.
func BenchmarkSnapshotIncrementalLimited(b *testing.B) {
	store := benchStore(b, 10000)
	ctx := context.Background()
	query := hermes.QueryWithFilters("org_1", 1024)
	since := uint64(9990)
	b.ReportAllocs()
	for b.Loop() {
		snap, err := store.SnapshotProjection(ctx, "signals", query, hermes.Fence{}, since)
		if err != nil || len(snap.Mutations) != 10 {
			b.Fatalf("incremental-limited len=%d err=%v", len(snap.Mutations), err)
		}
	}
}

// BenchmarkSnapshotPageDeep measures fetching a deep page (backfilling a large
// scope past the limit). Cost stays bounded by the cursor position + limit, not
// the whole scope.
func BenchmarkSnapshotPageDeep(b *testing.B) {
	store := benchStore(b, 10000)
	ctx := context.Background()
	query := hermes.QueryWithFilters("org_1", 1024)
	for _, cursor := range []uint64{0, 5000, 1025} { // first page, mid, near-last
		b.Run(fmt.Sprintf("cursor=%d", cursor), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := store.SnapshotPage(ctx, "signals", query, hermes.Fence{}, 0, cursor); err != nil {
					b.Fatalf("SnapshotPage() err=%v", err)
				}
			}
		})
	}
}

// BenchmarkEncodeFrame measures encoding one delta frame (binary envelope +
// RecordMutationBatch) for fan-out.
func BenchmarkEncodeFrame(b *testing.B) {
	muts := []*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeFrame(muts, 1, "1", "bench"); err != nil {
			b.Fatalf("encodeFrame() err=%v", err)
		}
	}
}

// BenchmarkHubBroadcast measures fan-out of one pre-encoded frame to N
// subscribers (the borrowed broadcast shape).
func BenchmarkHubBroadcast(b *testing.B) {
	for _, subs := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("subs=%d", subs), func(b *testing.B) {
			hub := NewHub(1 << 16)
			scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
			for range subs {
				sub := hub.Subscribe(scope)
				// Drain so buffers never fill during the benchmark.
				go func() {
					for range sub.Frames {
					}
				}()
			}
			key := ScopeKey(scope)
			frame := Frame{Watermark: "1", Epoch: 1}
			b.ReportAllocs()
			for b.Loop() {
				hub.Broadcast(key, frame)
			}
		})
	}
}

// BenchmarkApplyWithObserver measures the end-to-end apply path with the gateway
// observer attached (apply -> accepted batch -> encode -> fan-out) versus a bare
// apply, so the observer's added cost is visible.
func BenchmarkApplyWithObserver(b *testing.B) {
	run := func(b *testing.B, withGateway bool) {
		store, err := hermes.NewStore(hermes.ProjectionSpec{
			Name: "signals", Domain: "signals", Collection: "ticks",
			IndexedFields: []string{"symbol"}, MaxRecords: 4, MaxBytes: 1 << 20,
		})
		if err != nil {
			b.Fatalf("NewStore() err=%v", err)
		}
		if withGateway {
			gw, err := NewGateway(store, 1<<16)
			if err != nil {
				b.Fatalf("NewGateway() err=%v", err)
			}
			defer gw.Close()
			sub := gw.hub.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"})
			go func() {
				for range sub.Frames {
				}
			}()
		}
		ctx := context.Background()
		rec := database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
		}
		version := uint64(0)
		b.ReportAllocs()
		for b.Loop() {
			version++
			if _, err := store.Apply(ctx, "signals", hermes.Event{
				Operation: hermes.OperationUpsert, SourceID: "tick_1", Version: version, Record: rec,
			}); err != nil {
				b.Fatalf("Apply() err=%v", err)
			}
		}
	}
	b.Run("bare", func(b *testing.B) { run(b, false) })
	b.Run("with_observer", func(b *testing.B) { run(b, true) })
}

func benchDeliveryGateway(b *testing.B) *Gateway {
	b.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	if err != nil {
		b.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 1<<16)
	if err != nil {
		b.Fatalf("NewGateway() err=%v", err)
	}
	return gw
}

func benchAwaitSubscribers(b *testing.B, gw *Gateway, keys []string) {
	b.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for _, key := range keys {
		for gw.Hub().SubscriberCount(key) == 0 {
			if time.Now().After(deadline) {
				b.Fatalf("subscription %s not registered", key)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// benchDeliver drives one broadcast -> pump -> gorilla write -> client read
// round trip per iteration. With one frame in flight at a time the hub queue
// never drops, so the numbers isolate the delivery path itself.
func benchDeliver(b *testing.B, gw *Gateway, conn *websocket.Conn, keys []string, frames []Frame) {
	b.Helper()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		gw.Hub().Broadcast(keys[i%len(keys)], frames[i%len(frames)])
		if _, _, err := conn.ReadMessage(); err != nil {
			b.Fatalf("ReadMessage() err=%v", err)
		}
		i++
	}
}

// BenchmarkWebSocketDelivery measures a frame's full delivery path through the
// real handler stack for the single-scope stream and the multiplexed stream
// (SubscribeMultiplexHandler) at 1 and 8 scopes. The multiplexed pump adds a
// shared write mutex and per-frame drop check on top of the single-scope
// loop; this keeps that overhead visible next to the baseline.
func BenchmarkWebSocketDelivery(b *testing.B) {
	b.Run("single-scope", func(b *testing.B) {
		gw := benchDeliveryGateway(b)
		defer gw.Close()
		srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
		defer srv.Close()

		conn, resp, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/projections/signals/ticks", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil {
			b.Fatalf("ws dial err=%v", err)
		}
		defer conn.Close()

		key := ScopeKey(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"})
		benchAwaitSubscribers(b, gw, []string{key})
		frame, err := encodeFrame([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, 1, "1", "bench")
		if err != nil {
			b.Fatalf("encodeFrame() err=%v", err)
		}
		benchDeliver(b, gw, conn, []string{key}, []Frame{frame})
	})

	for _, scopes := range []int{1, 8} {
		b.Run(fmt.Sprintf("multiplex/scopes=%d", scopes), func(b *testing.B) {
			gw := benchDeliveryGateway(b)
			defer gw.Close()
			srv := httptest.NewServer(orgContextHandler("org_1", gw.Handler(HandlerConfig{})))
			defer srv.Close()

			conn, resp, err := websocket.DefaultDialer.Dial(
				"ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/projections/", nil)
			if resp != nil {
				defer func() { _ = resp.Body.Close() }()
			}
			if err != nil {
				b.Fatalf("ws dial err=%v", err)
			}
			defer conn.Close()

			cmd := MultiplexCommand{Type: "subscribe"}
			keys := make([]string, 0, scopes)
			frames := make([]Frame, 0, scopes)
			for i := range scopes {
				collection := fmt.Sprintf("ticks_%d", i)
				cmd.Scopes = append(cmd.Scopes, MultiplexScope{Domain: "signals", Collection: collection})
				keys = append(keys, "org_1:signals:"+collection)
				mut := tickMutation("tick_1", 1, "OVS")
				mut.Collection = collection
				frame, err := encodeFrame([]*foundationpb.RecordMutation{mut}, 1, "1", "bench")
				if err != nil {
					b.Fatalf("encodeFrame() err=%v", err)
				}
				frames = append(frames, frame)
			}
			payload, err := json.Marshal(cmd)
			if err != nil {
				b.Fatalf("marshal subscribe err=%v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				b.Fatalf("subscribe write err=%v", err)
			}
			benchAwaitSubscribers(b, gw, keys)
			benchDeliver(b, gw, conn, keys, frames)
		})
	}
}
