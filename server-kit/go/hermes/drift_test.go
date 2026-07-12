package hermes

import (
	"bytes"
	"context"
	"fmt"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"testing"
)

func TestStoreCheckDriftConsistentMerkle(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 6)
	store := newDriftStore(t, 16)
	if _, err := store.Rebuild(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 16, SampleSize: 3})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("drift report should be OK: %+v", report)
	}
	if report.SourceCount != 6 || report.HermesCount != 6 || report.SourceRoot == "" || report.SourceRoot != report.HermesRoot {
		t.Fatalf("unexpected report counts/root: %+v", report)
	}
	if len(report.Samples) != 3 {
		t.Fatalf("samples = %d, want 3", len(report.Samples))
	}
	for _, sample := range report.Samples {
		if !sample.Match || sample.SourceWitness.LeafHash == "" || len(sample.SourceWitness.Siblings) == 0 {
			t.Fatalf("bad sample witness: %+v", sample)
		}
	}
}

func TestStoreCheckDriftTreatsRawJSONSemantically(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	_, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_raw", map[string]any{
		"bucket":   1,
		"metadata": []byte(`{"trace_id":"abc","actor_id":"u1","labels":["a","b"]}`),
	}))
	if err != nil {
		t.Fatalf("source upsert failed: %v", err)
	}
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_raw", 1, map[string]any{
		"bucket":   1,
		"metadata": []byte(`{"actor_id":"u1","labels":["a","b"],"trace_id":"abc"}`),
	})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 1})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if !report.OK() {
		t.Fatalf("semantically equivalent raw JSON should not drift: %+v", report)
	}
}

func TestStoreCheckDriftDetectsHashMismatch(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	_, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 1, "value": "source"}))
	if err != nil {
		t.Fatalf("source upsert failed: %v", err)
	}
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_1", 1, map[string]any{"bucket": 1, "value": "hot"})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 4})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() || len(report.Mismatches) != 1 || report.Mismatches[0].Reason != "hash_mismatch" {
		t.Fatalf("expected one hash mismatch, got %+v", report)
	}
}

func TestStoreCheckDriftDetectsMissingHermesRecord(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 2)
	store := newDriftStore(t, 4)
	applyTestRecord(t, store, "drift_ticks", "org_1", "tick_0", 1, map[string]any{"bucket": 0, "value": "tick_0"})

	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 4, SampleSize: 4})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() {
		t.Fatalf("expected drift, got OK report: %+v", report)
	}
	if !hasDriftReason(report.Mismatches, "count_mismatch") || !hasDriftReason(report.Mismatches, "missing_hermes") {
		t.Fatalf("expected count and missing_hermes mismatch, got %+v", report.Mismatches)
	}
}

func TestStoreCheckDriftMarksBoundedTruncation(t *testing.T) {
	ctx := t.Context()
	source := database.NewMemoryDB()
	seedDriftSource(t, ctx, source, 3)
	store := newDriftStore(t, 2)
	if _, err := store.Rebuild(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}); err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	report, err := store.CheckDrift(ctx, "drift_ticks", source, Query{OrganizationID: "org_1"}, DriftOptions{MaxRecords: 2, SampleSize: 2})
	if err != nil {
		t.Fatalf("CheckDrift() error = %v", err)
	}
	if report.OK() || report.Complete || !report.Truncated || !hasDriftReason(report.Mismatches, "bounded_sample_truncated") {
		t.Fatalf("expected bounded truncation report, got %+v", report)
	}
}

func newDriftStore(t *testing.T, maxRecords int) *Store {
	t.Helper()
	return newTestStore(t, ProjectionSpec{
		Name:          "drift_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    maxRecords,
		MaxBytes:      1 << 20,
	})
}

func seedDriftSource(t *testing.T, ctx context.Context, source database.StateStore, count int) {
	t.Helper()
	for i := range count {
		id := fmt.Sprintf("tick_%d", i)
		if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", id, map[string]any{
			"bucket": i % 2,
			"value":  id,
		})); err != nil {
			t.Fatalf("source upsert[%d] failed: %v", i, err)
		}
	}
}

