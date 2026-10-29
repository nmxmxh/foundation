package math_lab

import (
	"fmt"
	"testing"
)

// Simulated Canonical Foundation Envelope (Tier 1 Metadata)
type MockEnvelopeMetadata struct {
	CorrelationID      string
	UserID             string
	OrganizationID     string
	DataClassification string // "public", "internal", "confidential", "restricted"
	Tags               []string
	Attributes         map[string]string
}

func encodeClassification(c string) uint8 {
	switch c {
	case "public":
		return 1 // use 1-4 so 0 is strictly empty/absent
	case "internal":
		return 2
	case "confidential":
		return 3
	case "restricted":
		return 4
	default:
		return 4 // fail-closed
	}
}

func TestMaplet_OneSidedInvariant(t *testing.T) {
	const n = 10000
	m := NewMaplet(n)

	// Keep ground truth
	truth := make(map[string]uint8, n)
	keys := make([]string, n)

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("tenant_%d:record_%d", i%10, i)
		val := uint8((i % 15) + 1)
		truth[key] = val
		keys[i] = key

		err := m.Insert(key, val)
		if err != nil {
			t.Fatalf("failed to insert key %s at index %d: %v", key, i, err)
		}
	}

	// Invariant Check 1: Zero False Negatives and Exact Value Equality
	for i, key := range keys {
		val, found := m.Lookup(key)
		if !found {
			t.Fatalf("one-sided error violation: key %s (index %d) not found in maplet", key, i)
		}
		expected := truth[key]
		if val != expected {
			t.Fatalf("one-sided error violation: key %s expected value %d, got %d", key, expected, val)
		}
	}
}

func TestMaplet_FalsePositiveRate(t *testing.T) {
	const n = 10000
	const probeCount = 100000
	m := NewMaplet(n)

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("inserted_key_%d", i)
		val := uint8((i % 15) + 1)
		if err := m.Insert(key, val); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	falsePositives := 0
	for i := 0; i < probeCount; i++ {
		absentKey := fmt.Sprintf("absent_key_%d", i)
		_, found := m.Lookup(absentKey)
		if found {
			falsePositives++
		}
	}

	fpr := float64(falsePositives) / float64(probeCount)
	t.Logf("Maplet FPR over %d probes: %.4f (%d false positives). Bits/key: %.2f",
		probeCount, fpr, falsePositives, m.BitsPerKey())

	// Theoretical bound with 12-bit fingerprint across 8 candidate slots is ~8/4096 = 0.195%
	// We assert FPR is strictly bounded below 0.5% (0.005)
	if fpr > 0.005 {
		t.Fatalf("measured FPR %.4f exceeds bound 0.005", fpr)
	}
}

func TestMaplet_TwoTierMetadataLifecycle(t *testing.T) {
	const count = 5000
	m := NewMaplet(count)

	// Tier 1: Canonical Durable Storage
	canonicalStore := make(map[string]*MockEnvelopeMetadata, count)

	classifications := []string{"public", "internal", "confidential", "restricted"}

	for i := 0; i < count; i++ {
		key := fmt.Sprintf("scope_record_%d", i)
		class := classifications[i%4]
		val := encodeClassification(class)

		canonicalStore[key] = &MockEnvelopeMetadata{
			CorrelationID:      fmt.Sprintf("corr_%d", i),
			UserID:             fmt.Sprintf("usr_%d", i),
			OrganizationID:     "org_ovasabi",
			DataClassification: class,
			Tags:               []string{"env:prod", "tier:gold"},
			Attributes:         map[string]string{"region": "us-east"},
		}

		if err := m.Insert(key, val); err != nil {
			t.Fatalf("insert failed for %s: %v", key, err)
		}
	}

	// Policy Rule: Fast-Path Filter allowing ONLY "public" (value 1)
	// Any other classification or absent key must be rejected.
	tier1Accesses := 0
	fastPathRejections := 0
	permittedCount := 0

	// Test 10,000 incoming requests (5,000 known, 5,000 unknown)
	for i := 0; i < 10000; i++ {
		var candidateKey string
		if i < count {
			candidateKey = fmt.Sprintf("scope_record_%d", i)
		} else {
			candidateKey = fmt.Sprintf("unregistered_record_%d", i)
		}

		// Fast Path: Query Tier 2 Maplet Micro-Value
		microVal, found := m.Lookup(candidateKey)
		if !found || microVal != 1 {
			// Instant fast-path drop: ZERO allocations, ZERO Tier 1 lookups!
			fastPathRejections++
			continue
		}

		// If Maplet says 1 (Public), verify against Tier 1 Canonical Truth
		tier1Accesses++
		canon, exists := canonicalStore[candidateKey]
		if !exists || canon.DataClassification != "public" {
			// Caught a false positive in the verification fallback!
			fastPathRejections++
			continue
		}

		permittedCount++
	}

	expectedPermitted := count / 4 // 1250 "public" records
	if permittedCount != expectedPermitted {
		t.Fatalf("expected %d permitted records, got %d", expectedPermitted, permittedCount)
	}

	t.Logf("Fast-path rejections: %d / 10000 (%.1f%%). Tier 1 deep lookups avoided: %d",
		fastPathRejections, float64(fastPathRejections)/100.0, 10000-tier1Accesses)
}

