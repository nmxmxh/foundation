package redis

import (
	"testing"
	"time"
)

func TestNormalizeOptionsDefaults(t *testing.T) {
	opts := normalizeOptions(Options{URL: "redis://localhost:6379"})
	if opts.Prefix != "ovasabi" {
		t.Fatalf("prefix = %q", opts.Prefix)
	}
	if len(opts.URLs) != 1 || opts.URLs[0] != opts.URL {
		t.Fatalf("urls not derived from url: %#v", opts)
	}
	if opts.PoolSize <= 0 || opts.DialTimeout <= 0 || opts.ReadTimeout <= 0 || opts.WriteTimeout <= 0 {
		t.Fatalf("expected positive pool/timeouts: %#v", opts)
	}
}

func TestNormalizeOptionsKeepsExplicitShardURLs(t *testing.T) {
	opts := normalizeOptions(Options{
		URL:          "redis://primary:6379",
		URLs:         []string{"redis://shard-a:6379", "redis://shard-b:6379"},
		Prefix:       "app",
		PoolSize:     64,
		MinIdle:      8,
		MaxRetries:   2,
		DialTimeout:  time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	if len(opts.URLs) != 2 || opts.URLs[0] != "redis://shard-a:6379" || opts.URLs[1] != "redis://shard-b:6379" {
		t.Fatalf("explicit shard urls should win: %#v", opts.URLs)
	}
	if opts.Prefix != "app" || opts.PoolSize != 64 || opts.MinIdle != 8 || opts.MaxRetries != 2 {
		t.Fatalf("explicit options not preserved: %#v", opts)
	}
}

func TestShardedClientChoosesStableShard(t *testing.T) {
	client := &shardedClient{
		shards: []*redisClient{{prefix: "app"}, {prefix: "app"}, {prefix: "app"}},
	}
	first := client.shard("tenant:123")
	for i := 0; i < 10; i++ {
		if got := client.shard("tenant:123"); got != first {
			t.Fatal("same key should choose the same shard")
		}
	}
}
