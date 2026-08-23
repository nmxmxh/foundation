package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
)

const (
	testDomain     = "seo"
	testCollection = "facets"
	testOrg        = "org-alpha"
	testRecord     = "rec-1"
)

// --- test doubles ---

type countingStore struct {
	StateStore
	gets    atomic.Int64
	upserts atomic.Int64
}

func (c *countingStore) GetRecord(ctx context.Context, d, col, o, r string) (DomainRecord, bool, error) {
	c.gets.Add(1)
	return c.StateStore.GetRecord(ctx, d, col, o, r)
}

func (c *countingStore) UpsertRecord(ctx context.Context, rec DomainRecord) (DomainRecord, error) {
	c.upserts.Add(1)
	return c.StateStore.UpsertRecord(ctx, rec)
}

// scriptedStore pins exact results, including stale timestamps, so race and
// fidelity tests do not depend on clock behavior of a live store.
type scriptedStore struct {
	getRes    DomainRecord
	getFound  bool
	getErr    error
	upsertRes DomainRecord
	upsertErr error
	deleteErr error
	gets      atomic.Int64
}

func (s *scriptedStore) UpsertRecord(context.Context, DomainRecord) (DomainRecord, error) {
	return s.upsertRes, s.upsertErr
}

func (s *scriptedStore) GetRecord(context.Context, string, string, string, string) (DomainRecord, bool, error) {
	s.gets.Add(1)
	return s.getRes, s.getFound, s.getErr
}

func (s *scriptedStore) ForEachRecord(context.Context, string, string, string, RecordQuery, RecordVisitor) error {
	return nil
}

func (s *scriptedStore) ListRecords(context.Context, string, string, string, RecordQuery) ([]DomainRecord, error) {
	return nil, nil
}

func (s *scriptedStore) CountRecords(context.Context, string, string, string, RecordQuery) (int64, error) {
	return 0, nil
}

