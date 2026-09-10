package projectiongw

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"google.golang.org/protobuf/proto"
)

// testSnapshotCacheBudget is generous enough that these tests never evict; the
// eviction and budget behavior is covered directly against snapshotCache.
const testSnapshotCacheBudget = 1 << 20

func newCachedTestGateway(t *testing.T, maxRecords int) *Gateway {
	t.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    maxRecords,
		MaxBytes:      1 << 24,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithSnapshotCache(testSnapshotCacheBudget))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	return gw
}

// mustEnvelopeCorr builds a projection envelope with an explicit correlation id.
// The shared mustEnvelope helper hardcodes one id, and hermes treats a repeated
// correlation as the same logical apply — so successive applies through it are
// deduped rather than accumulating, which silently flattens any test that needs
// more than one epoch.
func mustEnvelopeCorr(t *testing.T, correlation string, muts ...*foundationpb.RecordMutation) []events.Envelope {
	t.Helper()
	env, err := hermes.NewProjectionEnvelope(muts, correlation)
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	return []events.Envelope{env}
}

func snapshotViaHandler(t *testing.T, gw *Gateway, target string) (*httptest.ResponseRecorder, *foundationpb.ProjectionSnapshot) {
	t.Helper()
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var snap foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return rec, &snap
}

// TestSnapshotCacheServesIdenticalBytes is the safety property: a cached
// response must be indistinguishable from a freshly encoded one.
func TestSnapshotCacheServesIdenticalBytes(t *testing.T) {
	gw := newCachedTestGateway(t, 16)
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	first, firstSnap := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if stats := gw.CacheStats(); stats.Entries != 1 {
		t.Fatalf("after first read: entries = %d, want 1", stats.Entries)
	}

	second, secondSnap := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if stats := gw.CacheStats(); stats.Hits != 1 {
		t.Fatalf("after second read: hits = %d, want 1", stats.Hits)
	}

	if first.Body.String() != second.Body.String() {
		t.Fatal("cached response bytes differ from the freshly encoded response")
	}
	if !proto.Equal(firstSnap, secondSnap) {
		t.Fatal("cached snapshot is not proto-equal to the fresh one")
	}
	for _, header := range []string{"Content-Type", "X-Projection-Epoch", "X-Projection-Watermark"} {
		if first.Header().Get(header) != second.Header().Get(header) {
			t.Fatalf("header %s differs: %q vs %q", header, first.Header().Get(header), second.Header().Get(header))
		}
	}
}

// TestSnapshotCacheInvalidatesOnEpochAdvance is the correctness property that
// matters most: an apply must never be served a stale page.
func TestSnapshotCacheInvalidatesOnEpochAdvance(t *testing.T) {
	gw := newCachedTestGateway(t, 16)
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	_, before := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if len(before.GetBatch().GetMutations()) != 1 {
		t.Fatalf("first read returned %d records, want 1", len(before.GetBatch().GetMutations()))
	}

	// Warm the cache, then move the page.
	snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelopeCorr(t, "corr-second", tickMutation("tick_2", 2, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	_, after := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if got := len(after.GetBatch().GetMutations()); got != 2 {
		t.Fatalf("after apply: %d records, want 2 — a stale page was served", got)
	}
	if after.GetEpoch() <= before.GetEpoch() {
		t.Fatalf("epoch did not advance: %d then %d", before.GetEpoch(), after.GetEpoch())
	}
	if stats := gw.CacheStats(); stats.Stale == 0 {
		t.Fatal("expected the epoch advance to be observed as a stale entry")
	}
}

// TestSnapshotCacheKeysOnVectorModeAndLimit guards against two request shapes
// sharing one body when they must not.
func TestSnapshotCacheKeysOnVectorModeAndLimit(t *testing.T) {
	gw := newCachedTestGateway(t, 16)
	ticks := make([]*foundationpb.RecordMutation, 4)
	for i := range ticks {
		ticks[i] = tickMutation(fmt.Sprintf("tick_%d", i), uint64(i+1), "OVS")
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, ticks...)); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	_, all := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	_, limited := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks?limit=2")
	if len(all.GetBatch().GetMutations()) == len(limited.GetBatch().GetMutations()) {
		t.Fatalf("limit was ignored: both reads returned %d records", len(all.GetBatch().GetMutations()))
	}
	if got := len(limited.GetBatch().GetMutations()); got != 2 {
		t.Fatalf("limit=2 returned %d records", got)
	}

	// Two distinct shapes, two entries.
	if stats := gw.CacheStats(); stats.Entries != 2 {
		t.Fatalf("entries = %d, want 2 (one per request shape)", stats.Entries)
	}
}

// TestSnapshotCacheBypassesResumeAndCursor confirms per-client request shapes
// never occupy the cache.
func TestSnapshotCacheBypassesResumeAndCursor(t *testing.T) {
	gw := newCachedTestGateway(t, 16)
	ticks := make([]*foundationpb.RecordMutation, 3)
	for i := range ticks {
		ticks[i] = tickMutation(fmt.Sprintf("tick_%d", i), uint64(i+1), "OVS")
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, ticks...)); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	snapshotViaHandler(t, gw, "/v1/projections/signals/ticks?since=1")
	snapshotViaHandler(t, gw, "/v1/projections/signals/ticks?cursor=2")
	if stats := gw.CacheStats(); stats.Entries != 0 {
		t.Fatalf("entries = %d, want 0 — resume/cursor requests must bypass the cache", stats.Entries)
	}
}

