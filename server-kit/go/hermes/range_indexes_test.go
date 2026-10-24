package hermes

import (
	"fmt"
	"testing"
)

func TestInlineKeyPrefixOrder(t *testing.T) {
	pairs := [][2]string{
		{"a", "b"},
		{"alpha", "beta"},
		{"key_0001", "key_0002"},
		{"tenant:01", "tenant:02"},
		{"short", "shorter"},
	}

	for _, pair := range pairs {
		left := pair[0]
		right := pair[1]
		leftPrefix := inlineKeyPrefix(left)
		rightPrefix := inlineKeyPrefix(right)

		if leftPrefix < rightPrefix {
			if !(left < right) {
				t.Fatalf("prefix said left < right, but string was not: %s vs %s", left, right)
			}
		} else if leftPrefix > rightPrefix {
			if !(left > right) {
				t.Fatalf("prefix said left > right, but string was not: %s vs %s", left, right)
			}
		} else {
			// Prefix matches on first 8 bytes. Full string comparison must resolve.
			if left == right {
				t.Fatalf("expected different test keys: %s vs %s", left, right)
			}
		}
	}
}

func TestSortRangeEntriesInlinePrefixTieBreak(t *testing.T) {
	entries := make([]rangeIndexEntry, 0, 100)
	for i := 99; i >= 0; i-- {
		key := fmt.Sprintf("rec_%04d", i)
		entries = append(entries, rangeIndexEntry{
			key:     key,
			prefix:  inlineKeyPrefix(key),
			intVal:  42,
			version: uint64(i + 1),
		})
	}

	sortRangeEntries(entries, 'i')

	for i := 0; i < len(entries); i++ {
		expected := fmt.Sprintf("rec_%04d", i)
		if entries[i].key != expected {
			t.Fatalf("expected entry %d to have key %s, got %s", i, expected, entries[i].key)
		}
	}
}

func BenchmarkSortRangeEntries_Comparison(b *testing.B) {
	for _, size := range []int{10_000, 100_000} {
		// Template entries with common intVal to simulate realistic range index cardinality.
		template := make([]rangeIndexEntry, size)
		for i := 0; i < size; i++ {
			key := fmt.Sprintf("tenant:org_%04d:record_%08d", i%100, i)
			template[i] = rangeIndexEntry{
				key:      key,
				prefix:   inlineKeyPrefix(key),
				intVal:   int64(i % 50), // 50 distinct integer values, forcing tie-breaks
				version:  uint64(i + 1),
			}
		}

		b.Run(fmt.Sprintf("inline_prefix/size=%d", size), func(b *testing.B) {
			entries := make([]rangeIndexEntry, size)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				copy(entries, template)
				sortRangeEntries(entries, 'i')
			}
		})

		b.Run(fmt.Sprintf("scalar_string/size=%d", size), func(b *testing.B) {
			entries := make([]rangeIndexEntry, size)
			// Strip prefix to force pure string comparison
			templateScalar := make([]rangeIndexEntry, size)
			copy(templateScalar, template)
			for i := range templateScalar {
				templateScalar[i].prefix = 0
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				copy(entries, templateScalar)
				sortRangeEntries(entries, 'i')
			}
		})
	}
}