func (s *scriptedStore) EstimateCount(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

func (s *scriptedStore) DeleteRecord(context.Context, string, string, string, string) error {
	return s.deleteErr
}

type flakyBackend struct {
	cache.Backend
	mu      sync.Mutex
	failGet bool
	failSet bool
}

func (b *flakyBackend) Get(ctx context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	fail := b.failGet
	b.mu.Unlock()
	if fail {
		return nil, errors.New("redis unavailable")
	}
	return b.Backend.Get(ctx, key)
}

func (b *flakyBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	b.mu.Lock()
	fail := b.failSet
	b.mu.Unlock()
	if fail {
		return errors.New("redis unavailable")
	}
	return b.Backend.Set(ctx, key, value, ttl)
}

type slowBackend struct {
	cache.Backend
	delay time.Duration
}

func (b *slowBackend) Get(ctx context.Context, key string) ([]byte, error) {
	select {
	case <-time.After(b.delay):
		return b.Backend.Get(ctx, key)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- fixtures ---

func newTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	backend := cache.NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	return cache.New(cache.Config{Backend: backend})
}

func newWrapped(t *testing.T, inner StateStore, opts CachedStateStoreOptions) *CachedStateStore {
	t.Helper()
	wrapped, err := NewCachedStateStore(inner, newTestCache(t), opts)
	if err != nil {
		t.Fatalf("NewCachedStateStore: %v", err)
	}
	return wrapped
}

func sampleRecord(value string) DomainRecord {
	return DomainRecord{
		Domain:         testDomain,
		Collection:     testCollection,
		OrganizationID: testOrg,
		RecordID:       testRecord,
		Data:           RecordDataFromPairs(RecordField{Name: "title", Value: StringValue(value)}),
	}
}

func titleOf(t *testing.T, rec DomainRecord) string {
	t.Helper()
	value, ok := rec.Data.Get("title")
	if !ok {
		t.Fatal("title field missing")
	}
	return value.Text
}

type fallbackRecorder struct {
	mu  sync.Mutex
	ops []string
}

func (r *fallbackRecorder) record(op string, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

func (r *fallbackRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ops)
}

// --- constructor guards ---

func TestNewCachedStateStoreRejectsNilParts(t *testing.T) {
	if _, err := NewCachedStateStore(nil, cache.New(cache.Config{}), CachedStateStoreOptions{}); err == nil {
		t.Fatal("want error for nil inner")
	}
	if _, err := NewCachedStateStore(NewMemoryDB(), nil, CachedStateStoreOptions{}); err == nil {
		t.Fatal("want error for nil cache")
	}
}

func TestNilReceiverGuards(t *testing.T) {
	var s *CachedStateStore
	ctx := context.Background()
	if s.Unwrap() != nil {
		t.Fatal("nil Unwrap must be nil")
	}
	if _, _, err := s.GetRecord(ctx, "d", "c", "o", "r"); err == nil {
		t.Fatal("nil GetRecord must fail")
	}
	if _, err := s.UpsertRecord(ctx, DomainRecord{}); err == nil {
		t.Fatal("nil UpsertRecord must fail")
	}
	if err := s.DeleteRecord(ctx, "d", "c", "o", "r"); err == nil {
		t.Fatal("nil DeleteRecord must fail")
	}
	if err := s.InvalidateScope(ctx, "d", "c", "o"); err == nil {
		t.Fatal("nil InvalidateScope must fail")
	}
	if err := s.InvalidateOrganization(ctx, "o"); err == nil {
		t.Fatal("nil InvalidateOrganization must fail")
	}
	if err := s.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("nil Exec must fail")
	}
	if r := s.QueryRow(ctx, "SELECT 1"); r.Scan(new(int)) == nil {
		t.Fatal("nil QueryRow scan must fail")
	}
	if _, err := s.Query(ctx, "SELECT 1"); err == nil {
		t.Fatal("nil Query must fail")
	}
	if _, err := s.BeginTx(ctx); err == nil {
		t.Fatal("nil BeginTx must fail")
	}
	if got := s.Stats(); got.TotalConns != 0 {
		t.Fatalf("nil Stats must be zero, got %+v", got)
	}
	s.Close() // must not panic
}

// --- core behavior ---

func TestWriteThroughServesHitsWithoutInnerReads(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	if _, err := s.UpsertRecord(ctx, sampleRecord("v1")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := range 5 {
		rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
		if err != nil || !found || titleOf(t, rec) != "v1" {
			t.Fatalf("read %d: found=%v err=%v", i, found, err)
		}
	}
	if got := inner.gets.Load(); got != 0 {
		t.Fatalf("inner gets = %d, want 0 (write-through should warm cache)", got)
	}
}

func TestMissPopulatesThenWarmRead(t *testing.T) {
	mem := NewMemoryDB()
	inner := &countingStore{StateStore: mem}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	if _, err := mem.UpsertRecord(ctx, sampleRecord("seed")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found {
		t.Fatalf("cold read: found=%v err=%v", found, err)
	}
	if inner.gets.Load() != 1 {
		t.Fatalf("cold read inner gets = %d, want 1", inner.gets.Load())
	}
	warm, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found {
		t.Fatalf("warm read: found=%v err=%v", found, err)
	}
	if !bytes.Equal(mustJSON(warm.Data), mustJSON(first.Data)) {
		t.Fatal("warm read diverged from cold read")
	}
	if inner.gets.Load() != 1 {
		t.Fatalf("warm read inner gets = %d, want 1", inner.gets.Load())
	}
}

func TestNegativeCaching(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	for i := range 3 {
		if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, "missing"); err != nil || found {
			t.Fatalf("read %d: found=%v err=%v", i, found, err)
		}
	}
	if got := inner.gets.Load(); got != 1 {
		t.Fatalf("inner gets with negative cache = %d, want 1", got)
	}
}

func TestEmptyIdentityPassesThrough(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	if _, _, err := s.GetRecord(ctx, "", testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("passthrough get: %v", err)
	}
	if got := inner.gets.Load(); got != 1 {
		t.Fatalf("passthrough must reach inner, gets=%d", got)
	}
}

func TestUpsertRefreshesStaleEntry(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	time.Sleep(time.Millisecond) // ensure distinct UpdatedAt versions
	if _, err := s.UpsertRecord(ctx, sampleRecord("old")); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := s.UpsertRecord(ctx, sampleRecord("new")); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, rec) != "new" {
		t.Fatalf("post-refresh read: found=%v title=%q err=%v", found, titleOf(t, rec), err)
	}
	if got := inner.gets.Load(); got != 0 {
		t.Fatalf("refresh must serve later reads from cache, gets=%d", got)
	}
}

func TestDeleteTombstonesWithoutExtraInnerReads(t *testing.T) {
	mem := NewMemoryDB()
	inner := &countingStore{StateStore: mem}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	if _, err := s.UpsertRecord(ctx, sampleRecord("gone")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.DeleteRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || found {
		t.Fatalf("post-delete read: found=%v err=%v", found, err)
	}
	if got := inner.gets.Load(); got != 1 { // exactly the recreate-guard re-read
		t.Fatalf("inner gets after delete+tombstone hit = %d, want 1", got)
	}
	if _, err := mem.UpsertRecord(ctx, sampleRecord("back")); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	// Out-of-band recreate bypasses this wrapper, so it stays invisible until
	// the tombstone TTL lapses: staleness is bounded by NegativeTTL contract.
	if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || found {
		t.Fatalf("tombstone window read: found=%v err=%v", found, err)
	}
	if inner.gets.Load() != 1 {
		t.Fatalf("tombstone hit must not reach inner, gets=%d", inner.gets.Load())
	}
}

// --- regression guard: dual-write race ---

func TestDualWriteRaceOlderCommitCannotClobberNewer(t *testing.T) {
	now := time.Now().UTC()
	newer := sampleRecord("from-w2")
	newer.CreatedAt = now.Add(-time.Minute)
	newer.UpdatedAt = now
	older := sampleRecord("from-w1")
	older.CreatedAt = newer.CreatedAt
	older.UpdatedAt = now.Add(-time.Second)

	inner := &scriptedStore{upsertRes: older}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	// W2 already refreshed the shared cache with its committed value.
	key := s.recordKey(testDomain, testCollection, testOrg, testRecord)
	if err := s.cache.Set(ctx, key, newEnvelope(newer, true), time.Minute); err != nil {
		t.Fatalf("seed newer entry: %v", err)
	}

	// W1's earlier commit arrives late and must not overwrite it.
	if _, err := s.UpsertRecord(ctx, older); err != nil {
		t.Fatalf("late upsert: %v", err)
	}
	rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, rec) != "from-w2" {
		t.Fatalf("older commit clobbered newer cache: found=%v title=%q err=%v", found, titleOf(t, rec), err)
	}

	// Equal versions (idempotent replay) remain writable.
	inner.upsertRes = newer
	if _, err := s.UpsertRecord(ctx, newer); err != nil {
		t.Fatalf("equal-version upsert: %v", err)
	}
	if inner.gets.Load() != 0 {
		t.Fatalf("post-race reads must stay cache-served, gets=%d", inner.gets.Load())
	}
}

// --- regression guard: degradation / fallback ---

func TestCacheOutageDegradesToInner(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	fallbacks := &fallbackRecorder{}
	backend := &flakyBackend{Backend: cache.NewMemoryBackend()}
	s, err := NewCachedStateStore(inner, cache.New(cache.Config{Backend: backend}), CachedStateStoreOptions{
		OnFallback: fallbacks.record,
	})
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	ctx := context.Background()

	backend.mu.Lock()
	backend.failGet, backend.failSet = true, true
	backend.mu.Unlock()

	if _, err := s.UpsertRecord(ctx, sampleRecord("resilient")); err != nil {
		t.Fatalf("upsert during outage: %v", err)
	}
	rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, rec) != "resilient" {
		t.Fatalf("read during outage: found=%v title=%q err=%v", found, titleOf(t, rec), err)
	}
	if err := s.DeleteRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("delete during outage: %v", err)
	}
	if fallbacks.count() == 0 {
		t.Fatal("expected fallback events during outage")
	}
	if got := inner.gets.Load(); got != 2 { // outage read + delete recreate-guard
		t.Fatalf("inner gets during outage = %d, want 2", got)
	}
}

