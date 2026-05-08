package redis

import (
	"context"
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

func TestMemoryClientPubSubAndPrimitives(t *testing.T) {
	client := NewMemoryClient("app")
	ch, cancel, err := client.Subscribe(context.Background(), "events")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := client.Publish(context.Background(), "events", []byte("payload")); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case got := <-ch:
		if string(got) != "payload" {
			t.Fatalf("payload = %q", string(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for payload")
	}
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("subscription should close after cancel")
	}
	channels, cancelAll, err := client.PSubscribe(context.Background(), "a", "b")
	if err != nil || len(channels) != 2 {
		t.Fatalf("PSubscribe() channels=%d err=%v", len(channels), err)
	}
	cancelAll()
	if _, _, err := client.PSubscribe(context.Background()); err == nil {
		t.Fatal("expected empty psubscribe to fail")
	}
	if got, err := client.Incr(context.Background(), "count"); err != nil || got != 1 {
		t.Fatalf("Incr() = %d err=%v", got, err)
	}
	if ok, err := client.Expire(context.Background(), "count", time.Nanosecond); err != nil || !ok {
		t.Fatalf("Expire() = %v err=%v", ok, err)
	}
	time.Sleep(time.Millisecond)
	if got, err := client.Incr(context.Background(), "count"); err != nil || got != 1 {
		t.Fatalf("expired Incr() = %d err=%v", got, err)
	}
	if id, err := client.XAdd(context.Background(), "stream", map[string]interface{}{"x": 1}); err != nil || id == "" {
		t.Fatalf("XAdd() = %q err=%v", id, err)
	}
	if msgs, err := client.XReadGroup(context.Background(), "stream", "group", "consumer", 1); err != nil || msgs != nil {
		t.Fatalf("XReadGroup() = %+v err=%v", msgs, err)
	}
	if err := client.XAck(context.Background(), "stream", "group", "1"); err != nil {
		t.Fatalf("XAck() error = %v", err)
	}
	if token, err := client.Lock(context.Background(), "lock", time.Second); err != nil || token != "token-lock" {
		t.Fatalf("Lock() = %q err=%v", token, err)
	}
	if ok, err := client.Unlock(context.Background(), "lock", "token-lock"); err != nil || !ok {
		t.Fatalf("Unlock() = %v err=%v", ok, err)
	}
	if err := client.Set(context.Background(), "k", []byte("v"), time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got, err := client.Get(context.Background(), "k"); err != nil || got != nil {
		t.Fatalf("Get() = %+v err=%v", got, err)
	}
	if err := client.Del(context.Background(), "k"); err != nil {
		t.Fatalf("Del() error = %v", err)
	}
	if got, err := client.PFAdd(context.Background(), "hll", "a"); err != nil || got != 1 {
		t.Fatalf("PFAdd() = %d err=%v", got, err)
	}
	if got, err := client.PFCount(context.Background(), "hll"); err != nil || got != 0 {
		t.Fatalf("PFCount() = %d err=%v", got, err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := client.Publish(context.Background(), "events", []byte("x")); err == nil {
		t.Fatal("expected publish after close to fail")
	}
	if _, _, err := client.Subscribe(context.Background(), "events"); err == nil {
		t.Fatal("expected subscribe after close to fail")
	}
}

func TestConnectAndQualifyHelpers(t *testing.T) {
	client, err := Connect("", "", "memory")
	if err != nil || client == nil {
		t.Fatalf("Connect(memory) = %v err=%v", client, err)
	}
	if normalizeDriver(" redis ") != DriverRedis || normalizeDriver("bad") != DriverMemory {
		t.Fatal("normalizeDriver failed")
	}
	opts := normalizeOptions(Options{MinIdle: -1, MaxRetries: -1})
	if opts.MinIdle != 0 || opts.MaxRetries != 0 {
		t.Fatalf("negative options not clamped: %+v", opts)
	}
	mem := NewMemoryClient("app").(*memoryClient)
	if mem.qualify(" app:ready ") != "app:ready" || mem.qualify("ready") != "app:ready" {
		t.Fatal("memory qualify failed")
	}
	redis := &redisClient{prefix: "app"}
	if redis.qualify(" app:ready ") != "app:ready" || redis.qualify("ready") != "app:ready" {
		t.Fatal("redis qualify failed")
	}
	if _, err := newRedisClient(normalizeOptions(Options{Driver: DriverRedis})); err == nil {
		t.Fatal("expected missing redis url to fail")
	}
	if _, err := newShardedClient(normalizeOptions(Options{URLs: []string{" "}, Driver: DriverRedis})); err == nil {
		t.Fatal("expected empty shard urls to fail")
	}
}

func TestMemoryClientPSubscribeRollbackAfterClose(t *testing.T) {
	client := NewMemoryClient("app")
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := client.PSubscribe(context.Background(), "a", "b"); err == nil {
		t.Fatalf("expected PSubscribe after close to fail")
	}
}

func TestShardedClientPFCountEmptyAndClose(t *testing.T) {
	client := &shardedClient{shards: []*redisClient{{prefix: "a"}, {prefix: "b"}}}
	if got, err := client.PFCount(context.Background()); err != nil || got != 0 {
		t.Fatalf("PFCount empty = %d err=%v", got, err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
