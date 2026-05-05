package database

import (
	"context"
	"testing"
)

func TestNormalizePoolOptionsDefaults(t *testing.T) {
	opts := normalizePoolOptions(PoolOptions{})
	if opts.MaxConns <= 0 {
		t.Fatalf("expected default max conns")
	}
	if opts.MinConns < 0 {
		t.Fatalf("expected non-negative min conns")
	}
	if opts.ConnectTimeout <= 0 {
		t.Fatalf("expected default connect timeout")
	}
}

func TestNormalizePoolOptionsClampMinToMax(t *testing.T) {
	opts := normalizePoolOptions(PoolOptions{
		MaxConns: 4,
		MinConns: 10,
	})
	if opts.MinConns != opts.MaxConns {
		t.Fatalf("expected min conns to clamp to max conns")
	}
}

func TestDefaultPoolOptionsForLanes(t *testing.T) {
	hotRead := DefaultPoolOptionsFor(RuntimeLaneHotRead)
	background := DefaultPoolOptionsFor(RuntimeLaneBackground)
	analytics := DefaultPoolOptionsFor(RuntimeLaneAnalytics)

	if hotRead.QueryTimeout >= background.QueryTimeout {
		t.Fatalf("hot read query budget should be tighter than background: hot=%s background=%s", hotRead.QueryTimeout, background.QueryTimeout)
	}
	if analytics.MaxConns >= hotRead.MaxConns {
		t.Fatalf("analytics should use fewer DB connections than hot reads: analytics=%d hot=%d", analytics.MaxConns, hotRead.MaxConns)
	}
	if hotRead.AcquireTimeout <= 0 {
		t.Fatalf("expected acquire timeout")
	}
}

func TestQueryBudgetContextUsesDefaultTimeout(t *testing.T) {
	ctx, cancel := QueryBudgetContext(context.TODO(), PoolOptions{})
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatalf("expected query budget deadline")
	}
}
