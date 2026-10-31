//go:build servicebacked

package servicebacked

import (
	"context"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// Service-backed proof for the ranked lane (TE-38.2): the memory client's
// ordering, window, and trim semantics must hold identically on real Redis,
// including tie-breaking by lexicographic member order.
func TestServiceBackedSortedSetBoundedRecentBuffer(t *testing.T) {
	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer func() { _ = client.Close() }()

	ss, ok := client.(rediskit.SortedSetClient)
	if !ok {
		t.Fatal("live redis driver should implement SortedSetClient")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const key = "signals:recent:sb"
	defer func() { _ = client.Del(ctx, key) }()

	// Two members share a score to pin the lexicographic tie-break.
	if _, err := ss.ZAdd(ctx, key, 100, "sig-late"); err != nil {
		t.Fatalf("zadd late: %v", err)
	}
	if _, err := ss.ZAdd(ctx, key, 100, "sig-tie"); err != nil {
		t.Fatalf("zadd tie: %v", err)
	}
	for i, name := range []string{"sig-a", "sig-b", "sig-mid"} {
		if _, err := ss.ZAdd(ctx, key, float64(i), name); err != nil {
			t.Fatalf("zadd %s: %v", name, err)
		}
	}

	cardinality, err := ss.ZCard(ctx, key)
	if err != nil || cardinality != 5 {
		t.Fatalf("card = %d,%v want 5", cardinality, err)
	}

	// Newest-3: full mirror of the ascending set, so the tied pair orders
	// reverse-lexicographically (tie before late) exactly as Redis does.
	newest, err := ss.ZRevRange(ctx, key, 0, 2)
	if err != nil {
		t.Fatalf("revrange: %v", err)
	}
	wantNewest := []string{"sig-tie", "sig-late", "sig-mid"}
	for i := range wantNewest {
		if newest[i] != wantNewest[i] {
			t.Fatalf("newest[%d] = %q want %q", i, newest[i], wantNewest[i])
		}
	}

	// Trim the buffer to the newest 3.
	removed, err := ss.ZRemRangeByRank(ctx, key, 0, -4)
	if err != nil || removed != 2 {
		t.Fatalf("trim = %d,%v want 2", removed, err)
	}
	if cardinality, _ = ss.ZCard(ctx, key); cardinality != 3 {
		t.Fatalf("post-trim card = %d want 3", cardinality)
	}

	// Rescore moves a member across ranks.
	if _, err := ss.ZAdd(ctx, key, 500, "sig-mid"); err != nil {
		t.Fatalf("rescore mid: %v", err)
	}
	top, _ := ss.ZRevRange(ctx, key, 0, 0)
	if len(top) != 1 || top[0] != "sig-mid" {
		t.Fatalf("after rescore top = %v want sig-mid", top)
	}
}

// Service-backed proof for the bounded-window helpers: ZIncrBy, the TTL a
// ranked counter renews, and the orphan-free payload trim must behave on real
// Redis exactly as the memory driver's unit tests pin them.
func TestServiceBackedRankedWindowHelpers(t *testing.T) {
	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const counter = "demand:ranked:sb"
	defer func() { _ = client.Del(ctx, counter) }()
	for i, member := range []string{"a", "b", "c", "d"} {
		if _, err := rediskit.BumpRanked(ctx, client, counter, member, float64(i+1), time.Minute, 3); err != nil {
			t.Fatalf("bump %s: %v", member, err)
		}
	}
	if score, err := rediskit.BumpRanked(ctx, client, counter, "b", 5, time.Minute, 3); err != nil || score != 7 {
		t.Fatalf("bump b = %v,%v want 7", score, err)
	}
	ss := client.(rediskit.SortedSetClient)
	if top, _ := ss.ZRevRange(ctx, counter, 0, -1); len(top) != 3 || top[0] != "b" || top[1] != "d" || top[2] != "c" {
		t.Fatalf("ranked = %v want [b d c]", top)
	}

	const feed = "signals:window:sb"
	rows := make([]string, 0, 6)
	for i := range 6 {
		member := "row-" + string(rune('a'+i))
		rows = append(rows, "signals:window:row:"+member)
		if err := client.Set(ctx, "signals:window:row:"+member, "payload", time.Minute); err != nil {
			t.Fatalf("set payload: %v", err)
		}
		if _, err := ss.ZAdd(ctx, feed, float64(i), member); err != nil {
			t.Fatalf("zadd: %v", err)
		}
	}
	defer func() { _ = client.Del(ctx, append(rows, feed)...) }()
	removed, err := rediskit.TrimRankedWindow(ctx, client, feed, rediskit.WindowTrim{
		Keep:       2,
		PayloadKey: func(member string) string { return "signals:window:row:" + member },
		BatchSize:  3,
	})
	if err != nil || removed != 4 {
		t.Fatalf("trim = %d,%v want 4", removed, err)
	}
	for _, key := range rows[:4] {
		if v, _ := client.Get(ctx, key); len(v) != 0 {
			t.Fatalf("payload %s survived its eviction", key)
		}
	}
}
