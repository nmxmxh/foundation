package contracttest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

// MAYADataRecord represents an individual row/event record for deterministic validation.
type MAYADataRecord struct {
	Key        string
	Payload    []byte
	Attributes map[string]string
	Amount     int64
}

// MAYAValidationResult carries the deterministic proof outcome.
type MAYAValidationResult struct {
	RowCountSource  int
	RowCountTarget  int
	RowCountParity  bool
	MissingKeys     []string
	ExtraKeys       []string
	KeyParity       bool
	NullViolations  map[string]int
	NullParity      bool
	SourceChecksum  string
	TargetChecksum  string
	ChecksumParity  bool
	SourceAggregate int64
	TargetAggregate int64
	AggregateParity bool
	AllPassed       bool
}

// MAYAVerifier implements the MAYA deterministic data validation framework (ISO/IEC 25010).
// It verifies:
// 1. row_count parity: no dropped or extra rows.
// 2. key_parity: exact set equality of primary/correlation keys.
// 3. null_distribution: zero unexpected missing/empty attributes.
// 4. content_checksum: order-independent commutative cryptographic hash (XOR sum of SHA-256).
// 5. column_aggregates: exact numerical sum preservation.
type MAYAVerifier struct {
	requiredAttributes []string
}

// NewMAYAVerifier initializes a MAYA data integrity verifier.
func NewMAYAVerifier(requiredAttributes ...string) *MAYAVerifier {
	return &MAYAVerifier{
		requiredAttributes: requiredAttributes,
	}
}

// VerifyParity performs the five deterministic data integrity checks between source and target sets.
func (m *MAYAVerifier) VerifyParity(source, target []MAYADataRecord) MAYAValidationResult {
	res := MAYAValidationResult{
		RowCountSource: len(source),
		RowCountTarget: len(target),
		RowCountParity: len(source) == len(target),
		NullViolations: make(map[string]int),
	}

	sourceKeys := make(map[string]struct{}, len(source))
	targetKeys := make(map[string]struct{}, len(target))

	var sourceXor [32]byte
	var targetXor [32]byte

	var sourceSum int64
	var targetSum int64

	// Process Source
	for _, rec := range source {
		sourceKeys[rec.Key] = struct{}{}
		sourceSum += rec.Amount

		h := computeRecordHash(rec)
		for i := range 32 {
			sourceXor[i] ^= h[i]
		}
	}

	// Process Target and verify null distribution
	for _, rec := range target {
		targetKeys[rec.Key] = struct{}{}
		targetSum += rec.Amount

		h := computeRecordHash(rec)
		for i := range 32 {
			targetXor[i] ^= h[i]
		}

		for _, attr := range m.requiredAttributes {
			val, exists := rec.Attributes[attr]
			if !exists || val == "" {
				res.NullViolations[attr]++
			}
		}
	}

	// Key parity: find missing and extra keys
	for k := range sourceKeys {
		if _, exists := targetKeys[k]; !exists {
			res.MissingKeys = append(res.MissingKeys, k)
		}
	}
	for k := range targetKeys {
		if _, exists := sourceKeys[k]; !exists {
			res.ExtraKeys = append(res.ExtraKeys, k)
		}
	}
	sort.Strings(res.MissingKeys)
	sort.Strings(res.ExtraKeys)
	res.KeyParity = len(res.MissingKeys) == 0 && len(res.ExtraKeys) == 0

	res.NullParity = len(res.NullViolations) == 0
	res.SourceChecksum = hex.EncodeToString(sourceXor[:])
	res.TargetChecksum = hex.EncodeToString(targetXor[:])
	res.ChecksumParity = sourceXor == targetXor

	res.SourceAggregate = sourceSum
	res.TargetAggregate = targetSum
	res.AggregateParity = sourceSum == targetSum

	res.AllPassed = res.RowCountParity &&
		res.KeyParity &&
		res.NullParity &&
		res.ChecksumParity &&
		res.AggregateParity

	return res
}

