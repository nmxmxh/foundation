package runtimeconfig

import (
	"fmt"
	"hash/fnv"
	"net/netip"
	"slices"
	"strings"
)

// The edge region table is the geometric half of placement.
//
// Network distance is the one locality signal available before any work has
// run: closeness in IP space is a usable prior for "which edge should execute
// or store this". The table maps CIDR prefixes to edge regions so callers can
// derive two things from a peer address:
//
//  1. an affinity key — the stable Bloom input lanes publish and requests
//     carry, letting the dispatch argmin prefer near lanes;
//  2. the advisory jurisdiction of that edge, used when registering lanes,
//     never as an authorization fact.
//
// Two rules keep the geometry honest. The lookup is longest-prefix-match, so
// a narrow deployment prefix always beats its covering aggregate. And the key
// is only ever a hint: measured EWMA latency outranks it because the affinity
// bonus stays far below one queueing interval of any healthy lane. A wrong
// guess self-corrects; nothing here decides authorization or compliance —
// request jurisdictions come from tenant metadata and fail closed.

// EdgeRegion binds one edge placement region to its network prefixes.
type EdgeRegion struct {
	RegionID     string   `json:"region_id"`
	Jurisdiction uint16   `json:"jurisdiction,omitempty"`
	CIDRs        []string `json:"cidrs"`
}

// EdgeRegionTable is the validated, prefix-sorted lookup index.
type EdgeRegionTable struct {
	entries []edgePrefixEntry
	regions map[string]EdgeRegion
}

type edgePrefixEntry struct {
	prefix       netip.Prefix
	regionID     string
	jurisdiction uint16
}

// ParseEdgeRegionTable validates regions and builds the lookup index.
//
// Prefixes are masked on ingest so host bits cannot silently differ between
// publishers of the same range, and duplicate prefixes are refused: two
// regions claiming one network is a configuration fight that would otherwise
// be resolved by sort order instead of by review.
func ParseEdgeRegionTable(regions []EdgeRegion) (*EdgeRegionTable, error) {
	if len(regions) == 0 {
		return nil, fmt.Errorf("edge region table requires at least one region")
	}
	table := &EdgeRegionTable{regions: make(map[string]EdgeRegion, len(regions))}
	seen := make(map[string]string)
	for _, region := range regions {
		id := strings.TrimSpace(region.RegionID)
		if id == "" {
			return nil, fmt.Errorf("edge region id is required")
		}
		if _, exists := table.regions[id]; exists {
			return nil, fmt.Errorf("edge region %q declared twice", id)
		}
		if len(region.CIDRs) == 0 {
			return nil, fmt.Errorf("edge region %q requires at least one cidr", id)
		}
		for _, raw := range region.CIDRs {
			cidr := strings.TrimSpace(raw)
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				return nil, fmt.Errorf("edge region %q cidr %q: %w", id, raw, err)
			}
			masked := prefix.Masked()
			key := masked.String()
			if owner, exists := seen[key]; exists {
				return nil, fmt.Errorf("cidr %s claimed by both %q and %q", key, owner, id)
			}
			seen[key] = id
			table.entries = append(table.entries, edgePrefixEntry{
				prefix:       masked,
				regionID:     id,
				jurisdiction: region.Jurisdiction,
			})
		}
		table.regions[id] = EdgeRegion{RegionID: id, Jurisdiction: region.Jurisdiction, CIDRs: region.CIDRs}
	}
	// Longest prefix first; equal bits break on region id so the match is
	// deterministic regardless of declaration order.
	slices.SortStableFunc(table.entries, func(a, b edgePrefixEntry) int {
		if a.prefix.Bits() != b.prefix.Bits() {
			return b.prefix.Bits() - a.prefix.Bits()
		}
		return strings.Compare(a.regionID, b.regionID)
	})
	return table, nil
}

// Lookup returns the region whose prefix most narrowly contains addr.
//
// Address families never mix: a v6 entry cannot match a v4 address and vice
// versa. No match returns false, and callers must treat that as "no geometric
// opinion", not as a default region.
func (t *EdgeRegionTable) Lookup(addr netip.Addr) (EdgeRegion, bool) {
	if t == nil || !addr.IsValid() {
		return EdgeRegion{}, false
	}
	unmapped := addr.Unmap()
	for _, entry := range t.entries {
		if entry.prefix.Contains(unmapped) {
			return t.regions[entry.regionID], true
		}
	}
	return EdgeRegion{}, false
}

// AffinityKeyForRegion derives the stable Bloom locality key for a region.
//
// Stable across processes and restarts — it hashes the region id, not a
// pointer or an index — so a lane's published Bloom set and a later request
// agree without shared state.
func AffinityKeyForRegion(regionID string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte("ovasabi:edge-region:"))
	_, _ = hasher.Write([]byte(regionID))
	return hasher.Sum64()
}

// AffinityKeyForIP resolves addr through the table and returns its key.
func (t *EdgeRegionTable) AffinityKeyForIP(addr netip.Addr) (uint64, bool) {
	region, ok := t.Lookup(addr)
	if !ok {
		return 0, false
	}
	return AffinityKeyForRegion(region.RegionID), true
}

// Regions returns the validated region set sorted by id for stable output.
func (t *EdgeRegionTable) Regions() []EdgeRegion {
	if t == nil || len(t.regions) == 0 {
		return nil
	}
	out := make([]EdgeRegion, 0, len(t.regions))
	for _, region := range t.regions {
		out = append(out, region)
	}
	slices.SortFunc(out, func(a, b EdgeRegion) int { return strings.Compare(a.RegionID, b.RegionID) })
	return out
}
