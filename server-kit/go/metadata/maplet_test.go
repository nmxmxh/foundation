package metadata

import (
	"fmt"
	"testing"
)

func TestMetadataEnumsAndProjection(t *testing.T) {
	// 1. Classification Enums
	if EncodeClassification("public") != ClassificationPublic {
		t.Fatalf("expected ClassificationPublic")
	}
	if EncodeClassification("internal") != ClassificationInternal {
		t.Fatalf("expected ClassificationInternal")
	}
	if EncodeClassification("confidential") != ClassificationConfidential {
		t.Fatalf("expected ClassificationConfidential")
	}
	if EncodeClassification("restricted") != ClassificationRestricted {
		t.Fatalf("expected ClassificationRestricted")
	}
	if EncodeClassification("unknown") != ClassificationRestricted {
		t.Fatalf("expected fail-closed ClassificationRestricted for unknown")
	}

	// 2. Retention Enums
	if EncodeRetention("ephemeral") != RetentionEphemeral {
		t.Fatalf("expected RetentionEphemeral")
	}
	if EncodeRetention("30d") != Retention30Days {
		t.Fatalf("expected Retention30Days")
	}
	if EncodeRetention("1y") != Retention1Year {
		t.Fatalf("expected Retention1Year")
	}
	if EncodeRetention("7y") != Retention7Years {
		t.Fatalf("expected Retention7Years")
	}
	if EncodeRetention("permanent") != RetentionPermanent {
		t.Fatalf("expected RetentionPermanent")
	}
	if EncodeRetention("invalid") != RetentionPermanent {
		t.Fatalf("expected fail-closed RetentionPermanent for invalid")
	}

	// 3. Projections from EnvelopeMetadata
	if ProjectMetadataClassification(nil) != ClassificationRestricted {
		t.Fatalf("expected nil metadata to project to restricted")
	}
	if ProjectMetadataRetention(nil) != RetentionPermanent {
		t.Fatalf("expected nil metadata to project to permanent")
	}

	env := New()
	if ProjectMetadataClassification(&env) != ClassificationRestricted {
		t.Fatalf("expected empty attributes to default to restricted")
	}
	if ProjectMetadataRetention(&env) != RetentionPermanent {
		t.Fatalf("expected empty attributes to default to permanent")
	}

	env.Attributes["data_classification"] = "internal"
	env.Attributes["retention_class"] = "30d"
	if ProjectMetadataClassification(&env) != ClassificationInternal {
		t.Fatalf("expected ClassificationInternal")
	}
	if ProjectMetadataRetention(&env) != Retention30Days {
		t.Fatalf("expected Retention30Days")
	}
}

func TestLatticeMaplet_JoinSemilatticeInvariant(t *testing.T) {
	m := NewLatticeMaplet(100)

	// Inserting same key with lower then higher value should join to higher
	if err := m.Insert("key_lattice_1", ClassificationPublic); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	val, ok := m.Lookup("key_lattice_1")
	if !ok || val != ClassificationPublic {
		t.Fatalf("expected ClassificationPublic, got %d", val)
	}

	// Upgrade to Confidential
	if err := m.Insert("key_lattice_1", ClassificationConfidential); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	val, ok = m.Lookup("key_lattice_1")
	if !ok || val != ClassificationConfidential {
		t.Fatalf("expected joined ClassificationConfidential, got %d", val)
	}

	// Downgrade attempt to Internal should stay at Confidential (least upper bound)
	if err := m.Insert("key_lattice_1", ClassificationInternal); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	val, ok = m.Lookup("key_lattice_1")
	if !ok || val != ClassificationConfidential {
		t.Fatalf("expected ClassificationConfidential to persist across downgrade attempt, got %d", val)
	}

	// Value bound validation
	if err := m.Insert("key_invalid", 16); err == nil {
		t.Fatalf("expected error on value > 15")
	}

	// Absent key
	_, ok = m.Lookup("absent_key_xyz")
	if ok {
		t.Fatalf("expected absent key to return false")
	}

	// Empty maplet check
	emptyLattice := NewLatticeMaplet(8)
	if emptyLattice.BitsPerKey() != 0 {
		t.Fatalf("expected 0 bits per key on empty lattice maplet")
	}

	// Verify stats
	if m.Count() == 0 || m.MemoryBytes() == 0 || m.BitsPerKey() == 0 {
		t.Fatalf("unexpected zero stats")
	}
}

