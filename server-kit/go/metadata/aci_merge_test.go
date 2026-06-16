package metadata_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/contracttest"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

func TestMergeMapsACILaws(t *testing.T) {
	// ACI property tests for MergeMaps
	merge := func(a, b map[string]any) map[string]any {
		return metadata.MergeMaps(a, b)
	}

	equals := func(a, b map[string]any) bool {
		j1, err1 := json.Marshal(a)
		j2, err2 := json.Marshal(b)
		if err1 != nil || err2 != nil {
			return false
		}
		return bytes.Equal(j1, j2)
	}

	inputs := []map[string]any{
		{
			"tags":            []string{"tag:a"},
			"categories":      []string{"cat:x"},
			"knowledge_graph": "kg_shared",
		},
		{
			// sorted tags for idempotency with self-merge
			"tags":            []string{"tag:a", "tag:b"},
			"categories":      []string{"cat:y"},
			"knowledge_graph": "kg_shared",
		},
		{
			"tags":            []string{"tag:c"},
			"categories":      []string{"cat:x", "cat:z"},
			"knowledge_graph": "kg_shared",
		},
	}

	contracttest.AssertACILaws(t, merge, inputs, equals)
}
