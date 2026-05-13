package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if opts.StatementCacheCapacity <= 0 {
		t.Fatalf("expected statement cache capacity")
	}
	if opts.DescriptionCacheCapacity < 0 {
		t.Fatalf("expected non-negative description cache capacity")
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

func TestApplyPoolOptionsConfiguresPgxPool(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	ApplyPoolOptions(cfg, PoolOptions{
		MaxConns:                 12,
		MinConns:                 3,
		HealthCheckPeriod:        9 * time.Second,
		ConnectTimeout:           4 * time.Second,
		QueryTimeout:             75 * time.Millisecond,
		StatementCacheCapacity:   128,
		DescriptionCacheCapacity: 32,
	})
	if cfg.MaxConns != 12 || cfg.MinConns != 3 || cfg.HealthCheckPeriod != 9*time.Second {
		t.Fatalf("pool sizing not applied: %+v", cfg)
	}
	if cfg.ConnConfig.ConnectTimeout != 4*time.Second || cfg.ConnConfig.StatementCacheCapacity != 128 {
		t.Fatalf("connection options not applied: %+v", cfg.ConnConfig)
	}
	if cfg.ConnConfig.DescriptionCacheCapacity != 32 || cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatalf("cache options not applied: %+v", cfg.ConnConfig)
	}
	if got := cfg.ConnConfig.RuntimeParams["statement_timeout"]; got != "75" {
		t.Fatalf("statement_timeout = %q, want 75", got)
	}
	ApplyPoolOptions(nil, PoolOptions{})
}
