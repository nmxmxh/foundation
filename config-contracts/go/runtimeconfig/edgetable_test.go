package runtimeconfig

import (
	"net/netip"
	"testing"
)

func sampleEdgeRegions() []EdgeRegion {
	return []EdgeRegion{
		{RegionID: "eu-central", Jurisdiction: 7, CIDRs: []string{"10.0.0.0/8"}},
		{RegionID: "eu-frankfurt", Jurisdiction: 7, CIDRs: []string{"10.1.0.0/16"}},
		{RegionID: "us-east", Jurisdiction: 3, CIDRs: []string{"10.2.0.0/16", "2001:db8:1::/48"}},
	}
}

func TestParseEdgeRegionTableBuildsALookupIndex(t *testing.T) {
	table, err := ParseEdgeRegionTable(sampleEdgeRegions())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(table.Regions()); got != 3 {
		t.Fatalf("regions = %d want 3", got)
	}
}

func TestLookupPrefersTheLongestPrefix(t *testing.T) {
	table, err := ParseEdgeRegionTable(sampleEdgeRegions())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	insideNarrow := netip.MustParseAddr("10.1.2.3")
	insideWide := netip.MustParseAddr("10.9.9.9")

	region, ok := table.Lookup(insideNarrow)
	if !ok || region.RegionID != "eu-frankfurt" {
		t.Fatalf("narrow match = %q,%v want eu-frankfurt,true", region.RegionID, ok)
	}
	region, ok = table.Lookup(insideWide)
	if !ok || region.RegionID != "eu-central" {
		t.Fatalf("wide match = %q,%v want eu-central,true", region.RegionID, ok)
	}
}

func TestLookupNeverMixesAddressFamilies(t *testing.T) {
	table, err := ParseEdgeRegionTable(sampleEdgeRegions())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v6 := netip.MustParseAddr("2001:db8:1::5")
	region, ok := table.Lookup(v6)
	if !ok || region.RegionID != "us-east" {
		t.Fatalf("v6 match = %q,%v want us-east,true", region.RegionID, ok)
	}
	outside := netip.MustParseAddr("2001:db8:2::5")
	if _, ok := table.Lookup(outside); ok {
		t.Fatal("outside v6 must not match a v4-only remainder")
	}
}

func TestUnknownAddressesReturnNoGeometricOpinion(t *testing.T) {
	table, err := ParseEdgeRegionTable(sampleEdgeRegions())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := table.Lookup(netip.MustParseAddr("192.168.5.5")); ok {
		t.Fatal("uncovered address must not resolve")
	}
	var invalid netip.Addr
	if _, ok := table.Lookup(invalid); ok {
		t.Fatal("invalid address must not resolve")
	}
	var nilTable *EdgeRegionTable
	if _, ok := nilTable.Lookup(netip.MustParseAddr("10.0.0.1")); ok {
		t.Fatal("nil table must not resolve")
	}
}

func TestAffinityKeysAreStableAndDistinct(t *testing.T) {
	first := AffinityKeyForRegion("eu-central")
	again := AffinityKeyForRegion("eu-central")
	other := AffinityKeyForRegion("us-east")

	if first != again {
		t.Fatalf("key for one region drifted: %d vs %d", first, again)
	}
	if first == other {
		t.Fatal("distinct regions must not share an affinity key")
	}

	table, err := ParseEdgeRegionTable(sampleEdgeRegions())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	key, ok := table.AffinityKeyForIP(netip.MustParseAddr("10.1.2.3"))
	if !ok {
		t.Fatal("covered ip must yield a key")
	}
	if key != AffinityKeyForRegion("eu-frankfurt") {
		t.Fatalf("ip key %d does not match its resolved region", key)
	}
	if _, ok := table.AffinityKeyForIP(netip.MustParseAddr("192.168.5.5")); ok {
		t.Fatal("uncovered ip must yield no key")
	}
}

func TestParseRejectsBrokenTables(t *testing.T) {
	cases := map[string][]EdgeRegion{
		"empty":              {},
		"missing id":         {{CIDRs: []string{"10.0.0.0/8"}}},
		"missing cidrs":      {{RegionID: "solo"}},
		"bad cidr":           {{RegionID: "x", CIDRs: []string{"10.0.0.0/not-bits"}}},
		"duplicate prefix":   {{RegionID: "a", CIDRs: []string{"10.0.0.0/8"}}, {RegionID: "b", CIDRs: []string{"10.0.0.0/8"}}},
		"duplicate region":   {{RegionID: "a", CIDRs: []string{"10.0.0.0/8"}}, {RegionID: "a", CIDRs: []string{"10.1.0.0/16"}}},
		"host bits are kept": {{RegionID: "a", CIDRs: []string{"10.0.0.0/8"}}, {RegionID: "b", CIDRs: []string{"10.0.0.1/8"}}},
	}
	for name, regions := range cases {
		if _, err := ParseEdgeRegionTable(regions); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
}
