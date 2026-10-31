package hermes

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestNormalizeSpecBoundsRangeIndexes(t *testing.T) {
	spec, err := normalizeSpec(ProjectionSpec{
		Name: "range_bounds", Domain: "signals", Collection: "ticks",
		RangeIndexedFields: []string{"price", "bucket", "price", " ", "ordinal"},
		MaxRangeIndexes:    2,
	})
	if err != nil {
		t.Fatalf("normalizeSpec: %v", err)
	}
	if len(spec.RangeIndexedFields) != 2 || spec.RangeIndexedFields[0] != "price" || spec.RangeIndexedFields[1] != "bucket" {
		t.Fatalf("bounded range fields = %v, want [price bucket]", spec.RangeIndexedFields)
	}
}

// TestNormalizeRecordDataWithOrganizationDoesNotMutateCaller guards the
// aliasing bug fixed in d907b4f: the helper wrote the org value into, and
// inserted fields into, the caller's slice. Callers reuse that slice.
func TestNormalizeRecordDataWithOrganizationDoesNotMutateCaller(t *testing.T) {
	withOrg := database.RecordDataFromPairs(
		database.RecordField{Name: "a", Value: database.StringValue("1")},
		database.RecordField{Name: "organization_id", Value: database.StringValue("org-old")},
	)
	withOrgBefore := withOrg.Normalize()
	_ = normalizeRecordDataWithOrganization(withOrg, "org-new")
	if !withOrg[1].Value.Equal(withOrgBefore[1].Value) {
		t.Fatalf("caller organization_id was overwritten: %+v", withOrg)
	}

	backing := make(database.RecordData, 2, 8)
	backing[0] = database.RecordField{Name: "a", Value: database.StringValue("1")}
	backing[1] = database.RecordField{Name: "z", Value: database.StringValue("2")}
	spare := backing[:3]
	spare[2] = database.RecordField{Name: "sentinel", Value: database.StringValue("keep")}
	out := normalizeRecordDataWithOrganization(backing, "org-1")
	if len(backing) != 2 || backing[0].Name != "a" || backing[1].Name != "z" {
		t.Fatalf("caller slice changed: %+v", backing)
	}
	if spare[2].Name != "sentinel" {
		t.Fatalf("insert wrote into the caller's spare capacity: %+v", spare)
	}
	if len(out) != 3 || out[1].Name != "organization_id" {
		t.Fatalf("result = %+v, want organization_id inserted", out)
	}
}
