package database

import "testing"

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
