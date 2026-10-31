package worker

import (
	"fmt"
	"testing"
	"time"
)

// TestDedupeSetEvictsOldestAtBound guards the bound: eviction drops the oldest
// key, never a random live one, and the newest keys stay deduped.
func TestDedupeSetEvictsOldestAtBound(t *testing.T) {
	d := newDedupeSet(100)
	now := time.Now().UTC()
	for i := range 250 {
		d.mark(fmt.Sprintf("k-%d", i), now.Add(time.Duration(i)*time.Millisecond))
	}
	if got := d.len(); got != 100 {
		t.Fatalf("len = %d, want 100", got)
	}
	for i := 150; i < 250; i++ {
		if !d.has(fmt.Sprintf("k-%d", i), now) {
			t.Fatalf("recent key k-%d was evicted", i)
		}
	}
	if d.has("k-0", now) || d.has("k-149", now) {
		t.Fatal("oldest keys must be evicted first")
	}
}

func TestDedupeSetRemarkKeepsNewestExpiry(t *testing.T) {
	d := newDedupeSet(10)
	now := time.Now().UTC()
	d.mark("k", now)
	d.mark("k", now.Add(time.Hour))
	d.prune(now.Add(dedupeTTL + time.Minute))
	if !d.has("k", now.Add(dedupeTTL+time.Minute)) {
		t.Fatal("a stale queue slot must not delete the newer mark")
	}
	d.prune(now.Add(dedupeTTL + 2*time.Hour))
	if d.len() != 0 {
		t.Fatalf("len = %d after full expiry, want 0", d.len())
	}
}

func TestJobHealthIsBounded(t *testing.T) {
	e := NewEngine(nil, nil)
	base := time.Now().UTC()
	for i := range maxJobHealthEntries + 500 {
		// No id or correlation: each job gets a CreatedAt-based key.
		e.recordHealth(Job{JobKind: "k", Queue: "q", CreatedAt: base.Add(time.Duration(i))}, JobHealthQueued, nil, time.Time{}, time.Time{})
	}
	e.mu.RLock()
	n := len(e.jobHealth)
	e.mu.RUnlock()
	if n > maxJobHealthEntries {
		t.Fatalf("jobHealth holds %d entries, bound is %d", n, maxJobHealthEntries)
	}
}
