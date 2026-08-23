package redis

import (
	"context"
	"testing"
	"time"
)

// Compile-time assertions: every shipped driver implements the ranked lane.
var (
	_ SortedSetClient = (*memoryClient)(nil)
	_ SortedSetClient = (*redisClient)(nil)
	_ SortedSetClient = (*shardedClient)(nil)
)

func TestMemoryClientSortedSetBoundedRecentBuffer(t *testing.T) {
	base := NewMemoryClient("rank")
	client, ok := base.(SortedSetClient)
	if !ok {
		t.Fatal("memory client should implement SortedSetClient")
	}
	full := Client(base)
	ctx := context.Background()
	const key = "signals:recent"

	// Newest-wins insert order with sequence scores.
	for i := range 5 {
		added, err := client.ZAdd(ctx, key, float64(i), memberFor(i))
		if err != nil {
			t.Fatalf("zadd %d: %v", i, err)
		}
		if added != 1 {
			t.Fatalf("member %d should be newly added", i)
		}
	}

	// Rescoring an existing member reports updated, not added.
	if added, _ := client.ZAdd(ctx, key, 0.5, memberFor(2)); added != 0 {
		t.Fatalf("rescore reported added=%d want 0", added)
	}

	cardinality, err := client.ZCard(ctx, key)
	if err != nil || cardinality != 5 {
		t.Fatalf("card = %d,%v want 5", cardinality, err)
	}

	// Newest-N read: descending score, ties lexicographically descending.
	newest, err := client.ZRevRange(ctx, key, 0, 1)
	if err != nil {
		t.Fatalf("revrange: %v", err)
	}
	wantNewest := []string{memberFor(4), memberFor(3)}
	for i := range wantNewest {
		if newest[i] != wantNewest[i] {
			t.Fatalf("newest[%d] = %q want %q", i, newest[i], wantNewest[i])
		}
	}

	// Trim to a bounded buffer: keep the newest 3, drop ranks [0, -(keep+1)].
	if removed, err := client.ZRemRangeByRank(ctx, key, 0, -4); err != nil || removed != 2 {
		t.Fatalf("trim = %d,%v want 2", removed, err)
	}
	if cardinality, _ = client.ZCard(ctx, key); cardinality != 3 {
		t.Fatalf("post-trim card = %d want 3", cardinality)
	}

	// The trimmed members are gone; survivors keep descending order. Note
	// member "c" was rescored to 0.5 and therefore trimmed with the old lows.
	kept, _ := client.ZRevRange(ctx, key, 0, -1)
	wantKept := []string{memberFor(4), memberFor(3), memberFor(1)}
	for i := range wantKept {
		if kept[i] != wantKept[i] {
			t.Fatalf("kept[%d] = %q want %q", i, kept[i], wantKept[i])
		}
	}

	// Del removes the whole set through the shared primitive.
	if err := full.Del(ctx, key); err != nil {
		t.Fatalf("del: %v", err)
	}
	if cardinality, _ = client.ZCard(ctx, key); cardinality != 0 {
		t.Fatalf("post-del card = %d want 0", cardinality)
	}
}

func TestMemoryClientSortedSetWindowsAndTTL(t *testing.T) {
	base := NewMemoryClient("rank")
	client, ok := base.(SortedSetClient)
	if !ok {
		t.Fatal("memory client should implement SortedSetClient")
	}
	full := Client(base)
	ctx := context.Background()
	const key = "feed"

	for i := range 4 {
		_, err := client.ZAdd(ctx, key, float64(i+1), memberFor(i))
		if err != nil {
			t.Fatalf("zadd %d: %v", i, err)
		}
	}

	// Negative stop counts from the end: everything but the last.
	window, err := client.ZRevRange(ctx, key, 0, -2)
	if err != nil {
		t.Fatalf("revrange negative stop: %v", err)
	}
	if len(window) != 3 || window[0] != memberFor(3) {
		t.Fatalf("window = %v", window)
	}

	// Out-of-range windows are empty, never errors.
	empty, err := client.ZRevRange(ctx, key, 50, 60)
	if err != nil || len(empty) != 0 {
		t.Fatalf("far window = %v,%v want empty", empty, err)
	}

	// Whole-key TTL expiry clears the set like any other structure.
	if _, err := client.ZAdd(ctx, "expiring", 1, "m"); err != nil {
		t.Fatalf("zadd expiring: %v", err)
	}
	if _, err := full.Expire(ctx, "expiring", time.Nanosecond); err != nil {
		t.Fatalf("expire: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if cardinality, _ := client.ZCard(ctx, "expiring"); cardinality != 0 {
		t.Fatalf("expired card = %d want 0", cardinality)
	}
}

func TestMemoryClientSortedSetTieBreakMatchesRedisReverse(t *testing.T) {
	base := NewMemoryClient("rank")
	client := base.(SortedSetClient)
	ctx := context.Background()

	// Equal scores must order reverse-lexicographically under ZRevRange,
	// mirroring the live-server behavior pinned in servicebacked.
	if _, err := client.ZAdd(ctx, "tied", 100, "sig-late"); err != nil {
		t.Fatalf("zadd late: %v", err)
	}
	if _, err := client.ZAdd(ctx, "tied", 100, "sig-tie"); err != nil {
		t.Fatalf("zadd tie: %v", err)
	}

	descending, err := client.ZRevRange(ctx, "tied", 0, -1)
	if err != nil {
		t.Fatalf("revrange: %v", err)
	}
	if len(descending) != 2 || descending[0] != "sig-tie" || descending[1] != "sig-late" {
		t.Fatalf("descending = %v want [sig-tie sig-late]", descending)
	}
}

func memberFor(index int) string {
	return "sig-" + string(rune('a'+index))
}
