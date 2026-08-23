//go:build linux || darwin

package runtimehost

import (
	"context"
	"os"
	"testing"
)

// BenchmarkProcessPoolEpochExchange measures a warm pooled exchange over the
// shm-epoch transport: doorbell wake, slot swap, kernel round trip, ack.
// Fixture cost (child spawn, mapping, warmup) is paid once under setup.
// OPEN ISSUE (2026-08-23): under sustained load the child intermittently
// observes peer-lost, triggering a restart whose ready-handshake then stalls
// (~10 s). Gated behind RUN_PROCESS_POOL_BENCH=1 until the doorbell/restart
// interaction settles; see process_pool_doorbell_test.go for the passing
// contract legs.
func BenchmarkProcessPoolEpochExchange(b *testing.B) {
	if os.Getenv("RUN_PROCESS_POOL_BENCH") != "1" {
		b.Skip("open restart-storm issue; set RUN_PROCESS_POOL_BENCH=1 to reproduce")
	}
	pool := newEpochDoorbellPool(b)
	defer func() { _ = pool.Close() }()

	req := ProcessRequest{UnitID: "runtime.echo", Input: []byte("bench"), ContextHash: 1, ModuleVersion: 7}
	if _, err := pool.Execute(context.Background(), req); err != nil {
		b.Fatalf("warmup execute: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := pool.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}
