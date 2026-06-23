package hermes

import (
	"bytes"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

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

	// Diverge a value in the source: same id, different field content.
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

	// Add an extra source record absent from hermes -> count mismatch.
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
	// Same content, fields in a different order.
	reordered := database.RecordData{
		{Name: "qty", Value: database.IntValue(10)},
		{Name: "symbol", Value: database.StringValue("OVS")},
		{Name: "price", Value: database.FloatValue(3.5)},
	}
	// One value changed.
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

	// Malformed numeric text falls back to raw-text hashing rather than erroring.
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
