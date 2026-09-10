package projectiongw

import (
	"context"
	"net/http"
	"strconv"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
)

// The snapshot serve path.
//
// Three routes to the same bytes, chosen by what is already known:
//
//	cached    the page is unchanged since a prior response at this epoch, so the
//	          assembled body is written straight through — no read, no encode
//	streamed  the page had to be read, and the response goes out through the wire
//	          writer without materializing a contiguous body
//	marshal   the fallback, byte-for-byte what the gateway did before
//
// All three are proven to produce identical bytes: TestWireAppendSnapshotMatchesMarshal
// asserts the assembled form equals proto.Marshal, and
// TestWireWriteSnapshotMatchesAppend asserts the streamed form equals the
// assembled one.

// snapshotEnvelopeHeadroom is the byte allowance for the ProjectionSnapshot
// fields around the batch body: the scope submessage, the batch tag and length,
// the watermark and cursor strings, the epoch varint, and the has_more flag. It
// is a sizing hint only — the buffer grows if a scope or cursor exceeds it.
const snapshotEnvelopeHeadroom = 128

// writeSnapshotHeaders sets the response headers every snapshot carries. They are
// set before the first byte, since a streamed response cannot revise them.
func writeSnapshotHeaders(w http.ResponseWriter, epoch uint64, watermark string) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("X-Projection-Epoch", strconv.FormatUint(epoch, 10))
	w.Header().Set("X-Projection-Watermark", watermark)
}

// snapshotCacheLookupKey derives the cache key for a request, plus the epoch the
// cached body would have to match. The third result is false when the request is
// not cacheable at all, in which case neither other result is meaningful.
//
// Uncacheable shapes:
//   - no cache configured
//   - a resume watermark or a keyset cursor (per-client by construction; each
//     would occupy an entry for exactly one read)
//   - an unresolvable scope, policy, or partition (let the normal path produce
//     the error, rather than reporting a cache outcome for a request that is
//     about to fail)
func (g *Gateway) snapshotCacheLookupKey(req *foundationpb.ProjectionSnapshotRequest, audiences []string) (snapshotCacheKey, uint64, bool) {
	var key snapshotCacheKey
	if g.snapshotCache == nil || !snapshotRequestCacheable(req) {
		return key, 0, false
	}
	scope := req.GetScope()
	policy, err := g.audience.resolve(scope.GetDomain(), scope.GetCollection())
	if err != nil {
		return key, 0, false
	}
	projection, _, err := g.resolve(scope)
	if err != nil {
		return key, 0, false
	}
	epoch, err := g.store.Epoch(projection)
	if err != nil {
		return key, 0, false
	}
	key = snapshotCacheKey{
		scope:          ScopeKey(scope),
		excludeVectors: req.GetVectorMode() == foundationpb.ProjectionVectorMode_PROJECTION_VECTOR_MODE_EXCLUDE,
		limit:          int(req.GetLimit()),
	}
	// An audience-partitioned page is only ever valid for the membership that
	// produced it. Broadcast pages share one entry; per-record pages are keyed
	// by a digest of the caller's audience set so no body can cross memberships.
	if policy.Mode == AudiencePerRecord {
		key.audience = audienceDigest(audiences)
		if key.audience == "" {
			// No resolvable audience: SnapshotAudience will refuse this request.
			return key, 0, false
		}
	}
	return key, epoch, true
}

// ServeSnapshot resolves and writes one snapshot response. It is the single
// write path for the HTTP snapshot handler, and the only place the cache and the
// wire assembly are consulted.
//
// On a cache hit no read is issued and no encode happens. Otherwise the page is
// read through the normal audience-aware path and the response is assembled
// once, served, and cached when the request shape allows it.
//
// An error is returned before any byte is written, so the caller can still map
// it to a status code. Once writing has begun a failure is reported as a written
// byte count plus an error and the response is truncated — there is no way to
// revise a status after the header is on the wire.
func (g *Gateway) ServeSnapshot(ctx context.Context, w http.ResponseWriter, req *foundationpb.ProjectionSnapshotRequest, audiences []string) error {
	scope := req.GetScope()
	start := time.Now()

	key, epoch, cacheable := g.snapshotCacheLookupKey(req, audiences)
	if cacheable {
		entry, outcome := g.snapshotCache.lookup(key, epoch)
		recordSnapshotCache(scope, outcome)
		if entry != nil {
			writeSnapshotHeaders(w, entry.epoch, entry.watermark)
			n, err := w.Write(entry.body)
			recordSnapshotServed(scope, encodePathCached, entry.records, int64(n), time.Since(start))
			return err
		}
	} else if g.snapshotCache != nil {
		recordSnapshotCache(scope, cacheOutcomeBypass)
	}

	snapshot, err := g.SnapshotAudience(ctx, req, audiences)
	if err != nil {
		return err
	}
	parts, err := snapshotPartsFromProto(snapshot)
	if err != nil {
		return err
	}
	records := len(snapshot.GetBatch().GetMutations())

	// A cacheable response is assembled once into a buffer the cache takes
	// ownership of; an uncacheable one is streamed and never held.
	if cacheable {
		// Size the buffer up front: the exact body length is already known, so
		// the assembly never regrows, and the cache takes ownership of the
		// result.
		body, assembleErr := parts.appendSnapshot(make([]byte, 0, parts.bodyLen()+snapshotEnvelopeHeadroom))
		if assembleErr != nil {
			return assembleErr
		}
		writeSnapshotHeaders(w, snapshot.GetEpoch(), snapshot.GetWatermark())
		// The body is a serialized foundation.v1.ProjectionSnapshot served as
		// application/x-protobuf. It is never interpreted as markup, and the
		// record values in it are the same bytes the uncached path wrote before.
		n, writeErr := w.Write(body) // #nosec G705 -- binary protobuf response, not a document
		recordSnapshotServed(scope, encodePathMarshal, records, int64(n), time.Since(start))
		// Store under the epoch the page was actually read at, not the epoch
		// probed before the read: an apply between the two would otherwise cache
		// the new page under the old epoch and serve it after the next advance.
		g.snapshotCache.store(key, &snapshotCacheEntry{
			epoch:     snapshot.GetEpoch(),
			watermark: snapshot.GetWatermark(),
			records:   records,
			body:      body,
		})
		g.publishGauges()
		return writeErr
	}

	writeSnapshotHeaders(w, snapshot.GetEpoch(), snapshot.GetWatermark())
	written, err := parts.writeSnapshot(w)
	recordSnapshotServed(scope, encodePathStreamed, records, written, time.Since(start))
	return err
}
