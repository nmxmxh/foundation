package projectiongw

import (
	"fmt"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func databaseRecordForCollection(recordID, collection string) database.DomainRecord {
	record := databaseRecordForAudience(recordID, "cust_alice")
	record.Collection = collection
	return record
}

func databaseRecordForAudience(recordID, customer string) database.DomainRecord {
	return database.DomainRecord{
		Domain:         audienceDomain,
		Collection:     audienceCollection,
		OrganizationID: "org_default",
		RecordID:       recordID,
		Data:           database.RecordData{{Name: "customer_id", Value: database.StringValue(customer)}},
	}
}

// newAudienceBenchGateway materializes `records` orders spread over `audiences`
// customers, behind a per-record policy on the indexed customer field.
func newAudienceBenchGateway(b *testing.B, records, audiences int) *Gateway {
	b.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: audienceDomain, Domain: audienceDomain, Collection: audienceCollection,
		IndexedFields: []string{"customer_id"}, MaxRecords: records + 1, MaxBytes: 1 << 30,
	})
	if err != nil {
		b.Fatalf("NewStore() err=%v", err)
	}
	ctx := b.Context()
	for index := range records {
		if _, applyErr := store.Apply(ctx, audienceDomain, hermes.Event{
			Operation: hermes.OperationUpsert,
			SourceID:  fmt.Sprintf("order_%d", index),
			Version:   uint64(index + 1),
			Record: databaseRecordForAudience(
				fmt.Sprintf("order_%d", index),
				fmt.Sprintf("cust_%d", index%audiences),
			),
		}); applyErr != nil {
			b.Fatalf("Apply() err=%v", applyErr)
		}
	}
	gw, err := NewGateway(store, 0, WithAudience(AudienceConfig{Policies: map[string]AudiencePolicy{
		audienceScopeName: {Mode: AudiencePerRecord, Fields: []string{"customer_id"}},
	}}))
	if err != nil {
		b.Fatalf("NewGateway() err=%v", err)
	}
	b.Cleanup(gw.Close)
	return gw
}

// benchAudienceMutations builds one accepted batch whose records spread over
// `audiences` distinct owners — the input that decides how many frames the
// fan-out has to encode.
func benchAudienceMutations(records, audiences int) []hermes.AppliedMutation {
	applied := make([]hermes.AppliedMutation, 0, records)
	for index := range records {
		applied = append(applied, hermes.AppliedMutation{
			Operation: hermes.OperationUpsert,
			Version:   uint64(index + 1),
			Record: databaseRecordForAudience(
				fmt.Sprintf("order_%d", index),
				fmt.Sprintf("cust_%d", index%audiences),
			),
		})
	}
	return applied
}

// BenchmarkGroupAcceptedByAudience is the load-bearing claim of the audience
// design: fan-out work is a function of the DISTINCT AUDIENCES in a batch, not
// of how many subscribers are connected. Subscribers never appear here — they
// share whatever frames grouping produced — so a deployment's cost grows with
// how many different people a write concerns, which is a property of the write.
//
// Compare against audiences=1, which is the pre-audience shape: one group, one
// encode, exactly the work the tenant-wide path did.
func BenchmarkGroupAcceptedByAudience(b *testing.B) {
	policy := AudienceConfig{Policies: map[string]AudiencePolicy{
		audienceScopeName: {Mode: AudiencePerRecord, Fields: []string{"customer_id"}},
	}}
	tenant := AudienceConfig{}
	for _, audiences := range []int{1, 8, 64} {
		applied := benchAudienceMutations(256, audiences)
		b.Run(fmt.Sprintf("audiences=%d", audiences), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				groups, dropped := groupAccepted(applied, policy)
				if len(groups) != audiences || dropped != 0 {
					b.Fatalf("groups=%d dropped=%d, want %d/0", len(groups), dropped, audiences)
				}
			}
		})
		b.Run(fmt.Sprintf("tenant_baseline/audiences=%d", audiences), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				groups, _ := groupAccepted(applied, tenant)
				if len(groups) != 1 {
					b.Fatalf("groups=%d, want 1", len(groups))
				}
			}
		})
	}
}

// BenchmarkAudienceBroadcastBySubscribers holds the batch fixed and varies the
// subscriber count over one audience. Encoding already happened; this is the
// share-the-frame half, and it is what must NOT grow superlinearly for the
// design to be safe to turn on.
func BenchmarkAudienceBroadcastBySubscribers(b *testing.B) {
	for _, subscribers := range []int{1, 64, 512} {
		b.Run(fmt.Sprintf("subscribers=%d", subscribers), func(b *testing.B) {
			hub := NewHub(1 << 14)
			scope := audienceScope()
			keys := audienceKeys(scope, []string{"cust_0"})
			subs := make([]*Subscription, 0, subscribers)
			for range subscribers {
				subs = append(subs, hub.SubscribeKeys(keys, false))
			}
			defer func() {
				for _, sub := range subs {
					sub.Cancel()
				}
			}()
			// Drain so the bounded buffers never become the thing measured.
			for _, sub := range subs {
				go func(sub *Subscription) {
					for range sub.Frames {
					}
				}(sub)
			}
			frame := Frame{Envelope: make([]byte, 512), EnvelopeWithoutVectors: make([]byte, 512)}
			b.ReportAllocs()
			for b.Loop() {
				hub.Broadcast(keys[0], frame)
			}
		})
	}
}

// BenchmarkAudienceSnapshotFanOut measures the union read: one bounded read per
// (field, audience) pair. The point of the numbers is that each added pair
// costs one more O(limit) read rather than turning the read into O(scope).
func BenchmarkAudienceSnapshotFanOut(b *testing.B) {
	for _, audiences := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("audiences=%d", audiences), func(b *testing.B) {
			gw := newAudienceBenchGateway(b, 4096, audiences)
			request := &foundationpb.ProjectionSnapshotRequest{Scope: audienceScope(), Limit: 128}
			ids := make([]string, 0, audiences)
			for index := range audiences {
				ids = append(ids, fmt.Sprintf("cust_%d", index))
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := gw.SnapshotAudience(b.Context(), request, ids); err != nil {
					b.Fatalf("SnapshotAudience() err=%v", err)
				}
			}
		})
	}
}
