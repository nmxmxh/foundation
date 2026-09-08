package database

import "testing"

// buildBatchDeleteInput trims identities and drops duplicates, so the arrays
// sent to Postgres carry each identity once. A DELETE cannot match one row
// twice, and the sequential lane's second delete of an identity is a no-op, so
// dropping the duplicate is the same outcome for less work.
func TestBuildBatchDeleteInputTrimsAndDeduplicates(t *testing.T) {
	input := buildBatchDeleteInput([]DomainRecord{
		{Domain: " menu ", Collection: " dishes ", OrganizationID: " org_1 ", RecordID: " dish_1 "},
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1"},
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_2"},
	})
	if len(input.recordIDs) != 2 {
		t.Fatalf("record ids = %v, want the duplicate dropped", input.recordIDs)
	}
	if input.domains[0] != "menu" || input.collections[0] != "dishes" {
		t.Fatalf("identity not trimmed: %q/%q", input.domains[0], input.collections[0])
	}
	if input.organizations[0] != "org_1" || input.recordIDs[0] != "dish_1" {
		t.Fatalf("identity not trimmed: %q/%q", input.organizations[0], input.recordIDs[0])
	}
	if input.recordIDs[1] != "dish_2" {
		t.Fatalf("record ids = %v, want dish_2 second", input.recordIDs)
	}
	// Every array stays the same length, or the unnest would misalign columns.
	if len(input.domains) != 2 || len(input.collections) != 2 || len(input.organizations) != 2 {
		t.Fatalf("array lengths diverged: %d/%d/%d/%d",
			len(input.domains), len(input.collections), len(input.organizations), len(input.recordIDs))
	}
}

// An empty identity component is kept rather than rejected. The single-row
// DeleteRecord trims its arguments and matches nothing when a component is
// empty, so rejecting here would make the batch stricter than the lane it
// refines.
func TestBuildBatchDeleteInputKeepsEmptyIdentityComponents(t *testing.T) {
	input := buildBatchDeleteInput([]DomainRecord{
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "   "},
	})
	if len(input.recordIDs) != 1 || input.recordIDs[0] != "" {
		t.Fatalf("record ids = %v, want one empty entry", input.recordIDs)
	}
}

func TestBuildBatchDeleteInputHandlesEmptyBatch(t *testing.T) {
	input := buildBatchDeleteInput(nil)
	if len(input.domains) != 0 || len(input.recordIDs) != 0 {
		t.Fatalf("empty batch produced %d identities", len(input.domains))
	}
}
