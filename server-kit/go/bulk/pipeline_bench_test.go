package bulk

import (
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	transport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

func BenchmarkPipelineHandleStatus(b *testing.B) {
	mgr, pipeline := benchmarkPipeline(b)
	ctx := bulkContext("org_bench", "corr_pipeline_bench", "idem_pipeline_bench")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "pipeline_status",
		TotalSize:  1 << 20,
		ChunkSize:  1 << 20,
		MaxMemory:  1 << 20,
	})
	if err != nil {
		b.Fatalf("Initiate() error = %v", err)
	}
	env := transport.CreateEnvelope(EventStatusRequested, map[string]any{
		"transfer_id": plan.TransferID,
	}, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pipeline.HandleControl(ctx, env); err != nil {
			b.Fatalf("HandleControl(status) error = %v", err)
		}
	}
}

func BenchmarkPipelinePlanLane(b *testing.B) {
	_, pipeline := benchmarkPipeline(b)
	req := LaneRequest{
		Plan: TransferPlan{
			TransferID:  "pipeline_lane",
			ChunkSize:   1 << 20,
			MaxMemory:   1 << 20,
			Compression: EncodingIdentity,
		},
		Part:                PartDescriptor{PartNumber: 0, Size: 1 << 20},
		Locality:            "internet",
		DirectObjectStore:   true,
		HTTPStreamAvailable: true,
		Capabilities: PlatformCapabilities{
			OS: "darwin",
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if plan := pipeline.PlanLane(req); plan.Selected != LaneSignedObjectStore {
			b.Fatalf("PlanLane() = %+v", plan)
		}
	}
}

func benchmarkPipeline(b *testing.B) (*Manager, *Pipeline) {
	b.Helper()
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-pipeline-bench",
		Bucket:   "bulk",
	})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 1 << 20,
		MaxChunkSize:     1 << 20,
		MaxParts:         DefaultMaxParts,
		Clock:            fixedNow,
	})
	if err != nil {
		b.Fatalf("NewManager() error = %v", err)
	}
	pipeline, err := NewPipeline(PipelineOptions{
		Manager:            mgr,
		Ingress:            "runtime-transport",
		ObjectStoreBackend: "memory",
		DistributedState:   true,
		Clock:              fixedNow,
	})
	if err != nil {
		b.Fatalf("NewPipeline() error = %v", err)
	}
	return mgr, pipeline
}