func TestSlowBackendHonorsOpTimeout(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	fallbacks := &fallbackRecorder{}
	backend := cache.NewMemoryBackend()
	s, err := NewCachedStateStore(inner, cache.New(cache.Config{
		Backend: &slowBackend{Backend: backend, delay: 200 * time.Millisecond},
	}), CachedStateStoreOptions{
		CacheTimeout: 5 * time.Millisecond,
		OnFallback:   fallbacks.record,
	})
	if err != nil {
		t.Fatalf("wrap slow: %v", err)
	}
	ctx := context.Background()

	if _, err := s.UpsertRecord(ctx, sampleRecord("slow")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, rec) != "slow" {
		t.Fatalf("read past slow backend: found=%v title=%q err=%v", found, titleOf(t, rec), err)
	}
	if fallbacks.count() == 0 {
		t.Fatal("expected timeout-driven fallback ops")
	}
}

func TestCorruptPayloadFallsBackToReadThrough(t *testing.T) {
	mem := NewMemoryDB()
	inner := &countingStore{StateStore: mem}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	if _, err := mem.UpsertRecord(ctx, sampleRecord("truth")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	key := s.recordKey(testDomain, testCollection, testOrg, testRecord)
	// Valid JSON of the wrong shape: marshals fine, but must fail decoding
	// into the envelope so the read degrades to a read-through repair.
	if err := s.cache.Set(ctx, key, json.RawMessage("[1,2,3]"), time.Minute); err != nil {
		t.Fatalf("inject corrupt: %v", err)
	}
	rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, rec) != "truth" {
		t.Fatalf("corrupt-payload read: found=%v title=%q err=%v", found, titleOf(t, rec), err)
	}
	if inner.gets.Load() != 1 {
		t.Fatalf("corrupt read must reach inner once, gets=%d", inner.gets.Load())
	}
	if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || !found {
		t.Fatalf("repaired read: found=%v err=%v", found, err)
	}
	if inner.gets.Load() != 1 {
		t.Fatalf("repair must overwrite poison entry, gets=%d", inner.gets.Load())
	}
}

