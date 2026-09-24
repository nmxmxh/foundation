//go:build linux || darwin

package runtimehost

import (
	"context"
	"testing"
)

func TestDispatchSnapshotsAllocateNothing(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := block.Close(); err != nil {
			t.Error(err)
		}
	})
	allocations := testing.AllocsPerRun(100, func() {
		descriptors, err := block.SnapshotDescriptors()
		if err != nil {
			t.Fatal(err)
		}
		stats, err := block.SnapshotStats()
		if err != nil {
			t.Fatal(err)
		}
		if _, eligible := Decide(1, descriptors[:], stats[:], DispatchRequest{}); eligible {
			t.Fatal("empty table selected a lane")
		}
	})
	if allocations != 0 {
		t.Fatalf("snapshot allocations: got %g, want 0", allocations)
	}
}

func TestProcessPoolBufferAllocationBudget(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("race instrumentation changes allocation counts")
	}
	pool := benchmarkProcessPool()
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	req, dst := ProcessRequest{UnitID: "runtime.echo", Input: make([]byte, 1024)}, make([]byte, 1024)
	allocations := testing.AllocsPerRun(100, func() {
		response, err := pool.ExecuteInto(context.Background(), req, dst)
		if err != nil || len(response.Output) != len(dst) {
			t.Fatalf("execute: %v", err)
		}
	})
	// The exchange fixture allocates one wrapper. Cancellation owns the remaining three allocations.
	if allocations > 4 {
		t.Fatalf("process allocations: got %g, ceiling 4", allocations)
	}
	if _, err := (&ProcessPool{}).Execute(context.Background(), req); err == nil {
		t.Fatal("missing buffer pool must fail")
	}
}
