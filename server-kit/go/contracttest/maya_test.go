package contracttest

import (
	"testing"
	"time"
)

func TestMAYAVerifier_AllParityPasses(t *testing.T) {
	source := []MAYADataRecord{
		{Key: "k1", Payload: []byte("payload-1"), Attributes: map[string]string{"tenant_id": "t1"}, Amount: 100},
		{Key: "k2", Payload: []byte("payload-2"), Attributes: map[string]string{"tenant_id": "t1"}, Amount: 250},
		{Key: "k3", Payload: []byte("payload-3"), Attributes: map[string]string{"tenant_id": "t2"}, Amount: 50},
	}
	// Target has same elements in different order
	target := []MAYADataRecord{
		{Key: "k3", Payload: []byte("payload-3"), Attributes: map[string]string{"tenant_id": "t2"}, Amount: 50},
		{Key: "k1", Payload: []byte("payload-1"), Attributes: map[string]string{"tenant_id": "t1"}, Amount: 100},
		{Key: "k2", Payload: []byte("payload-2"), Attributes: map[string]string{"tenant_id": "t1"}, Amount: 250},
	}

	verifier := NewMAYAVerifier("tenant_id")
	res := verifier.VerifyParity(source, target)

	if !res.AllPassed {
		t.Fatalf("expected all parity checks to pass: %s", res.ErrorSummary())
	}
	if res.SourceChecksum != res.TargetChecksum {
		t.Fatalf("checksums do not match: source=%s, target=%s", res.SourceChecksum, res.TargetChecksum)
	}
	if res.SourceAggregate != 400 || res.TargetAggregate != 400 {
		t.Fatalf("unexpected aggregates: source=%d, target=%d", res.SourceAggregate, res.TargetAggregate)
	}
}

func TestMAYAVerifier_DetectsDroppedRow(t *testing.T) {
	source := []MAYADataRecord{
		{Key: "k1", Payload: []byte("data"), Amount: 10},
		{Key: "k2", Payload: []byte("dropped-data"), Amount: 20},
	}
	target := []MAYADataRecord{
		{Key: "k1", Payload: []byte("data"), Amount: 10},
	}

	verifier := NewMAYAVerifier()
	res := verifier.VerifyParity(source, target)

	if res.AllPassed {
		t.Fatal("expected failure on dropped row")
	}
	if res.RowCountParity {
		t.Fatal("row count parity should fail")
	}
	if len(res.MissingKeys) != 1 || res.MissingKeys[0] != "k2" {
		t.Fatalf("expected missing key k2, got %v", res.MissingKeys)
	}
	if res.ChecksumParity {
		t.Fatal("checksum parity should fail on dropped row")
	}
	if res.AggregateParity {
		t.Fatal("aggregate parity should fail on dropped row")
	}
}

func TestMAYAVerifier_DetectsExtraRow(t *testing.T) {
	source := []MAYADataRecord{
		{Key: "k1", Payload: []byte("data"), Amount: 10},
	}
	target := []MAYADataRecord{
		{Key: "k1", Payload: []byte("data"), Amount: 10},
		{Key: "phantom", Payload: []byte("ghost"), Amount: 5},
	}

	verifier := NewMAYAVerifier()
	res := verifier.VerifyParity(source, target)

	if res.AllPassed || res.KeyParity {
		t.Fatal("expected failure on extra row")
	}
	if len(res.ExtraKeys) != 1 || res.ExtraKeys[0] != "phantom" {
		t.Fatalf("expected extra key phantom, got %v", res.ExtraKeys)
	}
}

func TestMAYAVerifier_DetectsCorruptedPayloadByte(t *testing.T) {
	source := []MAYADataRecord{
		{Key: "k1", Payload: []byte("authentic"), Amount: 10},
	}
	target := []MAYADataRecord{
		{Key: "k1", Payload: []byte("corrupted"), Amount: 10},
	}

	verifier := NewMAYAVerifier()
	res := verifier.VerifyParity(source, target)

	if res.ChecksumParity || res.AllPassed {
		t.Fatal("expected checksum failure on corrupted payload")
	}
	if !res.RowCountParity || !res.KeyParity {
		t.Fatal("row count and key parity should still match")
	}
}

func TestMAYAVerifier_DetectsNullViolation(t *testing.T) {
	source := []MAYADataRecord{
		{Key: "k1", Attributes: map[string]string{"tenant_id": "org_1"}},
	}
	target := []MAYADataRecord{
		{Key: "k1", Attributes: map[string]string{"tenant_id": ""}}, // missing tenant
	}

	verifier := NewMAYAVerifier("tenant_id")
	res := verifier.VerifyParity(source, target)

	if res.NullParity || res.AllPassed {
		t.Fatal("expected null parity failure")
	}
	if count := res.NullViolations["tenant_id"]; count != 1 {
		t.Fatalf("expected 1 null violation, got %d", count)
	}
}

func TestStressRecorder_CalculatesRSD(t *testing.T) {
	recorder := NewStressRecorder(10)
	if metrics := recorder.Evaluate(); metrics.Count != 0 {
		t.Fatalf("expected empty metrics, got %+v", metrics)
	}

	// Record steady latencies
	latencies := []time.Duration{
		10 * time.Millisecond,
		11 * time.Millisecond,
		10 * time.Millisecond,
		12 * time.Millisecond,
		10 * time.Millisecond,
		11 * time.Millisecond,
	}
	for _, l := range latencies {
		recorder.Record(l)
	}

	metrics := recorder.Evaluate()
	if metrics.Count != 6 {
		t.Fatalf("expected 6 samples, got %d", metrics.Count)
	}
	if metrics.RSD <= 0.0 || metrics.RSD > 20.0 {
		t.Fatalf("expected low RSD under steady latency, got %f%%", metrics.RSD)
	}
	if metrics.P95Latency == 0 || metrics.MaxLatency == 0 {
		t.Fatal("expected non-zero percentiles")
	}

	// Single item recorder
	single := NewStressRecorder(1)
	single.Record(5 * time.Millisecond)
	sm := single.Evaluate()
	if sm.Count != 1 || sm.MeanLatency != 5*time.Millisecond {
		t.Fatalf("unexpected single metrics: %+v", sm)
	}

	// Test ErrorSummary formatting
	passSummary := (&MAYAValidationResult{AllPassed: true}).ErrorSummary()
	if passSummary == "" {
		t.Fatal("empty pass summary")
	}
	failSummary := (&MAYAValidationResult{AllPassed: false}).ErrorSummary()
	if failSummary == "" {
		t.Fatal("empty fail summary")
	}
}