// --- regression guard: byte fidelity ---

func TestByteFidelityAcrossCacheRoundTrip(t *testing.T) {
	raw := []byte(`{"n":9007199254740993,"nested":{"deep":[1,true,null]}}`)
	now := time.Now().UTC()
	truth := DomainRecord{
		Domain:         testDomain,
		Collection:     testCollection,
		OrganizationID: "",
		RecordID:       testRecord,
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
		Data: RecordDataFromPairs(
			RecordField{Name: "amount_minor", Value: IntValue(math.MaxInt64)},
			RecordField{Name: "payload", Value: RawValue(raw)},
			RecordField{Name: "ratio", Value: FloatValue(0.1)},
		),
		// NOTE: uint64 values above MaxInt64 are intentionally absent. The
		// shared extension number decoder (extension/value.go:670) maps
		// integer literals via Int64 then Float64 only, so such values
		// degrade identically on every read-back lane (Postgres or cache).
		// Callers needing exact u64 must wrap them in RawValue today.
	}
	inner := &scriptedStore{upsertRes: truth}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	stored, err := s.UpsertRecord(ctx, truth)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cached, found, err := s.GetRecord(ctx, testDomain, testCollection, "", testRecord)
	if err != nil || !found {
		t.Fatalf("cached read: found=%v err=%v", found, err)
	}
	if !cached.CreatedAt.Equal(stored.CreatedAt) || !cached.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatalf("timestamp drift: created %v/%v updated %v/%v",
			cached.CreatedAt, stored.CreatedAt, cached.UpdatedAt, stored.UpdatedAt)
	}
	assertSameField(t, "amount_minor", stored, cached)
	assertSameField(t, "payload", stored, cached)
	assertSameField(t, "ratio", stored, cached)

	payload, _ := cached.Data.Get("payload")
	if !bytes.Equal(payload.Raw, bytes.TrimSpace(raw)) {
		t.Fatalf("nested JSON bytes drifted:\n got: %s\nwant: %s", payload.Raw, raw)
	}
}

func assertSameField(t *testing.T, name string, stored, cached DomainRecord) {
	t.Helper()
	want, okW := stored.Data.Get(name)
	got, okG := cached.Data.Get(name)
	if !okW || !okG {
		t.Fatalf("field %q missing (stored=%v cached=%v)", name, okW, okG)
	}
	if got.Kind != want.Kind {
		t.Fatalf("field %q kind = %d, want %d (precision drift)", name, got.Kind, want.Kind)
	}
	if !got.Equal(want) {
		t.Fatalf("field %q drift:\n got: %s\nwant: %s", name, mustJSON(got), mustJSON(want))
	}
}

// --- regression guard: tenant-scoped sweeps across processes ---

