package database

import (
	"context"
	"strings"
	"testing"
)

func TestPostgresBulkHelpersValidateInputs(t *testing.T) {
	var db *PostgresDB
	if _, err := db.CopyFromRows(context.Background(), []string{"items"}, []string{"id"}, [][]any{{1}}); err == nil {
		t.Fatal("CopyFromRows nil db error = nil")
	}
	db = &PostgresDB{}
	if _, err := db.CopyFromRows(context.Background(), nil, []string{"id"}, [][]any{{1}}); err == nil {
		t.Fatal("CopyFromRows nil table path error = nil")
	}
	if err := db.SendBatch(context.Background(), nil, nil); err == nil {
		t.Fatal("SendBatch nil db pool error = nil")
	}
}

// TestBuildBatchUpsertInputDedupesKeepLast pins the batch-input builder: the
// validated array form preserves input order, deduplicates identities
// keep-last (the sequential UpsertRecord outcome), maps every input position —
// including duplicates — to its identity's slot, and rejects invalid records.
func TestBuildBatchUpsertInputDedupesKeepLast(t *testing.T) {
	mk := func(id, state string) DomainRecord {
		return DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: id,
			Data: RecordData{{Name: "state", Value: StringValue(state)}},
		}
	}
	input, err := buildBatchUpsertInput([]DomainRecord{
		mk("dish_1", "draft"),
		mk("dish_2", "published"),
		mk("dish_1", "published"), // duplicate identity: keep-last
	})
	if err != nil {
		t.Fatalf("buildBatchUpsertInput() error = %v", err)
	}
	if len(input.domains) != 2 || len(input.out) != 3 {
		t.Fatalf("arrays=%d out=%d, want 2 deduped rows for 3 inputs", len(input.domains), len(input.out))
	}
	if input.recordIDs[0] != "dish_1" || input.recordIDs[1] != "dish_2" {
		t.Fatalf("recordIDs = %v, want first-seen order", input.recordIDs)
	}
	// The duplicate's later payload must have replaced the earlier one.
	if !strings.Contains(input.payloads[0], `"state":"published"`) || strings.Contains(input.payloads[0], "draft") {
		t.Fatalf("dish_1 payload = %s, want keep-last published", input.payloads[0])
	}
	// Both dish_1 positions map to slot 0; dish_2 maps to slot 1.
	if input.rowFor[0] != 0 || input.rowFor[2] != 0 || input.rowFor[1] != 1 {
		t.Fatalf("rowFor = %v, want [0 1 0]", input.rowFor)
	}

	// Invalid record rejected before any SQL.
	if _, err := buildBatchUpsertInput([]DomainRecord{{Domain: "", Collection: "dishes", OrganizationID: "org_1", RecordID: "x"}}); err == nil {
		t.Fatal("invalid record error = nil")
	}
}

// TestUpsertRecordsBatchGuardsAndConnLease covers the batch entry guards and
// the by-value connection lease that replaced the per-op capture closure: the
// zero value and partially-populated leases must be safe to release on every
// exit path.
func TestUpsertRecordsBatchGuardsAndConnLease(t *testing.T) {
	var db *PostgresDB
	if _, err := db.UpsertRecordsBatch(context.Background(), []DomainRecord{{Domain: "d"}}); err == nil {
		t.Fatal("nil db error = nil")
	}
	db = &PostgresDB{}
	if _, err := db.UpsertRecordsBatch(context.Background(), []DomainRecord{{Domain: "d"}}); err == nil {
		t.Fatal("nil pool error = nil")
	}
	// Pool guard precedes the empty-batch fast path, matching the sibling
	// helpers: a pool-less store errors even for an empty batch.
	if _, err := db.UpsertRecordsBatch(context.Background(), nil); err == nil {
		t.Fatal("empty batch on pool-less store error = nil")
	}

	// Zero-value lease: both fields nil, release must be a safe no-op.
	connLease{}.release()
	// Cancel-only lease: cancel must run exactly once through release.
	ran := 0
	connLease{queryCancel: func() { ran++ }}.release()
	if ran != 1 {
		t.Fatalf("lease cancel ran %d times, want 1", ran)
	}
	// db-only lease with nil pool: recordPoolPressure guard must hold.
	connLease{db: &PostgresDB{}}.release()
}

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