func TestLatticeMaplet_CapacityBoundary(t *testing.T) {
	// Tiny maplet to force capacity exhaustion
	m := NewLatticeMaplet(8)
	var err error
	for i := range 50 {
		err = m.Insert(fmt.Sprintf("key_%d", i), 1)
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatalf("expected capacity or cuckoo cycle error on tiny maplet")
	}
}

func TestWideMaplet_LifecycleAndZeroAllocations(t *testing.T) {
	const n = 1000
	m := NewWideMaplet(n)

	for i := range 500 {
		key := fmt.Sprintf("wide_key_%d", i)
		val := uint8((i % 15) + 1)
		if err := m.Insert(key, val); err != nil {
			t.Fatalf("insert failed at %d: %v", i, err)
		}
	}

	// Overwrite existing key
	if err := m.Insert("wide_key_10", 7); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	val, ok := m.Lookup("wide_key_10")
	if !ok || val != 7 {
		t.Fatalf("expected updated value 7, got %d", val)
	}

	// Invalid value
	if err := m.Insert("invalid_val", 16); err == nil {
		t.Fatalf("expected error for value > 15")
	}

	// Verify 0 allocations on lookup
	lookupKey := "wide_key_100"
	allocs := testing.AllocsPerRun(1000, func() {
		v, found := m.Lookup(lookupKey)
		if !found || v != uint8((100%15)+1) {
			t.Fatalf("lookup failed")
		}
	})
	if allocs > 0 {
		t.Fatalf("expected 0 allocs/op for WideMaplet.Lookup, got %.2f", allocs)
	}

	// Absent key lookup
	_, ok = m.Lookup("definitely_absent_wide_key")
	if ok {
		t.Fatalf("expected absent key to return false in wide maplet")
	}

	// Verify stats
	if m.Count() != 500 {
		t.Fatalf("expected 500 keys, got %d", m.Count())
	}
	if m.MemoryBytes() == 0 || m.BitsPerKey() == 0 {
		t.Fatalf("unexpected zero stats")
	}

	// Empty maplet stats check
	empty := NewWideMaplet(10)
	if empty.BitsPerKey() != 0 {
		t.Fatalf("expected 0 bits per key on empty maplet")
	}
}

func TestCountingMaplet_LifecycleAndReset(t *testing.T) {
	cm := NewCountingMaplet(100)

	// First increment
	if c := cm.Increment("client_ip_1"); c != 1 {
		t.Fatalf("expected count 1, got %d", c)
	}

	// Repeated increments
	for i := 2; i <= 10; i++ {
		if c := cm.Increment("client_ip_1"); c != uint8(i) {
			t.Fatalf("expected count %d, got %d", i, c)
		}
	}

	// Atomic lock-free read
	if got := cm.GetCount("client_ip_1"); got != 10 {
		t.Fatalf("expected count 10, got %d", got)
	}

	// Absent key
	if got := cm.GetCount("absent_client_ip"); got != 0 {
		t.Fatalf("expected count 0 for absent, got %d", got)
	}

	// Zero allocations on GetCount
	lookupKey := "client_ip_1"
	allocs := testing.AllocsPerRun(1000, func() {
		_ = cm.GetCount(lookupKey)
	})
	if allocs > 0 {
		t.Fatalf("expected 0 allocs for CountingMaplet.GetCount, got %.2f", allocs)
	}

	// Reset
	cm.Reset()
	if cm.Count() != 0 {
		t.Fatalf("expected 0 count after reset")
	}
	if got := cm.GetCount("client_ip_1"); got != 0 {
		t.Fatalf("expected count 0 after reset, got %d", got)
	}
	if cm.MemoryBytes() == 0 {
		t.Fatalf("unexpected zero memory bytes")
	}
}