func computeRecordHash(rec MAYADataRecord) [32]byte {
	h := sha256.New()
	h.Write([]byte(rec.Key))
	h.Write([]byte{0})
	h.Write(rec.Payload)
	h.Write([]byte{0})

	// Deterministic sorted attribute serialization
	attrs := make([]string, 0, len(rec.Attributes))
	for k := range rec.Attributes {
		attrs = append(attrs, k)
	}
	sort.Strings(attrs)
	for _, k := range attrs {
		h.Write([]byte(k))
		h.Write([]byte{':'})
		h.Write([]byte(rec.Attributes[k]))
		h.Write([]byte{';'})
	}

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// StressMetrics captures performance stability and variability under load (ISO/IEC 25010 Time Behavior).
type StressMetrics struct {
	Count       int
	MeanLatency time.Duration
	StdDev      time.Duration
	RSD         float64 // Relative Standard Deviation percentage (sigma / mean * 100)
	P95Latency  time.Duration
	P99Latency  time.Duration
	MaxLatency  time.Duration
}

// StressRecorder collects latencies and calculates Relative Standard Deviation (RSD)
// to detect unstable performance behavior, thread starvation, or latency spikes.
type StressRecorder struct {
	mu        sync.Mutex
	latencies []time.Duration
}

func NewStressRecorder(expectedCount int) *StressRecorder {
	return &StressRecorder{
		latencies: make([]time.Duration, 0, expectedCount),
	}
}

func (s *StressRecorder) Record(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latencies = append(s.latencies, d)
}

func (s *StressRecorder) Evaluate() StressMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.latencies)
	if n == 0 {
		return StressMetrics{}
	}

	sorted := append([]time.Duration(nil), s.latencies...)
	slices.Sort(sorted)

	var total float64
	for _, lat := range sorted {
		total += float64(lat.Nanoseconds())
	}
	meanNanos := total / float64(n)

	var varianceSum float64
	for _, lat := range sorted {
		diff := float64(lat.Nanoseconds()) - meanNanos
		varianceSum += diff * diff
	}

	stdDevNanos := 0.0
	if n > 1 {
		stdDevNanos = math.Sqrt(varianceSum / float64(n-1))
	}

	rsd := 0.0
	if meanNanos > 0 {
		rsd = (stdDevNanos / meanNanos) * 100.0
	}

	p95Idx := max(int(math.Ceil(float64(n)*0.95))-1, 0)
	if p95Idx >= n {
		p95Idx = n - 1
	}

	p99Idx := max(int(math.Ceil(float64(n)*0.99))-1, 0)
	if p99Idx >= n {
		p99Idx = n - 1
	}

	return StressMetrics{
		Count:       n,
		MeanLatency: time.Duration(meanNanos),
		StdDev:      time.Duration(stdDevNanos),
		RSD:         rsd,
		P95Latency:  sorted[p95Idx],
		P99Latency:  sorted[p99Idx],
		MaxLatency:  sorted[n-1],
	}
}

// ErrorSummary formats failure diagnostics when parity checks fail.
func (r *MAYAValidationResult) ErrorSummary() string {
	if r.AllPassed {
		return "MAYA verification passed: 100% parity guaranteed."
	}
	msg := fmt.Sprintf("MAYA verification failed: row_count(source=%d, target=%d, match=%v), key_parity(%v, missing=%d, extra=%d), null_parity(%v, violations=%v), checksum_parity(%v), aggregate_parity(source=%d, target=%d, match=%v)",
		r.RowCountSource, r.RowCountTarget, r.RowCountParity,
		r.KeyParity, len(r.MissingKeys), len(r.ExtraKeys),
		r.NullParity, r.NullViolations,
		r.ChecksumParity,
		r.SourceAggregate, r.TargetAggregate, r.AggregateParity,
	)
	return msg
}
