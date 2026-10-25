//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

func TestServiceBackedMapletTwoTierMetadataIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	redisClient := openRedis(t, env)
	defer func() { _ = redisClient.Close() }()

	orgID := uniqueName(env.prefix, "maplet-org")
	cleanupOrganization(t, ctx, state, orgID)

	const n = 50
	maplet := metadata.NewLatticeMaplet(n)

	// Tier 1: Persist canonical records into PostgreSQL with JSONB metadata
	classifications := []string{"public", "internal", "confidential", "restricted"}
	for i := 0; i < n; i++ {
		recordID := fmt.Sprintf("rec-%03d", i)
		class := classifications[i%4]
		metaCode := metadata.EncodeClassification(class)

		rec := database.DomainRecord{
			Domain:         "security",
			Collection:     "resources",
			OrganizationID: orgID,
			RecordID:       recordID,
			Data: serviceRecordData(map[string]any{
				"name": fmt.Sprintf("Resource %d", i),
				"metadata": map[string]any{
					"data_classification": class,
					"correlation_id":      fmt.Sprintf("corr-%03d", i),
				},
			}),
		}
		if _, err := state.UpsertRecord(ctx, rec); err != nil {
			t.Fatalf("failed to upsert record %s: %v", recordID, err)
		}

		// Tier 2: Populate in-memory LatticeMaplet
		if err := maplet.Insert(recordID, metaCode); err != nil {
			t.Fatalf("failed to insert maplet micro-value: %v", err)
		}
	}

	// Verify fast-path policy evaluation: Allow only Public (code 1)
	fastPathDropped := 0
	slowPathVerified := 0

	for i := 0; i < n; i++ {
		recordID := fmt.Sprintf("rec-%03d", i)
		expectedClass := classifications[i%4]

		// Fast Path: Query Tier 2 Maplet
		microVal, found := maplet.Lookup(recordID)
		if !found {
			t.Fatalf("maplet failed to find inserted key %s", recordID)
		}

		if microVal != metadata.ClassificationPublic {
			fastPathDropped++
			continue
		}

		// Slow Path: Fetch canonical record from PostgreSQL to verify
		slowPathVerified++
		readRec, foundRecord, err := state.GetRecord(ctx, "security", "resources", orgID, recordID)
		if err != nil || !foundRecord {
			t.Fatalf("failed to get canonical record from postgres: found=%v err=%v", foundRecord, err)
		}
		if expectedClass != "public" {
			t.Fatalf("security violation: expected public, got %s", expectedClass)
		}
		_ = readRec
	}

	if fastPathDropped != 37 || slowPathVerified != 13 {
		t.Fatalf("unexpected policy counts: dropped=%d verified=%d", fastPathDropped, slowPathVerified)
	}

	t.Logf("Service-backed maplet verification passed: %d/50 (%.0f%%) slow-path DB reads avoided",
		fastPathDropped, float64(fastPathDropped)/50.0*100)
}