func hasDriftReason(mismatches []DriftMismatch, reason string) bool {
	for _, mismatch := range mismatches {
		if mismatch.Reason == reason {
			return true
		}
	}
	return false
}
func driftSpec() ProjectionSpec {
	return ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	}
}

// TestCheckDriftDetectsMatchAndDivergence is the black-box drift contract (TE-20
// regression class, TE-12 constraint check): when the hermes hot plane and the
// source-of-truth store hold the same records the report is OK with equal Merkle
// roots; a divergent value yields a per-record mismatch and unequal roots; a
// missing source record yields a count mismatch. This exercises the full drift
// path — canonical hashing of both DomainRecord and RecordView inputs, the Merkle
// tree, and set comparison — through one public entry point.
func TestCheckDriftDetectsMatchAndDivergence(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t, driftSpec())
	source := database.NewMemoryDB()

	records := map[string]map[string]any{
		"tick_1": {"symbol": "OVS"},
		"tick_2": {"symbol": "ABC"},
		"tick_3": {"symbol": "XYZ"},
	}
	version := uint64(0)
	for id, data := range records {
		version++
		applyTestRecord(t, store, "signals", "org_1", id, version, data)
		if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", id, data)); err != nil {
			t.Fatalf("source UpsertRecord(%s) err=%v", id, err)
		}
	}

	q := Query{OrganizationID: "org_1"}

	report, err := store.CheckDrift(ctx, "signals", source, q, DriftOptions{})
	if err != nil {
		t.Fatalf("CheckDrift() err=%v", err)
	}
	if !report.OK() {
		t.Fatalf("identical stores reported drift: roots %s/%s mismatches=%+v",
			report.SourceRoot, report.HermesRoot, report.Mismatches)
	}

	if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"symbol": "CHANGED"})); err != nil {
		t.Fatalf("source diverge err=%v", err)
	}
	report, err = store.CheckDrift(ctx, "signals", source, q, DriftOptions{})
	if err != nil {
		t.Fatalf("CheckDrift(diverged) err=%v", err)
	}
	if report.OK() {
		t.Fatal("diverged value not detected as drift")
	}
	if report.SourceRoot == report.HermesRoot {
		t.Fatal("diverged value should change the source Merkle root")
	}
	foundRecordMismatch := false
	for _, m := range report.Mismatches {
		if m.RecordID == "tick_2" {
			foundRecordMismatch = true
		}
	}
	if !foundRecordMismatch {
		t.Fatalf("expected a mismatch for tick_2, got %+v", report.Mismatches)
	}

	if _, err := source.UpsertRecord(ctx, testRecord("signals", "ticks", "org_1", "tick_extra", map[string]any{"symbol": "NEW"})); err != nil {
		t.Fatalf("source add err=%v", err)
	}
	report, err = store.CheckDrift(ctx, "signals", source, q, DriftOptions{})
	if err != nil {
		t.Fatalf("CheckDrift(count) err=%v", err)
	}
	if report.SourceCount != 4 || report.HermesCount != 3 {
		t.Fatalf("counts = source %d / hermes %d, want 4/3", report.SourceCount, report.HermesCount)
	}
	sawCountMismatch := false
	for _, m := range report.Mismatches {
		if m.Reason == "count_mismatch" {
			sawCountMismatch = true
		}
	}
	if !sawCountMismatch {
		t.Fatalf("expected count_mismatch, got %+v", report.Mismatches)
	}
}

// TestCheckDriftRejectsBadInput covers the input-guard boundaries of CheckDrift:
// a nil source and a query without an organization scope are rejected.
func TestCheckDriftRejectsBadInput(t *testing.T) {
	store := newTestStore(t, driftSpec())
	if _, err := store.CheckDrift(t.Context(), "signals", nil, Query{OrganizationID: "org_1"}, DriftOptions{}); err == nil {
		t.Fatal("nil source should be rejected")
	}
	if _, err := store.CheckDrift(t.Context(), "signals", database.NewMemoryDB(), Query{}, DriftOptions{}); err == nil {
		t.Fatal("missing organization scope should be rejected")
	}
}

