package projectiongw

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// End-to-end snapshot serve benchmarks through the real HTTP handler.
//
// The microbenchmarks in the hermes package measure the wire assembly in
// isolation. These measure what a deployment actually sees: scope resolution,
// the bounded hermes read, the encode, and the write, with and without the
// epoch-keyed cache. The gap between them is the honest figure — the isolated
// numbers do not include the read, which the cache also skips.

func benchGateway(b *testing.B, cacheBytes int, records int, vectorDim int) *Gateway {
	b.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    records * 2,
		MaxBytes:      1 << 30,
	})
	if err != nil {
		b.Fatalf("NewStore() err=%v", err)
	}
	opts := []Option{}
	if cacheBytes > 0 {
		opts = append(opts, WithSnapshotCache(cacheBytes))
	}
	gw, err := NewGateway(store, 0, opts...)
	if err != nil {
		b.Fatalf("NewGateway() err=%v", err)
	}

	mutations := make([]*foundationpb.RecordMutation, records)
	for i := range mutations {
		var vector []float32
		if vectorDim > 0 {
			vector = make([]float32, vectorDim)
			for j := range vector {
				vector[j] = float32(i*vectorDim+j) * 0.25
			}
		}
		mutations[i] = &foundationpb.RecordMutation{
			Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
			Version:        uint64(i + 1),
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationId: "org_1",
			RecordId:       fmt.Sprintf("tick_%06d", i),
			Fields: []*foundationpb.FieldValue{
				{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"}}},
				{Name: "price", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_DoubleValue{DoubleValue: float64(i) * 1.5}}},
			},
			Vector: vector,
		}
	}
	env, err := hermes.NewProjectionEnvelope(mutations, "corr-bench")
	if err != nil {
		b.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(b.Context(), "signals", []events.Envelope{env}); err != nil {
		b.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	return gw
}

// benchSnapshotCacheBudget is large enough that these benchmarks never evict, so
// they measure the hit path rather than eviction churn.
const benchSnapshotCacheBudget = 1 << 28

// benchServeLoop drives one HTTP snapshot request per iteration and reports the
// response size alongside the timing, so a change in bytes cannot be mistaken
// for a change in speed. When warm is set the cache entry is populated first, so
// the loop measures steady-state hits.
func benchServeLoop(b *testing.B, gw *Gateway, target string, warm bool) {
	b.Helper()
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))
	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, target, nil)
	if warm {
		handler.ServeHTTP(&discardResponseWriter{}, req)
	}
	b.ReportAllocs()
	var bytesPerOp int64
	for b.Loop() {
		w := &discardResponseWriter{}
		handler.ServeHTTP(w, req)
		if w.n == 0 {
			b.Fatal("empty response")
		}
		bytesPerOp = w.n
	}
	b.ReportMetric(float64(bytesPerOp), "responseB")
	if warm {
		if stats := gw.CacheStats(); stats.Hits == 0 {
			b.Fatal("benchmark never hit the cache")
		}
	}
}

// discardResponseWriter consumes a response without retaining it, so the
// benchmark measures the gateway rather than httptest's recorder buffer.
type discardResponseWriter struct {
	header http.Header
	n      int64
}

func (w *discardResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *discardResponseWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func (w *discardResponseWriter) WriteHeader(int) {}

// BenchmarkSnapshotServeRepeatReads is the fan-out shape: many subscribers
// reading the same unchanged page. Each iteration is one HTTP request.
func BenchmarkSnapshotServeRepeatReads(b *testing.B) {
	for _, records := range []int{100, 1000} {
		for _, dim := range []int{0, 128} {
			label := fmt.Sprintf("records%d/dim%d", records, dim)
			// The HTTP surface defaults to EXCLUDE, so a vector dimension only
			// reaches the response when the caller asks for it. Without this the
			// dim axis measures nothing.
			target := "/v1/projections/signals/ticks"
			if dim > 0 {
				target += "?vectors=include"
			}

			b.Run(label+"/NoCache", func(b *testing.B) {
				gw := benchGateway(b, 0, records, dim)
				benchServeLoop(b, gw, target, false)
			})

			b.Run(label+"/Cached", func(b *testing.B) {
				gw := benchGateway(b, benchSnapshotCacheBudget, records, dim)
				benchServeLoop(b, gw, target, true)
			})
		}
	}
}

// BenchmarkSnapshotServeColdEachTime is the worst case for the cache: the page
// moves between every read, so every request pays a miss plus a store. This is
// the number that says whether the cache is safe to leave on for a write-heavy
// scope.
func BenchmarkSnapshotServeColdEachTime(b *testing.B) {
	const records = 1000

	run := func(b *testing.B, cacheBytes int) {
		gw := benchGateway(b, cacheBytes, records, 0)
		handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))
		req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/v1/projections/signals/ticks", nil)
		version := uint64(records + 1)
		b.ReportAllocs()
		for b.Loop() {
			// Advance the epoch so the cached entry is always stale.
			version++
			env, err := hermes.NewProjectionEnvelope(
				[]*foundationpb.RecordMutation{{
					Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
					Version:        version,
					Domain:         "signals",
					Collection:     "ticks",
					OrganizationId: "org_1",
					RecordId:       "tick_000000",
					Fields: []*foundationpb.FieldValue{
						{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"}}},
					},
				}},
				fmt.Sprintf("corr-%d", version),
			)
			if err != nil {
				b.Fatalf("NewProjectionEnvelope() err=%v", err)
			}
			if _, err := gw.ApplyEnvelopes(b.Context(), "signals", []events.Envelope{env}); err != nil {
				b.Fatalf("ApplyEnvelopes() err=%v", err)
			}
			w := &discardResponseWriter{}
			handler.ServeHTTP(w, req)
			if w.n == 0 {
				b.Fatal("empty response")
			}
		}
	}

	b.Run("NoCache", func(b *testing.B) { run(b, 0) })
	b.Run("Cached", func(b *testing.B) { run(b, benchSnapshotCacheBudget) })
}
