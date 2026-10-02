package hermes

import "testing"

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
