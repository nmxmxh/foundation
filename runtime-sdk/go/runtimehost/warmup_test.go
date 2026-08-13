package runtimehost

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Warming must touch every page and change nothing.
//
// The second half matters more than the first: this runs against a live shared
// mapping, and a warm-up that altered a byte the arena header or a descriptor
// occupies would corrupt the data plane at startup — silently, because the
// corruption would look like a stale descriptor rather than a write.
func TestWarmMappingTouchesEveryPageWithoutAlteringContents(t *testing.T) {
	page := os.Getpagesize()
	raw := make([]byte, page*4)
	for i := range raw {
		raw[i] = 0xAB
	}
	// Zero the bytes warming is allowed to write, so the region is in the state
	// the process pool guarantees: everything warming touches is already zero.
	for offset := 0; offset < len(raw); offset += page {
		raw[offset] = 0
	}
	before := string(raw)

	warmMapping(raw)

	if string(raw) != before {
		t.Error("warmMapping altered the mapping's contents")
	}
}

// A short or empty region must not panic. Both are reachable: a pool may be
// configured without an arena, and the unsupported-platform segment carries a
// nil mapping.
func TestWarmMappingHandlesEmptyAndShortRegions(t *testing.T) {
	warmMapping(nil)
	warmMapping([]byte{})
	warmMapping(make([]byte, 1))
}

// Warming happens before the arena header is written, so an arena built over a
// warmed mapping must still validate. If the order were reversed this would
// fail on the magic word, which is the exact corruption the ordering prevents.
func TestArenaOverAWarmedMappingStillValidates(t *testing.T) {
	raw := make([]byte, generated.ARENA_MIN_BYTES)
	warmMapping(raw)

	arena, err := NewArenaOver(raw)
	if err != nil {
		t.Fatalf("NewArenaOver() over a warmed mapping: %v", err)
	}
	if got := arena.Stats().CapacityBytes; got != generated.ARENA_MIN_BYTES {
		t.Errorf("capacity = %d, want %d", got, generated.ARENA_MIN_BYTES)
	}
}

// recordingExchange captures what an exchange was asked to do.
type recordingExchange struct {
	unitIDs     []string
	bufferSizes []int
	err         error
}

func (r *recordingExchange) Exchange(_ context.Context, unitID string, buffer []byte) error {
	r.unitIDs = append(r.unitIDs, unitID)
	r.bufferSizes = append(r.bufferSizes, len(buffer))
	return r.err
}

func (r *recordingExchange) Close() error   { return nil }
func (r *recordingExchange) Restart() error { return nil }

func warmupWorker(t *testing.T, exchange workerExchange, unitID string) *processWorker {
	t.Helper()
	worker := &processWorker{
		logger:        testLogger(t),
		mode:          ProcessTransportStdio,
		testExchange:  exchange,
		warmupUnitID:  unitID,
		warmupTimeout: time.Second,
	}
	t.Cleanup(func() { worker.stopExchangeLoop() })
	return worker
}

// An unconfigured pool must still warm, or the feature helps only the callers
// who already knew to ask for it — and those are not the ones being surprised
// by a 17x first call.
func TestWarmupUsesTheDefaultUnitIDWhenUnconfigured(t *testing.T) {
	exchange := &recordingExchange{}
	warmupWorker(t, exchange, "").warmupLocked()

	if len(exchange.unitIDs) != 1 {
		t.Fatalf("warmup issued %d exchanges, want exactly 1", len(exchange.unitIDs))
	}
	if exchange.unitIDs[0] != DefaultWarmupUnitID {
		t.Errorf("warmup unit id = %q, want %q", exchange.unitIDs[0], DefaultWarmupUnitID)
	}
	// A short buffer would be rejected by the kernel before it ran through the
	// code this is trying to fault in, warming nothing.
	if exchange.bufferSizes[0] != int(generated.BUFFER_TOTAL_BYTES) {
		t.Errorf("warmup buffer = %d bytes, want the full %d control buffer",
			exchange.bufferSizes[0], generated.BUFFER_TOTAL_BYTES)
	}
}

// A pool that serves one hot unit wants that unit's own pages warmed too.
func TestWarmupHonoursAConfiguredUnitID(t *testing.T) {
	exchange := &recordingExchange{}
	warmupWorker(t, exchange, "runtime.echo").warmupLocked()

	if len(exchange.unitIDs) != 1 || exchange.unitIDs[0] != "runtime.echo" {
		t.Fatalf("warmup unit ids = %v, want [runtime.echo]", exchange.unitIDs)
	}
}

// The warm-up is an optimisation. A kernel that refuses it must still yield a
// usable worker: trading a latency problem for an availability one would be a
// strictly worse outcome than the cold start.
func TestWarmupFailureLeavesTheWorkerUsable(t *testing.T) {
	exchange := &recordingExchange{err: errors.New("kernel refused the warmup")}
	worker := warmupWorker(t, exchange, "")

	worker.warmupLocked()

	exchange.err = nil
	buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
	if err := worker.executeWithContext(context.Background(), "runtime.echo", buffer); err != nil {
		t.Fatalf("worker unusable after a failed warmup: %v", err)
	}
}