// TestSnapshotCacheRespectsByteBudget confirms the budget is a ceiling and that
// an oversized page is refused rather than evicting everything to fit.
func TestSnapshotCacheRespectsByteBudget(t *testing.T) {
	cache := newSnapshotCache(256)
	if cache == nil {
		t.Fatal("newSnapshotCache(256) returned nil")
	}

	// An entry larger than the whole budget is never admitted.
	cache.store(snapshotCacheKey{scope: "big"}, &snapshotCacheEntry{epoch: 1, body: make([]byte, 512)})
	if stats := cache.stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("oversized entry was admitted: %+v", stats)
	}

	// Entries that fit accumulate, and eviction keeps the budget.
	for i := range 8 {
		cache.store(snapshotCacheKey{scope: fmt.Sprintf("scope_%d", i)}, &snapshotCacheEntry{epoch: 1, body: make([]byte, 64)})
	}
	stats := cache.stats()
	if stats.Bytes > 256 {
		t.Fatalf("bytes = %d, over the 256 budget", stats.Bytes)
	}
	if stats.Evictions == 0 {
		t.Fatal("expected evictions once the budget was exceeded")
	}
}

// TestSnapshotCacheEvictsLeastRecentlyUsed confirms a repeatedly read entry
// survives while cold ones are dropped.
func TestSnapshotCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newSnapshotCache(200)
	hot := snapshotCacheKey{scope: "hot"}
	cache.store(hot, &snapshotCacheEntry{epoch: 1, body: make([]byte, 64)})

	for i := range 4 {
		// Touch the hot entry between each insertion.
		if _, outcome := cache.lookup(hot, 1); outcome != cacheOutcomeHit {
			t.Fatalf("hot entry evicted after %d insertions (outcome %s)", i, outcome)
		}
		cache.store(snapshotCacheKey{scope: fmt.Sprintf("cold_%d", i)}, &snapshotCacheEntry{epoch: 1, body: make([]byte, 64)})
	}
	if _, outcome := cache.lookup(hot, 1); outcome != cacheOutcomeHit {
		t.Fatalf("hot entry did not survive LRU eviction (outcome %s)", outcome)
	}
}

// TestSnapshotCacheDisabledByDefault confirms the substrate default is unchanged
// behavior.
func TestSnapshotCacheDisabledByDefault(t *testing.T) {
	gw := newTestGateway(t)
	if gw.snapshotCache != nil {
		t.Fatal("snapshot cache is enabled without WithSnapshotCache")
	}
	if stats := gw.CacheStats(); stats != (SnapshotCacheStats{}) {
		t.Fatalf("CacheStats() on a disabled cache = %+v, want zero value", stats)
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, tickMutation("tick_1", 1, "OVS"))); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	// And the uncached path still serves a correct snapshot through the streamed
	// writer.
	_, snap := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks")
	if got := snap.GetBatch().GetMutations(); len(got) != 1 || got[0].GetRecordId() != "tick_1" {
		t.Fatalf("uncached snapshot mutations = %+v", got)
	}
}

func TestSnapshotCacheZeroBudgetIsNil(t *testing.T) {
	for _, budget := range []int{0, -1} {
		if cache := newSnapshotCache(budget); cache != nil {
			t.Fatalf("newSnapshotCache(%d) = non-nil, want nil", budget)
		}
	}
	// Every cache method is nil-safe.
	var cache *snapshotCache
	if _, outcome := cache.lookup(snapshotCacheKey{}, 1); outcome != cacheOutcomeBypass {
		t.Fatalf("nil cache lookup outcome = %s, want bypass", outcome)
	}
	cache.store(snapshotCacheKey{}, &snapshotCacheEntry{body: []byte{1}})
	cache.invalidateScope("anything")
	if stats := cache.stats(); stats != (SnapshotCacheStats{}) {
		t.Fatalf("nil cache stats = %+v, want zero value", stats)
	}
}

func TestAudienceDigestIsOrderAndDuplicateInsensitive(t *testing.T) {
	base := audienceDigest([]string{"team_a", "team_b"})
	if base == "" {
		t.Fatal("digest of a non-empty set is empty")
	}
	for _, equivalent := range [][]string{
		{"team_b", "team_a"},
		{"team_a", "team_b", "team_a"},
		{" team_a ", "team_b"},
		{"team_a", "", "team_b"},
	} {
		if got := audienceDigest(equivalent); got != base {
			t.Fatalf("audienceDigest(%v) = %s, want %s", equivalent, got, base)
		}
	}
	if got := audienceDigest([]string{"team_a"}); got == base {
		t.Fatal("a strict subset produced the same digest")
	}
	for _, empty := range [][]string{nil, {}, {""}, {"  "}} {
		if got := audienceDigest(empty); got != "" {
			t.Fatalf("audienceDigest(%v) = %q, want empty", empty, got)
		}
	}
}

