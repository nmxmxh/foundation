//go:build servicebacked

package servicebacked

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scaling"
)

func TestServiceBackedLoadDBConnectionBudgetKeepsHeadroom(t *testing.T) {
	if got := serviceBackedLoadDefaultDBReservedConnections(120); got != 24 {
		t.Fatalf("reserved connections = %d, want 24", got)
	}
	if got := serviceBackedLoadDBConnectionBudget(128, 120, 24); got != 96 {
		t.Fatalf("connection budget = %d, want 96", got)
	}
	if got := serviceBackedLoadDBConnectionBudget(40, 120, 24); got != 40 {
		t.Fatalf("explicit lower budget = %d, want 40", got)
	}
}

func TestServiceBackedLoadDefaultMaxWorkersUsesDBHeadroom(t *testing.T) {
	cfg := scaling.AutoTuneForCores(8)
	got := serviceBackedLoadDefaultMaxWorkers(cfg, 120, 24)
	if got != 96 {
		t.Fatalf("default max workers = %d, want 96", got)
	}
}

func TestServiceBackedLoadPipelineDBWorkerDefaultIsConservative(t *testing.T) {
	tests := []struct {
		cores int
		want  int
	}{
		{1, 1},
		{2, 1},
		{4, 2},
		{8, 6},
		{16, 6},
		{32, 6},
	}
	for _, tt := range tests {
		if got := serviceBackedLoadDefaultPipelineDBWorkersForCores(tt.cores); got != tt.want {
			t.Fatalf("cores=%d pipeline DB workers=%d, want %d", tt.cores, got, tt.want)
		}
	}
}

func TestServiceBackedLoadDBWorkersUseDedicatedBudget(t *testing.T) {
	opts := database.PoolOptions{MaxConns: 96}
	t.Setenv("SERVICE_BACKED_LOAD_RESEARCH_DB_WORKERS", "")
	wantDefault := serviceBackedLoadDefaultPipelineDBWorkersForCores(scaling.AutoTune().CPUCount)
	if got := serviceBackedLoadDBWorkers(t, 100000, 64, 96, opts); got != wantDefault {
		t.Fatalf("default database workers = %d, want %d", got, wantDefault)
	}
	t.Setenv("SERVICE_BACKED_LOAD_RESEARCH_DB_WORKERS", "96")
	if got := serviceBackedLoadDBWorkers(t, 100000, 64, 96, opts); got != 96 {
		t.Fatalf("explicit database workers = %d, want 96", got)
	}
	if got := serviceBackedLoadDBWorkers(t, 100000, 64, 12, opts); got != 12 {
		t.Fatalf("global worker limit = %d, want 12", got)
	}
	opts.MaxConns = 8
	if got := serviceBackedLoadDBWorkers(t, 100000, 64, 96, opts); got != 8 {
		t.Fatalf("pool worker limit = %d, want 8", got)
	}
}