func TestMaplet_ZeroAllocationsOnLookup(t *testing.T) {
	m := NewMaplet(1000)
	for i := 0; i < 800; i++ {
		_ = m.Insert(fmt.Sprintf("key_%d", i), uint8((i%15)+1))
	}

	lookupKey := "key_42"
	allocs := testing.AllocsPerRun(1000, func() {
		val, ok := m.Lookup(lookupKey)
		if !ok || val != uint8((42%15)+1) {
			t.Fatalf("unexpected lookup failure")
		}
	})

	if allocs > 0 {
		t.Fatalf("expected 0 allocs/op for Maplet.Lookup, got %.2f", allocs)
	}
}

func TestMaplet_MemoryFootprintComparison(t *testing.T) {
	const n = 50000

	// 1. Maplet
	m := NewMaplet(n)
	for i := 0; i < n; i++ {
		_ = m.Insert(fmt.Sprintf("record_%d", i), uint8((i%15)+1))
	}
	mapletBytes := m.MemoryBytes()

	// 2. Go map[string]int
	// Standard pointer-map overhead in 64-bit Go is approx 48-64 bytes per entry
	// (8-byte pointer, 8-byte hash, 16-byte string header, 8-byte int, + string bytes)
	// A 15-byte string key + string header (16B) + map bucket slot (~24B) ≈ 55 bytes/key.
	approxGoMapBytes := n * 55

	// 3. EnvelopeMetadata
	// EnvelopeMetadata struct itself is ~280 bytes on stack/heap + string headers + tags slice
	approxEnvelopeBytes := n * 400

	t.Logf("=== MEMORY FOOTPRINT COMPARISON FOR %d KEYS ===", n)
	t.Logf("1. Two-Tier Maplet Micro-Value: %d KB (%.2f bytes/key, %.2f bits/key)",
		mapletBytes/1024, float64(mapletBytes)/float64(n), float64(mapletBytes*8)/float64(n))
	t.Logf("2. Foundation Go map[string]int (Hermes keyToSlot): ~%d KB (~%.2f bytes/key)",
		approxGoMapBytes/1024, float64(approxGoMapBytes)/float64(n))
	t.Logf("3. Foundation Full EnvelopeMetadata: ~%d KB (~%.2f bytes/key)",
		approxEnvelopeBytes/1024, float64(approxEnvelopeBytes)/float64(n))
	t.Logf("Maplet vs Go Map memory savings: %.1fx smaller", float64(approxGoMapBytes)/float64(mapletBytes))
	t.Logf("Maplet vs EnvelopeMetadata memory savings: %.1fx smaller", float64(approxEnvelopeBytes)/float64(mapletBytes))
}

// ---------------------------------------------------------------------------
// Benchmarks: Maplet vs Go Map vs Full EnvelopeMetadata
// ---------------------------------------------------------------------------

func BenchmarkMetadataLookup_Maplet(b *testing.B) {
	const n = 10000
	m := NewMaplet(n)
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("bench_key_%d", i)
		_ = m.Insert(keys[i], uint8((i%15)+1))
	}

	b.ResetTimer()
	b.ReportAllocs()
	var i int
	for b.Loop() {
		k := keys[i&(n-1)]
		val, _ := m.Lookup(k)
		_ = val
		i++
	}
}

func BenchmarkMetadataLookup_GoMapInt(b *testing.B) {
	const n = 10000
	goMap := make(map[string]int, n)
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("bench_key_%d", i)
		goMap[keys[i]] = (i % 15) + 1
	}

	b.ResetTimer()
	b.ReportAllocs()
	var i int
	for b.Loop() {
		k := keys[i&(n-1)]
		val := goMap[k]
		_ = val
		i++
	}
}

func BenchmarkMetadataLookup_EnvelopeMetadata(b *testing.B) {
	const n = 10000
	canonMap := make(map[string]*MockEnvelopeMetadata, n)
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("bench_key_%d", i)
		canonMap[keys[i]] = &MockEnvelopeMetadata{
			CorrelationID:      "corr_12345",
			UserID:             "usr_67890",
			OrganizationID:     "org_ovasabi",
			DataClassification: "confidential",
			Tags:               []string{"tag1", "tag2"},
			Attributes:         map[string]string{"attr": "val"},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	var i int
	for b.Loop() {
		k := keys[i&(n-1)]
		env := canonMap[k]
		_ = env.DataClassification
		i++
	}
}
