package appbench

import (
	"encoding/binary"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/vectorstr"
)

// ----------------------------------------------------------------------------
// 1. String Routing & Terminal State Benchmarks
// ----------------------------------------------------------------------------

var baselineTerminalMap = map[string]int{
	"success":   0,
	"requested": 1,
	"failed":    2,
	"ack":       3,
}

// The StringRouting, HeaderMatch and ControlPlane pairs below compare trivial
// units (a 4-key map, a 12-byte fold, one bump allocation). They stay as
// illustrations and are not ratcheted: a ratchet on a unit this small measures
// the compiler, not the foundation.
func BenchmarkStringRouting_StandardMap(b *testing.B) {
	states := []string{"requested", "success", "failed", "ack", "unknown_state"}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s := states[i%len(states)]
		_, ok := baselineTerminalMap[s]
		_ = ok
	}
}

func BenchmarkStringRouting_FoundationVector(b *testing.B) {
	states := []string{"requested", "success", "failed", "ack", "unknown_state"}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s := states[i%len(states)]
		_ = events.IsValidTerminalState(s)
	}
}

// ----------------------------------------------------------------------------
// 2. Header & Protocol Case-Insensitive Matching
// ----------------------------------------------------------------------------

func BenchmarkHeaderMatch_StandardEqualFold(b *testing.B) {
	headers := []string{"Content-Type", "CONTENT-TYPE", "content-type", "Authorization"}
	target := "content-type"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		h := headers[i%len(headers)]
		_ = strings.EqualFold(h, target)
	}
}

func BenchmarkHeaderMatch_FoundationVectorFold(b *testing.B) {
	headers := []string{"Content-Type", "CONTENT-TYPE", "content-type", "Authorization"}
	padded := vectorstr.MakePadded16("content-type")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		h := headers[i%len(headers)]
		_ = vectorstr.EqualFold16(h, &padded, 12)
	}
}

// ----------------------------------------------------------------------------
// 3. 4KB Control Plane Buffer Allocation
// ----------------------------------------------------------------------------

func BenchmarkControlPlane_StandardHeap(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		buf := make([]byte, 4096)
		binary.LittleEndian.PutUint32(buf[0:4], uint32(i))
		escapeSink = buf
	}
}

func BenchmarkControlPlane_FoundationScratch(b *testing.B) {
	arena := scratch.New(64 * 1024)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		arena.Reset()
		buf, err := arena.Alloc(4096)
		if err != nil {
			b.Fatal(err)
		}
		binary.LittleEndian.PutUint32(buf[0:4], uint32(i))
		escapeSink = buf
	}
}

// ----------------------------------------------------------------------------
// 4. End-to-End Request/Frame Dispatch Lifecycle
// ----------------------------------------------------------------------------

func BenchmarkLifecycleFrame_Standard(b *testing.B) {
	eventType := "order:checkout:success"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		// Standard approach: split string, hash map lookup, heap buffer allocation
		parts := strings.Split(eventType, ":")
		if len(parts) >= 3 {
			terminal := parts[len(parts)-1]
			if _, ok := baselineTerminalMap[terminal]; ok {
				// Allocate control message on heap
				ctrl := make([]byte, 4096)
				binary.LittleEndian.PutUint32(ctrl[0:4], 1)
				binary.LittleEndian.PutUint32(ctrl[4:8], uint32(i))
				escapeSink = ctrl
			}
		}
	}
}

func BenchmarkLifecycleFrame_Foundation(b *testing.B) {
	eventType := "order:checkout:success"
	arena := scratch.New(64 * 1024)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		arena.Reset()
		// Foundation approach: LastIndexByte, O(1) perfect hash + 16-byte vector, scratch bump allocation
		idx := strings.LastIndexByte(eventType, ':')
		if idx > 0 && idx < len(eventType)-1 {
			terminal := eventType[idx+1:]
			if events.IsValidTerminalState(terminal) {
				ctrl, _ := arena.Alloc(4096)
				binary.LittleEndian.PutUint32(ctrl[0:4], 1)
				binary.LittleEndian.PutUint32(ctrl[4:8], uint32(i))
				escapeSink = ctrl
			}
		}
	}
}

