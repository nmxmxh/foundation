package database

import "testing"

// TestRecordDataUnmarshalNormalizesOnSortedFastPath guards parity with
// Normalize: a sorted payload with empty or padded names must not keep them.
func TestRecordDataUnmarshalNormalizesOnSortedFastPath(t *testing.T) {
	cases := map[string][]string{
		`{"":1,"a":2}`:        {"a"},
		`{" a":1,"b":2}`:      {"a", "b"},
		`{"a":1,"b":2,"c":3}`: {"a", "b", "c"},
	}
	for input, want := range cases {
		var d RecordData
		if err := d.UnmarshalJSON([]byte(input)); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if len(d) != len(want) {
			t.Fatalf("%s: got %d fields %+v, want %v", input, len(d), d, want)
		}
		for i, name := range want {
			if d[i].Name != name {
				t.Fatalf("%s: field %d = %q, want %q", input, i, d[i].Name, name)
			}
		}
	}
}
