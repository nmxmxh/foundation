//go:build linux || darwin

package runtimehost

import (
	"context"
	"testing"
)

// BenchmarkProcessPoolEpochExchange measures a warm pooled exchange over the
// shm-epoch transport: doorbell wake, slot swap, kernel round trip, ack.
// Fixture cost (child spawn, mapping, warmup) is paid once under setup.
// BenchmarkProcessPoolEpochExchange measures a warm pooled exchange over the
// shm-epoch transport: doorbell wake, slot swap, kernel round trip, ack.
// Sustained-load stability is guaranteed by the baseline-arming contract in
// process_pool_doorbell_test.go (arm waited-slot baselines BEFORE triggering
// the counterpart's publish).
func BenchmarkProcessPoolEpochExchange(b *testing.B) {
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
