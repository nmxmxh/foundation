package worker

import (
	"sync"
	"time"
)

// dedupeTTL is how long a successful idempotency key blocks a replay.
const dedupeTTL = 24 * time.Hour

type dedupeEntry struct {
	key    string
	expiry time.Time
}

// dedupeSet records successful idempotency keys with a fixed TTL.
//
// It has its own lock, so dedup checks do not contend with job health or
// processor registration. Every entry has the same TTL, so insertion order is
// expiry order: a FIFO queue gives O(1) amortized expiry and O(1) eviction at
// the bound. The old map pruned with a full scan under the engine lock and
// evicted random (often live) keys.
type dedupeSet struct {
	mu      sync.Mutex
	entries map[string]time.Time
	order   []dedupeEntry
	head    int
	max     int
}

func newDedupeSet(maxEntries int) *dedupeSet {
	return &dedupeSet{entries: make(map[string]time.Time), max: maxEntries}
}

// has reports whether key succeeded inside its window.
func (d *dedupeSet) has(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	expiry, ok := d.entries[key]
	if !ok {
		return false
	}
	if now.After(expiry) {
		delete(d.entries, key)
		return false
	}
	return true
}

// mark records key as succeeded until now+dedupeTTL.
func (d *dedupeSet) mark(key string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	expiry := now.Add(dedupeTTL)
	d.entries[key] = expiry
	d.order = append(d.order, dedupeEntry{key: key, expiry: expiry})
	d.popLocked(func(dedupeEntry) bool { return len(d.entries) > d.max })
	d.compactLocked()
}

// prune drops every expired key.
func (d *dedupeSet) prune(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.popLocked(func(front dedupeEntry) bool { return now.After(front.expiry) })
	d.compactLocked()
}

// popLocked removes queue entries from the front while more(front) holds.
// A queue entry is stale when its key was marked again later; then only the
// queue slot goes, and the newer map entry stays.
func (d *dedupeSet) popLocked(more func(front dedupeEntry) bool) {
	for d.head < len(d.order) && more(d.order[d.head]) {
		front := d.order[d.head]
		d.order[d.head] = dedupeEntry{}
		d.head++
		if expiry, ok := d.entries[front.key]; ok && expiry.Equal(front.expiry) {
			delete(d.entries, front.key)
		}
	}
}

func (d *dedupeSet) compactLocked() {
	if d.head > 1024 && d.head*2 > len(d.order) {
		n := copy(d.order, d.order[d.head:])
		clear(d.order[n:])
		d.order = d.order[:n]
		d.head = 0
	}
}

func (d *dedupeSet) len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries)
}
