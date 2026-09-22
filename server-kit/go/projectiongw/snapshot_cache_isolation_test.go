package projectiongw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/proto"
)

func TestSnapshotCacheSeparatesAudienceBoundaries(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies()})
	gw.snapshotCache = newSnapshotCache(testSnapshotCacheBudget)
	applyOrders(t, gw, orderMutation("first", 1, "a", ""), orderMutation("second", 2, "a\x00b", ""))
	for _, fixture := range []struct {
		audiences []string
		want      string
	}{
		{[]string{"a", "b"}, "first"},
		{[]string{"a\x00b"}, "second"},
		{[]string{"b", "a", "a"}, "first"},
		{[]string{"a\x00b"}, "second"},
	} {
		handler := gw.Handler(HandlerConfig{
			Tenant:   func(*http.Request) (string, error) { return "org_default", nil },
			Audience: func(*http.Request) ([]string, error) { return fixture.audiences, nil },
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/projections/marketplace/orders", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("snapshot failed: %d %s", response.Code, response.Body.String())
		}
		var snapshot foundationpb.ProjectionSnapshot
		if err := proto.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		mutations := snapshot.GetBatch().GetMutations()
		if len(mutations) != 1 || mutations[0].GetRecordId() != fixture.want {
			t.Fatalf("audiences %q received %v, want only %s", fixture.audiences, mutations, fixture.want)
		}
	}
	if stats := gw.CacheStats(); stats.Entries != 2 || stats.Hits != 2 {
		t.Fatalf("expected isolated reusable entries, got %+v", stats)
	}
}

func TestSnapshotCacheRejectsUnresolvableAudience(t *testing.T) {
	gw := newAudienceGateway(t, AudienceConfig{Policies: orderAudiencePolicies(), Strict: true})
	gw.snapshotCache = newSnapshotCache(testSnapshotCacheBudget)
	for _, fixture := range []struct {
		scope     *foundationpb.ProjectionScope
		audiences []string
	}{
		{audienceScope(), nil},
		{audienceScope(), []string{" "}},
		{nil, []string{"a"}},
		{&foundationpb.ProjectionScope{Domain: audienceDomain, Collection: audienceCollection}, []string{"a"}},
	} {
		req := &foundationpb.ProjectionSnapshotRequest{Scope: fixture.scope}
		if _, _, cacheable := gw.snapshotCacheLookupKey(req, fixture.audiences); cacheable {
			t.Fatalf("invalid request became cacheable: %+v", fixture)
		}
	}
}

func BenchmarkAudienceDigest(b *testing.B) {
	for _, fixture := range []struct {
		name string
		ids  []string
	}{
		{"single", []string{"customer-1"}},
		{"multiple", []string{"customer-1", "merchant-2", "team-3"}},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if audienceDigest(fixture.ids) == "" {
					b.Fatal("missing digest")
				}
			}
		})
	}
}