func TestCountingMaplet_DisplacementAndSaturation(t *testing.T) {
	cm := NewCountingMaplet(8)
	// Fill to capacity and trigger displacements
	for i := range 40 {
		cm.Increment(fmt.Sprintf("ip_%d", i))
	}

	// Saturate one counter to 255
	for range 260 {
		cm.Increment("saturated_ip")
	}
	if got := cm.GetCount("saturated_ip"); got != 255 {
		t.Fatalf("expected saturated count 255, got %d", got)
	}
}

func TestScalableLatticeMaplet_AutoGrowth(t *testing.T) {
	// Start with tiny capacity of 8
	sm := NewScalableLatticeMaplet(8)
	if sm.LayerCount() != 1 {
		t.Fatalf("expected 1 layer initially")
	}

	// Insert 50 keys, which will trigger layer doubling and expansion
	for i := range 50 {
		err := sm.Insert(fmt.Sprintf("scale_key_%d", i), ClassificationInternal)
		if err != nil {
			t.Fatalf("insert failed at %d: %v", i, err)
		}
	}

	if sm.LayerCount() <= 1 {
		t.Fatalf("expected auto-growth to add layers, got %d", sm.LayerCount())
	}

	// Verify all keys are retrievable
	for i := range 50 {
		val, ok := sm.Lookup(fmt.Sprintf("scale_key_%d", i))
		if !ok || val != ClassificationInternal {
			t.Fatalf("key %d not found or wrong value: ok=%v val=%d", i, ok, val)
		}
	}

	// Absent key
	_, ok := sm.Lookup("absent_scalable_key")
	if ok {
		t.Fatalf("expected absent key to return false")
	}

	if sm.MemoryBytes() == 0 {
		t.Fatalf("expected non-zero memory bytes")
	}
}

func TestWideMaplet_CapacityBoundary(t *testing.T) {
	m := NewWideMaplet(8)
	var err error
	for i := range 50 {
		err = m.Insert(fmt.Sprintf("key_%d", i), 1)
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatalf("expected capacity error on overloaded wide maplet")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: LatticeMaplet, WideMaplet, CountingMaplet, ScalableLatticeMaplet
// ---------------------------------------------------------------------------

func BenchmarkLatticeMaplet_Lookup(b *testing.B) {
	const n = 10000
	m := NewLatticeMaplet(n)
	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("tenant_%d:record_%d", i%10, i)
		_ = m.Insert(keys[i], ClassificationInternal)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		k := keys[i&(n-1)]
		val, _ := m.Lookup(k)
		_ = val
	}
}

func BenchmarkWideMaplet_Lookup(b *testing.B) {
	const n = 10000
	m := NewWideMaplet(n)
	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("tenant_%d:record_%d", i%10, i)
		_ = m.Insert(keys[i], uint8((i%15)+1))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		k := keys[i&(n-1)]
		val, _ := m.Lookup(k)
		_ = val
	}
}

func BenchmarkCountingMaplet_Increment(b *testing.B) {
	cm := NewCountingMaplet(10000)
	ips := make([]string, 1000)
	for i := range 1000 {
		ips[i] = fmt.Sprintf("192.168.1.%d", i%256)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cm.Increment(ips[i%1000])
	}
}

func BenchmarkScalableLatticeMaplet_Lookup(b *testing.B) {
	const n = 10000
	sm := NewScalableLatticeMaplet(2000)
	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("scale_key_%d", i)
		_ = sm.Insert(keys[i], ClassificationConfidential)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		k := keys[i&(n-1)]
		val, _ := sm.Lookup(k)
		_ = val
	}
}
