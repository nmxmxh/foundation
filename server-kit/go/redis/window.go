package redis

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Bounded ranked windows: the two helpers here are the only sanctioned way to
// grow a sorted set from application code. Sorted-set members carry no TTL of
// their own, so a ZADD without a matching trim is unbounded growth that waits
// for maxmemory and then lets allkeys-lfu evict whatever it likes, including
// the index itself. docs/redis_practices.md "Bounded ranked windows" states
// the rules; redis_practices_check enforces that every file adding to a
// sorted set also trims it.

// ErrRankedUnsupported reports a client without the sorted-set surface a
// ranked window needs. Callers on a side-cache path treat it as a miss.
var ErrRankedUnsupported = errors.New("redis client lacks ranked-set support")

const (
	defaultWindowTrimBatch  = 5_000
	defaultWindowTrimPasses = 4
)

// WindowTrim bounds a ranked index to its newest or highest Keep members.
type WindowTrim struct {
	// Keep is the number of highest-ranked members that survive. Zero or
	// negative empties the index.
	Keep int
	// PayloadKey maps an evicted member to the key holding its row. Leave nil
	// for an index whose members are self-contained or whose payload lifetime
	// is decided by another index (a facet over a master feed).
	PayloadKey func(member string) string
	// BatchSize bounds one eviction pass; MaxPasses bounds the passes per
	// call. A backlog larger than BatchSize x MaxPasses drains on later calls
	// instead of in one unbounded read and DEL.
	BatchSize int
	MaxPasses int
}

// TrimRankedWindow enforces opts.Keep on the sorted set at key and reports how
// many members it removed.
//
// With a PayloadKey, the order is what makes the trim orphan-free without a
// script: the evicted payloads are unlinked first, and only then are the exact
// same members removed from the index. A failure between the two leaves
// members whose payloads are gone, which a reader treats as a miss and the
// next trim removes again; it never leaves a payload no index points to. The
// opposite order (rank trim, then DEL) stranded payloads for their whole TTL
// whenever the DEL failed or timed out, and was the 2026-08-24 eviction
// spiral in pronto.
func TrimRankedWindow(ctx context.Context, client Client, key string, opts WindowTrim) (int64, error) {
	zc, ok := client.(SortedSetClient)
	if !ok {
		return 0, ErrRankedUnsupported
	}
	keep := int64(max(opts.Keep, 0))
	if opts.PayloadKey == nil {
		return zc.ZRemRangeByRank(ctx, key, 0, -(keep + 1))
	}
	batch := int64(opts.BatchSize)
	if batch <= 0 {
		batch = defaultWindowTrimBatch
	}
	passes := opts.MaxPasses
	if passes <= 0 {
		passes = defaultWindowTrimPasses
	}
	var removed int64
	for range passes {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		victims, err := zc.ZRevRange(ctx, key, keep, keep+batch-1)
		if err != nil {
			return removed, fmt.Errorf("read evicted members of %s: %w", key, err)
		}
		if len(victims) == 0 {
			return removed, nil
		}
		payloads := make([]string, 0, len(victims))
		for _, member := range victims {
			payloads = append(payloads, opts.PayloadKey(member))
		}
		if err := client.Del(ctx, payloads...); err != nil {
			// Members stay indexed so the next trim retries the unlink.
			return removed, fmt.Errorf("unlink evicted payloads of %s: %w", key, err)
		}
		n, err := zc.ZRem(ctx, key, victims...)
		removed += n
		if err != nil {
			return removed, fmt.Errorf("remove evicted members of %s: %w", key, err)
		}
		if int64(len(victims)) < batch {
			return removed, nil
		}
	}
	return removed, nil
}

// BumpRanked adds delta to member's score in the ranked counter at key, renews
// the set's TTL, and caps it at keep members, returning the new score.
//
// It replaces the per-member INCR-then-ZADD pattern, which needs a counter key
// per member (each with its own TTL that is easy to forget) and one round trip
// per unit of quantity. Trimming drops the lowest scores, so a ranked counter
// keeps exactly the members a top-N read can return. A failed TTL renewal or
// trim is reported after the increment has landed; the set is a side cache,
// so callers log it rather than fail the request that produced the demand.
func BumpRanked(ctx context.Context, client Client, key, member string, delta float64, ttl time.Duration, keep int) (float64, error) {
	counter, ok := client.(RankedCounterClient)
	if !ok {
		return 0, ErrRankedUnsupported
	}
	if ttl <= 0 || keep <= 0 {
		return 0, fmt.Errorf("ranked counter %s needs a positive ttl and keep", key)
	}
	score, err := counter.ZIncrBy(ctx, key, delta, member)
	if err != nil {
		return 0, err
	}
	if _, err := client.Expire(ctx, key, ttl); err != nil {
		return score, fmt.Errorf("renew ttl of %s: %w", key, err)
	}
	if _, err := TrimRankedWindow(ctx, client, key, WindowTrim{Keep: keep}); err != nil {
		return score, err
	}
	return score, nil
}
