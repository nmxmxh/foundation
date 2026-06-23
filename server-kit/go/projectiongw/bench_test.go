package projectiongw

import (
	"context"
	"fmt"
	"testing"

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