func TestInvalidateScopeSweepsSharedBackend(t *testing.T) {
	mem := NewMemoryDB()
	if _, err := mem.UpsertRecord(context.Background(), sampleRecord("shared")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sharedCache := newTestCache(t)
	inner := &countingStore{StateStore: mem}
	nodeA, err := NewCachedStateStore(inner, sharedCache, CachedStateStoreOptions{})
	if err != nil {
		t.Fatalf("nodeA: %v", err)
	}
	nodeB, err := NewCachedStateStore(inner, sharedCache, CachedStateStoreOptions{})
	if err != nil {
		t.Fatalf("nodeB: %v", err)
	}
	ctx := context.Background()

	if _, _, err := nodeA.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("warm A: %v", err)
	}
	if _, _, err := nodeB.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("warm B: %v", err)
	}
	if got := inner.gets.Load(); got != 1 {
		t.Fatalf("shared warm reads should reach inner once, got %d", got)
	}

	if err := nodeB.InvalidateScope(ctx, testDomain, testCollection, testOrg); err != nil {
		t.Fatalf("invalidate scope: %v", err)
	}
	if _, _, err := nodeB.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil {
		t.Fatalf("post-sweep read B: %v", err)
	}
	if got := inner.gets.Load(); got != 2 {
		t.Fatalf("post-sweep read must reach inner, gets=%d", got)
	}
	if err := nodeA.InvalidateOrganization(ctx, testOrg); err != nil {
		t.Fatalf("invalidate org: %v", err)
	}
}

// --- capability delegation + unwrap helper ---

func TestDelegationAndCapabilities(t *testing.T) {
	mem := NewMemoryDB()
	s := newWrapped(t, mem, CachedStateStoreOptions{})
	ctx := context.Background()

	if err := s.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Exec delegate: %v", err)
	}
	if row := s.QueryRow(ctx, "SELECT 1"); row.Scan() == nil {
		t.Fatal("memory row scan must surface its sentinel error")
	}
	rows, err := s.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("Query delegate: %v", err)
	}
	rows.Close()
	if s.Stats().TotalConns != 1 {
		t.Fatalf("Stats delegate TotalConns = %d, want 1", s.Stats().TotalConns)
	}
	if _, err := s.BeginTx(ctx); err == nil {
		t.Fatal("MemoryDB has no TxBeginner; wrapper must say so")
	}
	list, err := s.ListRecords(ctx, testDomain, testCollection, testOrg, RecordQuery{})
	if err != nil || len(list) != 0 {
		t.Fatalf("ListRecords passthrough: len=%d err=%v", len(list), err)
	}
	s.Close()
}

func TestAsPostgresDBUnwrapChain(t *testing.T) {
	pg, ok := AsPostgresDB(&PostgresDB{})
	if !ok || pg == nil {
		t.Fatal("direct PostgresDB must resolve")
	}
	if resolved, ok := AsPostgresDB(NewMemoryDB()); ok || resolved != nil {
		t.Fatal("MemoryDB must not claim PostgresDB")
	}

	target := &PostgresDB{}
	nested := StateStore(target)
	for i := range 3 {
		wrapped, err := NewCachedStateStore(nested, newTestCache(t), CachedStateStoreOptions{})
		if err != nil {
			t.Fatalf("layer %d: %v", i, err)
		}
		nested = wrapped
	}
	resolved, ok := AsPostgresDB(nested)
	if !ok || resolved != target {
		t.Fatal("AsPostgresDB must unwrap layered caches to concrete PostgresDB")
	}

	current := StateStore(target)
	for i := range 12 { // exceeds maxUnwrapDepth (8)
		next, err := NewCachedStateStore(current, newTestCache(t), CachedStateStoreOptions{})
		if err != nil {
			t.Fatalf("deep layer %d: %v", i, err)
		}
		current = next
	}
	if deep, ok := AsPostgresDB(current); ok || deep != nil {
		t.Fatal("chains past max depth must stop safely")
	}
}

// --- concurrency (CI runs this file with -race) ---

