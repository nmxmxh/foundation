package projectiongw

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
)

// Epoch-keyed cache of assembled ProjectionSnapshot responses.
//
// A broadcast scope delivers byte-identical snapshots to every subscriber, so
// the response body depends only on (scope, epoch, vector mode, limit). The
// gateway currently re-reads and re-encodes it per request; hermes advances the
// partition epoch on every accepted apply, so an unchanged epoch is proof the
// materialized page has not moved and the cached body is still exact.
//
// Only the canonical cold-start shape is cached — no resume watermark, no
// keyset cursor. Those requests are per-client by construction and would each
// occupy an entry for one use. Audience-partitioned scopes are not cached here
// either: their response depends on the caller's audience set, and the union
// read that produces them has no unfiltered page to key on. wire.go's framed-
// record primitive is what that case needs, and is already built and tested
// against it; wiring it in requires restructuring the audience read path, which
// is deliberately not part of this change.
//
// The cache is opt-in (WithSnapshotCache). A zero budget disables it, which is
// the default: it trades memory for encode time and that trade belongs to the
// deploying project, not to the substrate.

// snapshotCacheKey identifies a cacheable response shape.
//
// Tenant is part of the scope key, so entries never cross tenants. The audience
// digest is the second isolation boundary: an audience-partitioned page is
// cached only for the exact audience set that produced it, so a caller can never
// be served a body assembled for a different membership. Broadcast scopes carry
// an empty digest and share one entry.
//
// vectorMode is reduced to a bool because that is all the read path uses —
// UNSPECIFIED and INCLUDE are the same read, so they must not occupy two entries.
type snapshotCacheKey struct {
	scope          string
	excludeVectors bool
	limit          int
	audience       string
}

// audienceDigest is a stable, collision-resistant key for a set of audience ids.
// Order and duplicates are normalized away so two callers with the same
// membership share an entry, and a digest is never a readable audience id in a
// metrics label or log line.
func audienceDigest(audiences []string) string {
	if len(audiences) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(audiences))
	seen := make(map[string]struct{}, len(audiences))
	for _, audience := range audiences {
		trimmed := strings.TrimSpace(audience)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return ""
	}
	sort.Strings(normalized)
	// A length prefix per id keeps the digest injective: {"a","bc"} and
	// {"ab","c"} must not hash to the same key.
	sum := sha256.New()
	for _, id := range normalized {
		_, _ = sum.Write([]byte(id))
		_, _ = sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)[:16])
}

// snapshotCacheEntry holds one assembled response and the epoch it is valid at.
type snapshotCacheEntry struct {
	epoch     uint64
	watermark string
	records   int
	body      []byte
	lastUsed  uint64
}

// snapshotCache is a bounded, epoch-validated store of assembled snapshot
// bodies. It is small on purpose: eviction is least-recently-used over a byte
// budget, with no background goroutine and no expiry clock.
type snapshotCache struct {
	maxBytes int

	mu      sync.Mutex
	entries map[snapshotCacheKey]*snapshotCacheEntry
	bytes   int
	clock   uint64

	hits      atomic.Uint64
	misses    atomic.Uint64
	stale     atomic.Uint64
	evictions atomic.Uint64
}

func newSnapshotCache(maxBytes int) *snapshotCache {
	if maxBytes <= 0 {
		return nil
	}
	return &snapshotCache{
		maxBytes: maxBytes,
		entries:  make(map[snapshotCacheKey]*snapshotCacheEntry),
	}
}

// lookup returns the cached body for key when it is valid at epoch, along with
// the outcome for instrumentation. A hit returns an entry whose body the caller
// must not modify: it is shared with every other reader at the same epoch.
func (c *snapshotCache) lookup(key snapshotCacheKey, epoch uint64) (*snapshotCacheEntry, string) {
	if c == nil {
		return nil, cacheOutcomeBypass
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		c.misses.Add(1)
		return nil, cacheOutcomeMiss
	}
	if entry.epoch != epoch {
		// The page moved. Drop it now rather than letting an unread stale entry
		// hold budget until eviction reaches it.
		c.removeLocked(key, entry)
		c.stale.Add(1)
		return nil, cacheOutcomeStale
	}
	c.clock++
	entry.lastUsed = c.clock
	c.hits.Add(1)
	return entry, cacheOutcomeHit
}

// store records an assembled body. A body larger than the whole budget is not
// cached — admitting it would evict everything else to serve one scope.
func (c *snapshotCache) store(key snapshotCacheKey, entry *snapshotCacheEntry) {
	if c == nil || entry == nil || len(entry.body) == 0 {
		return
	}
	if len(entry.body) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		c.removeLocked(key, existing)
	}
	c.clock++
	entry.lastUsed = c.clock
	c.entries[key] = entry
	c.bytes += len(entry.body)
	c.evictLocked()
}

// invalidateScope drops every entry for a scope regardless of vector mode or
// limit. Used when a scope is known to have changed out of band.
func (c *snapshotCache) invalidateScope(scope string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if key.scope == scope {
			c.removeLocked(key, entry)
		}
	}
}

func (c *snapshotCache) removeLocked(key snapshotCacheKey, entry *snapshotCacheEntry) {
	delete(c.entries, key)
	c.bytes -= len(entry.body)
	if c.bytes < 0 {
		c.bytes = 0
	}
}

// evictLocked drops least-recently-used entries until the byte budget holds.
func (c *snapshotCache) evictLocked() {
	for c.bytes > c.maxBytes && len(c.entries) > 0 {
		var oldestKey snapshotCacheKey
		var oldest *snapshotCacheEntry
		for key, entry := range c.entries {
			if oldest == nil || entry.lastUsed < oldest.lastUsed {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			return
		}
		c.removeLocked(oldestKey, oldest)
		c.evictions.Add(1)
	}
}

// SnapshotCacheStats is the observable state of the snapshot cache.
type SnapshotCacheStats struct {
	Entries   int
	Bytes     int
	MaxBytes  int
	Hits      uint64
	Misses    uint64
	Stale     uint64
	Evictions uint64
}

func (c *snapshotCache) stats() SnapshotCacheStats {
	if c == nil {
		return SnapshotCacheStats{}
	}
	c.mu.Lock()
	entries, bytes := len(c.entries), c.bytes
	c.mu.Unlock()
	return SnapshotCacheStats{
		Entries:   entries,
		Bytes:     bytes,
		MaxBytes:  c.maxBytes,
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Stale:     c.stale.Load(),
		Evictions: c.evictions.Load(),
	}
}

// snapshotRequestCacheable reports whether a request has the canonical
// cold-start shape the cache keys on. Resume and backfill requests are
// per-client and are served by the uncached path.
func snapshotRequestCacheable(req *foundationpb.ProjectionSnapshotRequest) bool {
	return req.GetSinceWatermark() == "" && req.GetCursor() == ""
}