// ----------------------------------------------------------------------------
// 5. Empirical Garbage Churn & Latency Profile Test
// ----------------------------------------------------------------------------

var escapeSink []byte

// TestGreasedLightningGarbageAndLatencyProfile is an opt-in profile, not a gate.
// It allocates about 390 MB and asserts a wall-clock ratio, which is not stable
// under -race, coverage, or a loaded CI host. Run it with FOUNDATION_PERF_PROFILE=1.
func TestGreasedLightningGarbageAndLatencyProfile(t *testing.T) {
	if os.Getenv("FOUNDATION_PERF_PROFILE") != "1" || testing.Short() {
		t.Skip("opt-in profile: set FOUNDATION_PERF_PROFILE=1")
	}
	const iterations = 100000

	// Run Standard Path
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)
	startStandard := time.Now()

	for i := range iterations {
		parts := strings.Split("order:checkout:success", ":")
		terminal := parts[len(parts)-1]
		if _, ok := baselineTerminalMap[terminal]; ok {
			buf := make([]byte, 4096)
			binary.LittleEndian.PutUint32(buf[0:4], uint32(i))
			escapeSink = buf
		}
	}
	elapsedStandard := time.Since(startStandard)
	runtime.ReadMemStats(&m2)
	bytesStandard := m2.TotalAlloc - m1.TotalAlloc

	// Run Foundation Greased-Lightning Path
	runtime.GC()
	var m3, m4 runtime.MemStats
	runtime.ReadMemStats(&m3)
	startFoundation := time.Now()

	arena := scratch.New(64 * 1024)
	for i := range iterations {
		arena.Reset()
		const s = "order:checkout:success"
		idx := strings.LastIndexByte(s, ':')
		terminal := s[idx+1:]
		if events.IsValidTerminalState(terminal) {
			buf, _ := arena.Alloc(4096)
			binary.LittleEndian.PutUint32(buf[0:4], uint32(i))
			escapeSink = buf
		}
	}
	elapsedFoundation := time.Since(startFoundation)
	runtime.ReadMemStats(&m4)
	bytesFoundation := m4.TotalAlloc - m3.TotalAlloc

	speedup := float64(elapsedStandard) / float64(elapsedFoundation)
	garbageAvoidedMB := float64(bytesStandard-bytesFoundation) / (1024 * 1024)

	t.Logf("\n=======================================================")
	t.Logf("GREASED LIGHTNING EMPIRICAL PROOF (%d operations)", iterations)
	t.Logf("=======================================================")
	t.Logf("STANDARD PATH:")
	t.Logf("  Total Time:          %v", elapsedStandard)
	t.Logf("  Avg Latency:         %v/op", elapsedStandard/iterations)
	t.Logf("  Total Heap Garbage:  %.2f MB (%d bytes)", float64(bytesStandard)/(1024*1024), bytesStandard)
	t.Logf("-------------------------------------------------------")
	t.Logf("FOUNDATION PATH (Scratch + Vector + Perfect Hash):")
	t.Logf("  Total Time:          %v", elapsedFoundation)
	t.Logf("  Avg Latency:         %v/op", elapsedFoundation/iterations)
	t.Logf("  Total Heap Garbage:  %.2f MB (%d bytes)", float64(bytesFoundation)/(1024*1024), bytesFoundation)
	t.Logf("-------------------------------------------------------")
	t.Logf("PERFORMANCE RESULTS:")
	t.Logf("  Speedup:             %.2fx FASTER", speedup)
	t.Logf("  Garbage Avoided:     %.2f MB of GC churn eliminated", garbageAvoidedMB)
	t.Logf("=======================================================\n")

	if speedup < 2.0 {
		t.Errorf("expected at least 2.0x speedup, got %.2fx", speedup)
	}
	if garbageAvoidedMB < 300.0 {
		t.Errorf("expected >300 MB garbage avoided, got %.2f MB", garbageAvoidedMB)
	}
}