func TestConcurrentMixedTraffic(t *testing.T) {
	inner := &countingStore{StateStore: NewMemoryDB()}
	s := newWrapped(t, inner, CachedStateStoreOptions{})
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 40 {
				id := fmt.Sprintf("%s-%d", testRecord, g%2)
				switch i % 3 {
				case 0:
					rec := sampleRecord(fmt.Sprintf("g%d-i%d", g, i))
					rec.RecordID = id
					if _, err := s.UpsertRecord(ctx, rec); err != nil {
						t.Errorf("upsert: %v", err)
						return
					}
				case 1:
					if _, _, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, id); err != nil {
						t.Errorf("get: %v", err)
						return
					}
				default:
					if _, _, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, "never"); err != nil {
						t.Errorf("negative get: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// --- evidence: allocation monitoring ---

// TestAllocationBudgetCachedHit pins the warm-hit allocation cost so cache
// regressions surface in CI instead of production RSS graphs. Calibrated
// against BenchmarkCachedGetHit; raise only with benchmark evidence.
func TestAllocationBudgetCachedHit(t *testing.T) {
	s := newWrapped(t, NewMemoryDB(), CachedStateStoreOptions{})
	ctx := context.Background()

	if _, err := s.UpsertRecord(ctx, sampleRecord("alloc")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	warm, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
	if err != nil || !found || titleOf(t, warm) != "alloc" {
		t.Fatalf("warm: found=%v err=%v", found, err)
	}

	failed := false
	avg := testing.AllocsPerRun(200, func() {
		rec, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord)
		if err != nil || !found || titleOf(t, rec) != "alloc" {
			failed = true
		}
	})
	if failed {
		t.Fatal("allocation probe read failed")
	}
	const budget = 24.0
	if avg > budget {
		t.Fatalf("cached hit allocs/op = %.1f, budget %.0f (profile before raising)", avg, budget)
	}
	t.Logf("cached hit allocations: %.1f allocs/op", avg)
}

func BenchmarkCachedGetHit(b *testing.B) {
	backend := cache.NewMemoryBackend()
	defer backend.Close()
	s, err := NewCachedStateStore(NewMemoryDB(), cache.New(cache.Config{Backend: backend}), CachedStateStoreOptions{})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.UpsertRecord(ctx, sampleRecord("bench")); err != nil {
		b.Fatal(err)
	}
	if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || !found {
		b.Fatalf("warm: found=%v err=%v", found, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || !found {
			b.Fatalf("hit: found=%v err=%v", found, err)
		}
	}
}

func BenchmarkCachedReadThroughMiss(b *testing.B) {
	mem := NewMemoryDB()
	backend := cache.NewMemoryBackend()
	defer backend.Close()
	s, err := NewCachedStateStore(mem, cache.New(cache.Config{Backend: backend}), CachedStateStoreOptions{})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	base := sampleRecord("bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base.RecordID = fmt.Sprintf("miss-%d", i)
		if _, err := mem.UpsertRecord(ctx, base); err != nil {
			b.Fatalf("seed: %v", err)
		}
		if _, found, err := s.GetRecord(ctx, testDomain, testCollection, testOrg, base.RecordID); err != nil || !found {
			b.Fatalf("read-through: found=%v err=%v", found, err)
		}
	}
}

func BenchmarkUncachedGet(b *testing.B) {
	mem := NewMemoryDB()
	ctx := context.Background()
	if _, err := mem.UpsertRecord(ctx, sampleRecord("bench")); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, found, err := mem.GetRecord(ctx, testDomain, testCollection, testOrg, testRecord); err != nil || !found {
			b.Fatalf("read: found=%v err=%v", found, err)
		}
	}
}

func BenchmarkEnvelopeEncodeDecode(b *testing.B) {
	now := time.Now().UTC().Truncate(time.Second)
	rec := DomainRecord{
		Domain:         testDomain,
		Collection:     testCollection,
		OrganizationID: testOrg,
		RecordID:       testRecord,
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
		Data: RecordDataFromPairs(
			RecordField{Name: "amount_minor", Value: IntValue(123456)},
			RecordField{Name: "payload", Value: RawValue([]byte(`{"n":9007199254740993,"nested":{"deep":[1,true,null]}}`))},
		),
	}
	envelope := newEnvelope(rec, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw, err := json.Marshal(envelope)
		if err != nil {
			b.Fatal(err)
		}
		var decoded cachedRecordEnvelope
		if err := json.Unmarshal(raw, &decoded); err != nil {
			b.Fatal(err)
		}
		if len(decoded.Data) == 0 {
			b.Fatal("record data lost in round trip")
		}
	}
}

// --- helpers ---

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