// TestAudienceDigestIsInjectiveAcrossBoundaries guards the length-prefix in the
// digest: concatenation-equivalent sets must not collide.
func TestAudienceDigestIsInjectiveAcrossBoundaries(t *testing.T) {
	if audienceDigest([]string{"a", "bc"}) == audienceDigest([]string{"ab", "c"}) {
		t.Fatal("audience sets {a,bc} and {ab,c} collide")
	}
}

// TestSnapshotCacheConcurrentReadersUnderApplies is the race-detector case: many
// readers share one cached body while applies keep advancing the epoch. A reader
// must never observe a torn or stale page, and the cache must not be corrupted
// by concurrent store and evict.
func TestSnapshotCacheConcurrentReadersUnderApplies(t *testing.T) {
	gw := newCachedTestGateway(t, 64)
	ticks := make([]*foundationpb.RecordMutation, 8)
	for i := range ticks {
		ticks[i] = tickMutation(fmt.Sprintf("tick_%d", i), uint64(i+1), "OVS")
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, ticks...)); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	handler := orgContextHandler("org_1", gw.Handler(HandlerConfig{}))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		cacheApplyLoop(gw, stop)
	}()

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cacheReadLoop(t, handler, 200)
		}()
	}

	// Let the readers finish, then stop the writer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	time.Sleep(150 * time.Millisecond)
	close(stop)
	<-done

	stats := gw.CacheStats()
	if stats.Bytes > stats.MaxBytes {
		t.Fatalf("cache bytes = %d, over budget %d", stats.Bytes, stats.MaxBytes)
	}
	if stats.Hits+stats.Misses+stats.Stale == 0 {
		t.Fatal("cache saw no traffic")
	}
	t.Logf("cache under contention: %+v", stats)
}

// cacheApplyLoop advances the projection epoch until stop closes, so readers
// race against a moving page.
func cacheApplyLoop(gw *Gateway, stop <-chan struct{}) {
	for version := uint64(100); ; version++ {
		select {
		case <-stop:
			return
		default:
		}
		env, err := hermes.NewProjectionEnvelope(
			[]*foundationpb.RecordMutation{tickMutation("tick_hot", version, "OVS")},
			fmt.Sprintf("corr-%d", version),
		)
		if err != nil {
			return
		}
		if _, err := gw.ApplyEnvelopes(context.Background(), "signals", []events.Envelope{env}); err != nil {
			return
		}
	}
}

// cacheReadLoop issues snapshot reads and fails the test if any response is not
// a well-formed snapshot. It runs off the test goroutine, so it reports with
// t.Error rather than t.Fatal.
func cacheReadLoop(t *testing.T, handler http.Handler, reads int) {
	t.Helper()
	for range reads {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/projections/signals/ticks", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d", rec.Code)
			return
		}
		var snap foundationpb.ProjectionSnapshot
		if err := proto.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
			t.Errorf("decode snapshot: %v", err)
			return
		}
		if snap.GetBatch() == nil {
			t.Error("snapshot carried no batch")
			return
		}
	}
}

// TestSnapshotHandlerCursorPagination covers the keyset backfill contract over
// HTTP: a client pages older records by presenting the prior response's
// next_cursor. The handler previously never read the cursor query parameter, so
// this half of the documented pagination contract was unreachable — every
// "next page" request silently returned the first page again.
func TestSnapshotHandlerCursorPagination(t *testing.T) {
	gw := newCachedTestGateway(t, 16)
	ticks := make([]*foundationpb.RecordMutation, 6)
	for i := range ticks {
		ticks[i] = tickMutation(fmt.Sprintf("tick_%d", i), uint64(i+1), "OVS")
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", mustEnvelope(t, ticks...)); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	_, first := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks?limit=2")
	if got := len(first.GetBatch().GetMutations()); got != 2 {
		t.Fatalf("first page has %d records, want 2", got)
	}
	if !first.GetHasMore() {
		t.Fatal("first page reports has_more=false with 6 records at limit 2")
	}
	if first.GetNextCursor() == "" {
		t.Fatal("first page returned no next_cursor")
	}

	_, second := snapshotViaHandler(t, gw, "/v1/projections/signals/ticks?limit=2&cursor="+first.GetNextCursor())
	if got := len(second.GetBatch().GetMutations()); got != 2 {
		t.Fatalf("second page has %d records, want 2", got)
	}

	firstIDs := map[string]bool{}
	for _, m := range first.GetBatch().GetMutations() {
		firstIDs[m.GetRecordId()] = true
	}
	for _, m := range second.GetBatch().GetMutations() {
		if firstIDs[m.GetRecordId()] {
			t.Fatalf("record %q appeared on both pages — the cursor was ignored", m.GetRecordId())
		}
	}
}