// TestCanonicalRecordDataInvariants pins the drift fingerprint invariants
// (TE-31): the canonical byte encoding is deterministic, insensitive to field
// order (Normalize sorts), and sensitive to any value change. These three
// properties are what let the Merkle root act as a faithful drift detector.
func TestCanonicalRecordDataInvariants(t *testing.T) {
	a := database.RecordData{
		{Name: "symbol", Value: database.StringValue("OVS")},
		{Name: "price", Value: database.FloatValue(3.5)},
		{Name: "qty", Value: database.IntValue(10)},
	}

	reordered := database.RecordData{
		{Name: "qty", Value: database.IntValue(10)},
		{Name: "symbol", Value: database.StringValue("OVS")},
		{Name: "price", Value: database.FloatValue(3.5)},
	}

	changed := database.RecordData{
		{Name: "symbol", Value: database.StringValue("OVS")},
		{Name: "price", Value: database.FloatValue(9.9)},
		{Name: "qty", Value: database.IntValue(10)},
	}

	enc := func(d database.RecordData) []byte { return appendCanonicalRecordData(nil, d) }

	if !bytes.Equal(enc(a), enc(a)) {
		t.Fatal("canonical encoding is not deterministic")
	}
	if !bytes.Equal(enc(a), enc(reordered)) {
		t.Fatal("canonical encoding must be insensitive to field order")
	}
	if bytes.Equal(enc(a), enc(changed)) {
		t.Fatal("canonical encoding must change when a value changes")
	}
}

// TestCanonicalRecordValueKinds covers each scalar kind's canonical encoding,
// including the malformed-number fallback (a non-numeric int/uint/float text is
// hashed as its raw text rather than panicking or silently colliding).
func TestCanonicalRecordValueKinds(t *testing.T) {
	enc := func(v database.RecordValue) []byte { return appendCanonicalRecordValue(nil, v) }

	kinds := []database.RecordValue{
		{Kind: database.RecordValueNull},
		database.StringValue("s"),
		database.BoolValue(true),
		database.IntValue(-7),
		database.UintValue(7),
		database.FloatValue(1.25),
		database.RawValue([]byte(`{"a":1}`)),
	}
	seen := map[string]bool{}
	for _, v := range kinds {
		out := enc(v)
		if len(out) == 0 {
			t.Fatalf("empty canonical encoding for %+v", v)
		}
		seen[string(out)] = true
	}
	if len(seen) != len(kinds) {
		t.Fatalf("distinct kinds collided: %d encodings for %d kinds", len(seen), len(kinds))
	}

	malformed := []database.RecordValue{
		{Kind: database.RecordValueInt, Text: "not-int"},
		{Kind: database.RecordValueUint, Text: "not-uint"},
		{Kind: database.RecordValueFloat, Text: "not-float"},
	}
	for _, v := range malformed {
		if len(enc(v)) == 0 {
			t.Fatalf("malformed %+v produced empty encoding", v)
		}
	}
}

// TestCanonicalRawJSONForDrift covers JSON canonicalization: semantically equal
// JSON that differs in whitespace and key order canonicalizes identically, while
// invalid JSON passes through as its trimmed text.
func TestCanonicalRawJSONForDrift(t *testing.T) {
	a := canonicalRawJSONForDrift([]byte(`{"b":1,"a":2}`))
	b := canonicalRawJSONForDrift([]byte(`  { "a": 2, "b": 1 }  `))
	if a != b {
		t.Fatalf("equal JSON canonicalized differently: %q vs %q", a, b)
	}
	if got := canonicalRawJSONForDrift([]byte(`not json`)); got != "not json" {
		t.Fatalf("invalid JSON passthrough = %q, want \"not json\"", got)
	}
}
