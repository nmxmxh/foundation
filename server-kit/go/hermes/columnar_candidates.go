package hermes

import (
	"cmp"
	"time"
)

// trackColumnarOrder proves when reverse publication order also matches canonical columnar order.
// Repeated versions or decreasing timestamps require selection over all candidates until the next registry rebuild.
func (r *partitionRegistry) trackColumnarOrder(updatedAt time.Time, version uint64) {
	if r.columnarUnordered.Load() {
		return
	}
	if version <= r.lastColumnarVer || updatedAt.Before(r.lastColumnarTime) {
		r.columnarUnordered.Store(true)
		return
	}
	r.lastColumnarTime = updatedAt
	r.lastColumnarVer = version
}

// entryCandidates retains immutable entries without copying full records during selection and sorting.
type entryCandidates struct {
	entries []*recordEntry
	limit   int
}

// add maintains a bounded heap with the worst retained entry at its root.
func (c *entryCandidates) add(entry *recordEntry) {
	if c.limit <= 0 {
		c.entries = append(c.entries, entry)
		return
	}
	if len(c.entries) < c.limit {
		c.entries = append(c.entries, entry)
		for child := len(c.entries) - 1; child > 0; {
			parent := (child - 1) / 2
			if compareColumnarEntries(c.entries[child], c.entries[parent]) <= 0 {
				break
			}
			c.entries[parent], c.entries[child] = c.entries[child], c.entries[parent]
			child = parent
		}
		return
	}
	if compareColumnarEntries(entry, c.entries[0]) >= 0 {
		return
	}
	c.entries[0] = entry
	c.restoreRoot()
}

func (c *entryCandidates) restoreRoot() {
	for parent := 0; parent < len(c.entries)/2; {
		child := 2*parent + 1
		if right := child + 1; right < len(c.entries) && compareColumnarEntries(c.entries[right], c.entries[child]) > 0 {
			child = right
		}
		if compareColumnarEntries(c.entries[parent], c.entries[child]) >= 0 {
			return
		}
		c.entries[parent], c.entries[child] = c.entries[child], c.entries[parent]
		parent = child
	}
}

// compareColumnarEntries orders newest timestamps first, then newest versions, then ascending record identifiers.
func compareColumnarEntries(a, b *recordEntry) int {
	if order := b.record.UpdatedAt.Compare(a.record.UpdatedAt); order != 0 {
		return order
	}
	if order := cmp.Compare(b.version, a.version); order != 0 {
		return order
	}
	return cmp.Compare(a.record.RecordID, b.record.RecordID)
}
