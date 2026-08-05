package extension

import (
	"strconv"
	"testing"
	"unsafe"
)

// Value is the single most widely reused type in server-kit: 25 packages hold
// extension.Object on request paths. Its width is therefore not a local detail
// — every byte added here is multiplied by eight (Go allocates a whole bucket)
// and again by every map on every request.
//
// These are ceilings, in the same spirit as tooling/benchmark_baseline.psv:
// they may fall, never rise. A field added to Value without shrinking another
// fails here rather than showing up months later as unexplained bytes/op in
// the null lane (docs/foundation_benchmarks.md).
const (
	// maxValueSize is the current width of Value on 64-bit platforms.
	//
	// The layout is deliberately unpacked: kind, str, i64, u64, f64, b, bytes,
	// list, obj each occupy their own space even though Kind makes them
	// mutually exclusive.
	//
	// Packing them was measured and declined in August 2026. Per populated
	// Object, on go1.26: 112 bytes costs 1200 B/2 allocs, a packed 48 bytes
	// costs 624 B/2 allocs. The allocation count does not move at any width —
	// the win is bytes and latency only, and the path to single-digit allocs is
	// to not carry an Object per request (rule 1 of the package cost model),
	// not to shrink Value. That did not justify putting unsafe.Pointer into the
	// most widely held type in server-kit.
	//
	// This constant exists so the cost of that decision stays visible and does
	// not quietly grow. Reopen it with new measurements, not with intuition.
	maxValueSize = 112

	// maxObjectEntrySize is what one key/value pair costs inside an Object.
	// Go sizes map buckets for eight entries, so the first key written into any
	// Object allocates roughly 8*maxObjectEntrySize bytes — about 1 KB — no
	// matter how few keys follow it.
	maxObjectEntrySize = 128
)

func TestValueSizeCeiling(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("size ceilings are calibrated for 64-bit platforms")
	}

	if got := unsafe.Sizeof(Value{}); got > maxValueSize {
		t.Fatalf("unsafe.Sizeof(Value{}) = %d bytes, ceiling is %d.\n"+
			"Value is held by every extension.Object in server-kit; widening it "+
			"costs ~8x this delta per populated Object per request. Either shrink "+
			"another field or lower the ceiling deliberately and record the new "+
			"null-lane numbers in docs/foundation_benchmarks.md.", got, maxValueSize)
	}

	entry := unsafe.Sizeof("") + unsafe.Sizeof(Value{})
	if entry > maxObjectEntrySize {
		t.Fatalf("Object entry (string key + Value) = %d bytes, ceiling is %d", entry, maxObjectEntrySize)
	}
}

// TestObjectFirstKeyAllocatesBucket documents the consequence the ceilings
// above exist to bound: an Object holding one key costs about as much as one
// holding eight. Hot paths that need two or three known fields should use a
// struct; Object is for open schema at boundaries, which is what the package
// doc says and what this test prices.
func TestObjectFirstKeyAllocatesBucket(t *testing.T) {
	perOp := testing.AllocsPerRun(100, func() {
		obj := make(Object, 1)
		obj["k"] = String("v")
	})
	if perOp > 2 {
		t.Fatalf("populating a 1-key Object took %.0f allocations, want <= 2", perOp)
	}
}
