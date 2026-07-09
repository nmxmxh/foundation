//go:build servicebacked

package servicebacked

// Service-backed conformance: link the Hermes projection TLA specs to the real
// implementation running against real Postgres/Redis infrastructure.
//
//   docs/specs/tla/CacheProjectionFreshness.tla -> ProjectionVersionMonotonic
//   docs/specs/tla/HermesProjectionPublish.tla  -> VersionWatermarkConsistent
//
// TLC proves these invariants hold in the abstract model. This test drives the
// real hermes.Store, records a trace of (epoch, source watermark, applied
// version) after each real apply, and asserts the same invariants hold on the
// real execution -- an option-3 "does a real run refine the spec?" check on the
// live substrate.
//
// Run with the service-backed harness: `make test-service-backed` (requires a
// live Postgres + Redis; the //go:build servicebacked tag keeps it out of the
// default unit build).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func TestConformanceServiceBackedHermesProjectionMonotonic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	orgID := uniqueName(env.prefix, "conf-monotonic-org")
	cleanupOrganization(t, ctx, state, orgID)

	const projection = "svc_conformance_monotonic"
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    32,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	const recordID = "conf-tick"

	// A recorded trace step: what the projection exposed after a real apply.
	type step struct {
		version   uint64
		epoch     uint64
		watermark uint64
	}
	var trace []step

	// Apply a sequence of versioned upserts through the real Store.Apply path.
	for version := uint64(1); version <= 6; version++ {
		record := database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: orgID,
			RecordID:       recordID,
			Data:           serviceRecordData(map[string]any{"bucket": int64(version)}),
		}
		if _, err := state.UpsertRecord(ctx, record); err != nil {
			t.Fatalf("postgres upsert v%d failed: %v", version, err)
		}
		// SourceID is per-event idempotency identity (a producer retry reuses
		// it; a new mutation mints a new one), so each version carries its own.
		// The shared "service-backed" prefix drives the watermark dedup tier
		// the VersionWatermarkConsistent invariant observes. Reusing one
		// SourceID across versions made every apply after the first an exact
		// duplicate, so the watermark could never track the applied version.
		if _, err := store.Apply(ctx, projection, hermes.Event{
			Operation:     hermes.OperationUpsert,
			SourceID:      fmt.Sprintf("service-backed:conformance:%d", version),
			Version:       version,
			CorrelationID: "corr-conformance-monotonic",
			Record:        record,
		}); err != nil {
			t.Fatalf("hermes Apply v%d failed: %v", version, err)
		}

		epoch, err := store.Epoch(projection)
		if err != nil {
			t.Fatalf("Epoch() error = %v", err)
		}
		stats, err := store.Stats(projection)
		if err != nil {
			t.Fatalf("Stats() error = %v", err)
		}

		// TearFreeRead surrogate: the record is always readable as a consistent
		// snapshot after a publish (never missing mid-swap).
		if _, ok, err := store.GetRecord(ctx, projection, hermes.Query{OrganizationID: orgID}, recordID, hermes.Fence{}); err != nil || !ok {
			t.Fatalf("GetRecord after apply v%d: ok=%v err=%v", version, ok, err)
		}

		trace = append(trace, step{version: version, epoch: epoch, watermark: stats.SourceWatermark})
	}

	// Conformance invariants, checked on the real recorded trace.
	for i, s := range trace {
		// VersionWatermarkConsistent: the applied version never runs ahead of
		// the source watermark the projection reports.
		if s.version > s.watermark {
			t.Fatalf("step %d: applied version %d exceeds source watermark %d "+
				"(VersionWatermarkConsistent)", i, s.version, s.watermark)
		}
		if i == 0 {
			continue
		}
		prev := trace[i-1]
		// ProjectionVersionMonotonic: the watermark never regresses.
		if s.watermark < prev.watermark {
			t.Fatalf("step %d: source watermark regressed %d -> %d "+
				"(ProjectionVersionMonotonic)", i, prev.watermark, s.watermark)
		}
		// Epoch is monotone and advances on each applied batch.
		if s.epoch < prev.epoch {
			t.Fatalf("step %d: epoch regressed %d -> %d", i, prev.epoch, s.epoch)
		}
	}
}
