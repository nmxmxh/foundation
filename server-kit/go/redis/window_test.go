package redis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// unlinkFailingClient refuses DEL so the trim can be driven through the
// failure between unlinking payloads and removing index members.
type unlinkFailingClient struct {
	Client
	SortedSetClient
	fail bool
}

func (c *unlinkFailingClient) Del(ctx context.Context, keys ...string) error {
	if c.fail {
		return errors.New("unlink refused")
	}
	return c.Client.Del(ctx, keys...)
}

func seedWindow(t *testing.T, client Client, key string, n int) {
	t.Helper()
	ctx := context.Background()
	zc := client.(SortedSetClient)
	for i := range n {
		member := fmt.Sprintf("m%02d", i)
		if err := client.Set(ctx, "row:"+member, "payload", time.Hour); err != nil {
			t.Fatal(err)
		}
		if _, err := zc.ZAdd(ctx, key, float64(i), member); err != nil {
			t.Fatal(err)
		}
	}
}

func rowKey(member string) string { return "row:" + member }

func TestTrimRankedWindowUnlinksPayloadsOfEvictedMembers(t *testing.T) {
	client := NewMemoryClient("window")
	ctx := context.Background()
	seedWindow(t, client, "feed", 10)

	removed, err := TrimRankedWindow(ctx, client, "feed", WindowTrim{Keep: 3, PayloadKey: rowKey, BatchSize: 2, MaxPasses: 10})
	if err != nil || removed != 7 {
		t.Fatalf("removed = %d, err = %v; want 7, nil", removed, err)
	}
	newest, _ := client.(SortedSetClient).ZRevRange(ctx, "feed", 0, -1)
	if fmt.Sprint(newest) != "[m09 m08 m07]" {
		t.Fatalf("survivors = %v, want the three newest", newest)
	}
	for i := range 7 {
		if v, _ := client.Get(ctx, rowKey(fmt.Sprintf("m%02d", i))); len(v) != 0 {
			t.Fatalf("payload of evicted m%02d survived", i)
		}
	}
	if v, _ := client.Get(ctx, rowKey("m09")); len(v) == 0 {
		t.Fatal("payload of a surviving member was unlinked")
	}
}

// The 2026-08-24 spiral: trimming the index before the unlink stranded every
// payload whose DEL failed. Members must stay indexed until the unlink lands.
func TestTrimRankedWindowKeepsMembersWhenUnlinkFails(t *testing.T) {
	mem := NewMemoryClient("window-fail")
	client := &unlinkFailingClient{Client: mem, SortedSetClient: mem.(SortedSetClient), fail: true}
	ctx := context.Background()
	seedWindow(t, mem, "feed", 5)

	if _, err := TrimRankedWindow(ctx, client, "feed", WindowTrim{Keep: 2, PayloadKey: rowKey}); err == nil {
		t.Fatal("a failed unlink must be reported")
	}
	if n, _ := client.ZCard(ctx, "feed"); n != 5 {
		t.Fatalf("index = %d after failed unlink, want 5", n)
	}

	client.fail = false
	if removed, err := TrimRankedWindow(ctx, client, "feed", WindowTrim{Keep: 2, PayloadKey: rowKey}); err != nil || removed != 3 {
		t.Fatalf("retry removed = %d, err = %v; want 3, nil", removed, err)
	}
	for i := range 3 {
		if v, _ := mem.Get(ctx, rowKey(fmt.Sprintf("m%02d", i))); len(v) != 0 {
			t.Fatalf("payload of evicted m%02d survived the retry", i)
		}
	}
}

func TestTrimRankedWindowBoundsPassesPerCall(t *testing.T) {
	client := NewMemoryClient("window-passes")
	ctx := context.Background()
	seedWindow(t, client, "feed", 10)

	removed, err := TrimRankedWindow(ctx, client, "feed", WindowTrim{Keep: 0, PayloadKey: rowKey, BatchSize: 2, MaxPasses: 2})
	if err != nil || removed != 4 {
		t.Fatalf("removed = %d, err = %v; want 4 (2 passes x 2), nil", removed, err)
	}
}

func TestTrimRankedWindowIndexOnly(t *testing.T) {
	client := NewMemoryClient("window-index")
	ctx := context.Background()
	seedWindow(t, client, "facet", 6)
	if removed, err := TrimRankedWindow(ctx, client, "facet", WindowTrim{Keep: 4}); err != nil || removed != 2 {
		t.Fatalf("removed = %d, err = %v; want 2, nil", removed, err)
	}
	// Index-only trims never touch payloads.
	if v, _ := client.Get(ctx, rowKey("m00")); len(v) == 0 {
		t.Fatal("index-only trim unlinked a payload")
	}
}

func TestBumpRankedIncrementsRenewsTTLAndCaps(t *testing.T) {
	client := NewMemoryClient("ranked")
	ctx := context.Background()
	for i, member := range []string{"a", "b", "c", "d"} {
		if _, err := BumpRanked(ctx, client, "trending", member, float64(i+1), time.Hour, 3); err != nil {
			t.Fatal(err)
		}
	}
	score, err := BumpRanked(ctx, client, "trending", "b", 5, time.Hour, 3)
	if err != nil || score != 7 {
		t.Fatalf("score = %v, err = %v; want 7, nil", score, err)
	}
	top, _ := client.(SortedSetClient).ZRevRange(ctx, "trending", 0, -1)
	if fmt.Sprint(top) != "[b d c]" {
		t.Fatalf("ranked = %v, want [b d c] with the lowest score trimmed", top)
	}
	if _, err := BumpRanked(ctx, client, "short", "x", 1, time.Millisecond, 3); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if n, _ := client.(SortedSetClient).ZCard(ctx, "short"); n != 0 {
		t.Fatalf("ranked counter outlived its ttl with %d members", n)
	}
}

func TestBumpRankedRejectsUnboundedCounters(t *testing.T) {
	client := NewMemoryClient("ranked-guard")
	ctx := context.Background()
	if _, err := BumpRanked(ctx, client, "k", "m", 1, 0, 10); err == nil {
		t.Fatal("a ranked counter without a ttl must be refused")
	}
	if _, err := BumpRanked(ctx, client, "k", "m", 1, time.Hour, 0); err == nil {
		t.Fatal("a ranked counter without a cap must be refused")
	}
}
