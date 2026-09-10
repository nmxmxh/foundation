package projectiongw

import (
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metrics"
)

// Snapshot read-path instrumentation.
//
// The gateway had no metrics before this: an encode that cost 27 MB of transient
// garbage per response was invisible, and so was the fan-out multiplier that
// made it expensive. These record the three things that decide whether the wire
// path is behaving — how much work a response cost, how much of it was avoided,
// and how large the pages actually are.
//
// Tags carry domain and collection but never the tenant id. Tenant is unbounded
// cardinality: one series per tenant per metric would grow without limit and is
// the standard way a metrics backend gets taken down by its own instrumentation.

const (
	// metricSnapshotTotal counts served snapshot responses by outcome.
	metricSnapshotTotal = "projection_snapshot_total"
	// metricSnapshotEncodeMS is wall time from a resolved page to the last byte
	// handed to the writer.
	metricSnapshotEncodeMS = "projection_snapshot_encode_ms"
	// metricSnapshotBytes is the encoded response size.
	metricSnapshotBytes = "projection_snapshot_bytes"
	// metricSnapshotRecords is the record count in a page, which is what makes
	// an encode cost what it costs.
	metricSnapshotRecords = "projection_snapshot_records"
	// metricSnapshotCacheTotal counts cache outcomes: hit, miss, stale, bypass.
	metricSnapshotCacheTotal = "projection_snapshot_cache_total"
	// metricSnapshotCacheBytes is the cache's current resident size.
	metricSnapshotCacheBytes = "projection_snapshot_cache_bytes"
	// metricSnapshotCacheEntries is the cache's current entry count.
	metricSnapshotCacheEntries = "projection_snapshot_cache_entries"
	// metricSnapshotCacheEvictions counts entries dropped for budget. It is a
	// gauge over a monotonic counter rather than a counter metric, because the
	// cache owns the running total and the serve path only samples it.
	metricSnapshotCacheEvictions = "projection_snapshot_cache_evictions"
	// metricAudienceDrops exposes the existing audienceDrops counter, which was
	// only readable through a Go accessor before. A non-zero value means
	// accepted mutations reached no subscriber, so some client is relying on its
	// next snapshot to reconcile.
	metricAudienceDrops = "projection_audience_drops"
)

// Cache outcome labels.
const (
	cacheOutcomeHit    = "hit"
	cacheOutcomeMiss   = "miss"
	cacheOutcomeStale  = "stale"
	cacheOutcomeBypass = "bypass"
)

// Encode path labels.
const (
	encodePathCached   = "cached"
	encodePathStreamed = "streamed"
	encodePathMarshal  = "marshal"
)

// scopeTags builds the low-cardinality tag set for a scope.
func scopeTags(scope *foundationpb.ProjectionScope) metrics.Tags {
	return metrics.Tags{
		"domain":     scope.GetDomain(),
		"collection": scope.GetCollection(),
	}
}

// withTag returns a copy of tags with one key added, so a shared base tag set is
// never mutated by a recorder.
func withTag(tags metrics.Tags, key, value string) metrics.Tags {
	out := make(metrics.Tags, len(tags)+1)
	for k, v := range tags {
		out[k] = v
	}
	out[key] = value
	return out
}

// recordSnapshotServed reports one served response: its encode path, size,
// record count, and the time the encode took.
func recordSnapshotServed(scope *foundationpb.ProjectionScope, path string, records int, bytes int64, elapsed time.Duration) {
	tags := scopeTags(scope)
	metrics.Counter(metricSnapshotTotal, withTag(tags, "path", path))
	metrics.Histogram(metricSnapshotEncodeMS, withTag(tags, "path", path), float64(elapsed.Nanoseconds())/1e6)
	metrics.Histogram(metricSnapshotBytes, tags, float64(bytes))
	metrics.Histogram(metricSnapshotRecords, tags, float64(records))
}

// recordSnapshotCache reports one cache outcome.
func recordSnapshotCache(scope *foundationpb.ProjectionScope, outcome string) {
	metrics.Counter(metricSnapshotCacheTotal, withTag(scopeTags(scope), "outcome", outcome))
}

// publishGauges mirrors sampled gateway state into gauges: cache occupancy, and
// the audience-drop counter that previously had no publish point at all.
//
// It is called on the serve path rather than from a ticker, so the gateway needs
// no goroutine of its own and an idle one reports nothing.
func (g *Gateway) publishGauges() {
	stats := g.snapshotCache.stats()
	metrics.Gauge(metricSnapshotCacheBytes, nil, float64(stats.Bytes))
	metrics.Gauge(metricSnapshotCacheEntries, nil, float64(stats.Entries))
	metrics.Gauge(metricSnapshotCacheEvictions, nil, float64(stats.Evictions))
	metrics.Gauge(metricAudienceDrops, nil, float64(g.AudienceDrops()))
}

// CacheStats exposes the snapshot cache's observable state. It is nil-safe and
// returns the zero value when no cache is configured.
func (g *Gateway) CacheStats() SnapshotCacheStats {
	return g.snapshotCache.stats()
}
