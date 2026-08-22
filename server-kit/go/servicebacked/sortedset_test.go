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
