package redis

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
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
	for range 10 {
		if got := client.shard("tenant:123"); got != first {
			t.Fatal("same key should choose the same shard")
		}
	}
}

func TestRedisRelayOwnsOutputClose(t *testing.T) {
	src := make(chan *goredis.Message, 1)
	var closes int32
	out, cancel := relayRedisMessages(context.Background(), src, func() {
		atomic.AddInt32(&closes, 1)
	})

	src <- &goredis.Message{Payload: "payload"}
	select {
	case got := <-out:
		if string(got) != "payload" {
			t.Fatalf("payload = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for relay payload")
	}

	close(src)
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("relay output should close when source closes")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for relay output close")
	}

	cancel()
	cancel()
	if got := atomic.LoadInt32(&closes); got != 1 {
		t.Fatalf("pubsub close count = %d, want 1", got)
	}
}

func TestRedisRelayCancelIsIdempotent(t *testing.T) {
	src := make(chan *goredis.Message)
	var closes int32
	out, cancel := relayRedisMessages(context.Background(), src, func() {
		atomic.AddInt32(&closes, 1)
	})

	cancel()
	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("relay output should close after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for relay output close")
	}
	if got := atomic.LoadInt32(&closes); got != 1 {
		t.Fatalf("pubsub close count = %d, want 1", got)
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
	if id, err := client.XAdd(context.Background(), "stream", Values{Field("x", 1)}); err != nil || id == "" {
		t.Fatalf("XAdd() = %q err=%v", id, err)
	}
	msgs, err := client.XReadGroup(context.Background(), "stream", "group", "consumer", 1)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("XReadGroup() = %+v err=%v", msgs, err)
	}
	if err := client.XAck(context.Background(), "stream", "group", msgs[0].ID); err != nil {
		t.Fatalf("XAck() error = %v", err)
	}
	token, err := client.Lock(context.Background(), "lock", time.Second)
	if err != nil || token == "" {
		t.Fatalf("Lock() = %q err=%v", token, err)
	}
	if ok, err := client.Unlock(context.Background(), "lock", token); err != nil || !ok {
		t.Fatalf("Unlock() = %v err=%v", ok, err)
	}
	if err := client.Set(context.Background(), "k", []byte("v"), time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got, err := client.Get(context.Background(), "k"); err != nil || string(got) != "v" {
		t.Fatalf("Get() = %+v err=%v", got, err)
	}
	if err := client.Del(context.Background(), "k"); err != nil {
		t.Fatalf("Del() error = %v", err)
	}
	if got, err := client.PFAdd(context.Background(), "hll", "a"); err != nil || got != 1 {
		t.Fatalf("PFAdd() = %d err=%v", got, err)
	}
	if got, err := client.PFCount(context.Background(), "hll"); err != nil || got != 1 {
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

func TestMemoryClientPatternSubscribeMatchesQualifiedChannels(t *testing.T) {
	client := NewMemoryClient("app")
	channels, cancel, err := client.PSubscribe(context.Background(), "tenant:*")
	if err != nil {
		t.Fatalf("PSubscribe() error = %v", err)
	}
	defer cancel()

	if err := client.Publish(context.Background(), "tenant:org_0042:signal", []byte("ready")); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case got := <-channels[0]:
		if string(got) != "ready" {
			t.Fatalf("payload = %q, want ready", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pattern payload")
	}

	if err := client.Publish(context.Background(), "other:org_0042:signal", []byte("skip")); err != nil {
		t.Fatalf("Publish(other) error = %v", err)
	}
	select {
	case got := <-channels[0]:
		t.Fatalf("unexpected pattern payload: %q", got)
	case <-time.After(10 * time.Millisecond):
	}
}

func TestMemoryClientSetGetTTLAndCopies(t *testing.T) {
	client := NewMemoryClient("app")
	value := []byte("value")
	if err := client.Set(context.Background(), "cache:key", value, time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value[0] = 'x'
	got, err := client.Get(context.Background(), "cache:key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "value" {
		t.Fatalf("stored value = %q, want value", got)
	}
	got[0] = 'x'
	got, err = client.Get(context.Background(), "cache:key")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if string(got) != "value" {
		t.Fatalf("returned value should be a copy, got %q", got)
	}
	if err := client.Set(context.Background(), "cache:ttl", "gone", time.Nanosecond); err != nil {
		t.Fatalf("Set(ttl) error = %v", err)
	}
	time.Sleep(time.Millisecond)
	if got, err := client.Get(context.Background(), "cache:ttl"); err != nil || got != nil {
		t.Fatalf("expired Get() = %q err=%v, want nil", got, err)
	}
}

func TestMemoryClientSetManyGetMany(t *testing.T) {
	client := NewMemoryClient("app")
	batch, ok := client.(BatchClient)
	if !ok {
		t.Fatal("memory client should implement BatchClient")
	}
	err := batch.SetMany(context.Background(), Values{
		Field("cache:a", []byte("alpha")),
		Field("cache:b", "beta"),
	}, time.Second)
	if err != nil {
		t.Fatalf("SetMany() error = %v", err)
	}
	got, err := batch.GetMany(context.Background(), "cache:a", "cache:b", "cache:missing")
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if string(got["cache:a"]) != "alpha" || string(got["cache:b"]) != "beta" {
		t.Fatalf("GetMany() = %#v", got)
	}
	got["cache:a"][0] = 'x'
	got, err = batch.GetMany(context.Background(), "cache:a")
	if err != nil || string(got["cache:a"]) != "alpha" {
		t.Fatalf("GetMany() copy = %#v err=%v", got, err)
	}
	got, err = batch.SetGetMany(context.Background(), Values{Field("cache:c", "gamma")}, time.Second)
	if err != nil || string(got["cache:c"]) != "gamma" {
		t.Fatalf("SetGetMany() = %#v err=%v", got, err)
	}
	if err := batch.SetMany(context.Background(), Values{Field("cache:ttl", "gone")}, time.Nanosecond); err != nil {
		t.Fatalf("SetMany(ttl) error = %v", err)
	}
	time.Sleep(time.Millisecond)
	got, err = batch.GetMany(context.Background(), "cache:ttl")
	if err != nil || len(got) != 0 {
		t.Fatalf("expired GetMany() = %#v err=%v, want empty", got, err)
	}
}

func TestMemoryClientLocksRequireMatchingTokenAndExpire(t *testing.T) {
	client := NewMemoryClient("app")
	token, err := client.Lock(context.Background(), "resource", 5*time.Millisecond)
	if err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	if _, err := client.Lock(context.Background(), "resource", time.Second); err == nil {
		t.Fatal("expected held lock to reject second caller")
	}
	if ok, err := client.Unlock(context.Background(), "resource", "wrong-token"); err != nil || ok {
		t.Fatalf("Unlock(wrong) = %v err=%v, want false", ok, err)
	}
	if ok, err := client.Unlock(context.Background(), "resource", token); err != nil || !ok {
		t.Fatalf("Unlock(correct) = %v err=%v, want true", ok, err)
	}
	token, err = client.Lock(context.Background(), "resource", time.Nanosecond)
	if err != nil || token == "" {
		t.Fatalf("Lock(expiring) = %q err=%v", token, err)
	}
	time.Sleep(time.Millisecond)
	if token, err = client.Lock(context.Background(), "resource", time.Second); err != nil || token == "" {
		t.Fatalf("Lock(after expiry) = %q err=%v", token, err)
	}
}

func TestMemoryClientStreamsReadGroupsAreMonotonicAndAckable(t *testing.T) {
	client := NewMemoryClient("app")
	firstID, err := client.XAdd(context.Background(), "events", Values{Field("n", 1)})
	if err != nil {
		t.Fatalf("XAdd(first) error = %v", err)
	}
	secondID, err := client.XAdd(context.Background(), "events", Values{Field("n", 2)})
	if err != nil {
		t.Fatalf("XAdd(second) error = %v", err)
	}
	if firstID == secondID {
		t.Fatalf("stream IDs should be unique, got %q", firstID)
	}
	first, err := client.XReadGroup(context.Background(), "events", "workers", "worker-1", 1)
	if err != nil || len(first) != 1 || first[0].ID != firstID {
		t.Fatalf("first read = %+v err=%v, want first message", first, err)
	}
	pending, err := client.XReadGroupPending(context.Background(), "events", "workers", "worker-1", 10)
	if err != nil || len(pending) != 1 || pending[0].ID != firstID {
		t.Fatalf("pending read = %+v err=%v, want first message", pending, err)
	}
	second, err := client.XReadGroup(context.Background(), "events", "workers", "worker-1", 10)
	if err != nil || len(second) != 1 || second[0].ID != secondID {
		t.Fatalf("second read = %+v err=%v, want second message", second, err)
	}
	empty, err := client.XReadGroup(context.Background(), "events", "workers", "worker-1", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty read = %+v err=%v, want no new messages", empty, err)
	}
	if err := client.XAck(context.Background(), "events", "workers", firstID, secondID); err != nil {
		t.Fatalf("XAck() error = %v", err)
	}
	allForNewGroup, err := client.XReadGroup(context.Background(), "events", "auditors", "auditor-1", 10)
	if err != nil || len(allForNewGroup) != 2 {
		t.Fatalf("new group read = %+v err=%v, want both messages", allForNewGroup, err)
	}
}

func TestMemoryClientXAddManyOwnsStreamPayloads(t *testing.T) {
	client := NewMemoryClient("app")
	batch, ok := client.(StreamBatchClient)
	if !ok {
		t.Fatal("memory client should implement StreamBatchClient")
	}
	payload := []byte("one")
	ids, errs := batch.XAddMany(context.Background(), "events", []Values{
		{Field("payload", payload)},
		{Field("payload", []byte("two"))},
	})
	if len(ids) != 2 || len(errs) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("XAddMany ids=%v errs=%v", ids, errs)
	}
	for _, err := range errs {
		if err != nil {
			t.Fatalf("XAddMany unexpected error: %v", err)
		}
	}
	payload[0] = 'x'
	messages, err := client.XReadGroup(context.Background(), "events", "workers", "worker-1", 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("XReadGroup after XAddMany len=%d err=%v", len(messages), err)
	}
	raw, _ := messages[0].Values.Get("payload")
	if got := raw.([]byte); string(got) != "one" {
		t.Fatalf("stream payload was not owned: %q", got)
	}
}

func TestValuesInterfaceSlicePreservesFieldValueOrder(t *testing.T) {
	args := Values{Field("first", "one"), Field("", "skip"), Field("second", []byte("two"))}.InterfaceSlice()
	if len(args) != 4 {
		t.Fatalf("InterfaceSlice len = %d, want 4", len(args))
	}
	if args[0] != "first" || args[1] != "one" || args[2] != "second" || string(args[3].([]byte)) != "two" {
		t.Fatalf("InterfaceSlice args = %#v", args)
	}
}

func TestMemoryClientXAddManyFieldOwnsStreamPayloads(t *testing.T) {
	client := NewMemoryClient("app")
	batch, ok := client.(interface {
		XAddManyField(context.Context, string, string, [][]byte) ([]string, []error)
	})
	if !ok {
		t.Fatal("memory client should implement XAddManyField")
	}
	payload := []byte("one")
	ids, errs := batch.XAddManyField(context.Background(), "events", "payload", [][]byte{payload, []byte("two")})
	if len(ids) != 2 || len(errs) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("XAddManyField ids=%v errs=%v", ids, errs)
	}
	for _, err := range errs {
		if err != nil {
			t.Fatalf("XAddManyField unexpected error: %v", err)
		}
	}
	payload[0] = 'x'
	messages, err := client.XReadGroup(context.Background(), "events", "workers", "worker-1", 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("XReadGroup after XAddManyField len=%d err=%v", len(messages), err)
	}
	raw, _ := messages[0].Values.Get("payload")
	if got := raw.([]byte); string(got) != "one" {
		t.Fatalf("stream payload was not owned: %q", got)
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

func BenchmarkMemoryClientGetHit(b *testing.B) {
	client := NewMemoryClient("bench")
	ctx := context.Background()
	if err := client.Set(ctx, "cache:key", []byte("value"), time.Minute); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	
	for b.Loop() {
		value, err := client.Get(ctx, "cache:key")
		if err != nil || string(value) != "value" {
			b.Fatalf("Get() = %q err=%v", value, err)
		}
	}
}

func BenchmarkMemoryClientSetManyGetMany64(b *testing.B) {
	client := NewMemoryClient("bench")
	batch := client.(BatchClient)
	ctx := context.Background()
	values := make(Values, 0, 64)
	keys := make([]string, 0, 64)
	for i := range 64 {
		key := fmt.Sprintf("cache:%d", i)
		values = append(values, Field(key, []byte("value")))
		keys = append(keys, key)
	}

	b.ReportAllocs()
	
	for b.Loop() {
		if err := batch.SetMany(ctx, values, time.Minute); err != nil {
			b.Fatal(err)
		}
		got, err := batch.GetMany(ctx, keys...)
		if err != nil || len(got) != len(keys) {
			b.Fatalf("GetMany() len=%d err=%v", len(got), err)
		}
	}
}

func BenchmarkMemoryClientSetGetMany64(b *testing.B) {
	client := NewMemoryClient("bench")
	batch := client.(BatchClient)
	ctx := context.Background()
	values, keys := memoryBatchValues(64)

	b.ReportAllocs()
	
	for b.Loop() {
		got, err := batch.SetGetMany(ctx, values, time.Minute)
		if err != nil || len(got) != len(keys) {
			b.Fatalf("SetGetMany() len=%d err=%v", len(got), err)
		}
	}
}

func memoryBatchValues(size int) (Values, []string) {
	values := make(Values, 0, size)
	keys := make([]string, 0, size)
	for i := range size {
		key := fmt.Sprintf("cache:%d", i)
		values = append(values, Field(key, []byte("value")))
		keys = append(keys, key)
	}
	return values, keys
}

func BenchmarkMemoryClientPublish1KSubscribers(b *testing.B) {
	client := NewMemoryClient("bench")
	ctx := context.Background()
	cancelFns := make([]func(), 0, 1000)
	for range 1000 {
		_, cancel, err := client.Subscribe(ctx, "events")
		if err != nil {
			b.Fatal(err)
		}
		cancelFns = append(cancelFns, cancel)
	}
	defer func() {
		for _, cancel := range cancelFns {
			cancel()
		}
	}()

	payload := []byte("event-ready")
	b.ReportAllocs()
	
	for b.Loop() {
		if err := client.Publish(ctx, "events", payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryClientPSubscribePrefix1K(b *testing.B) {
	client := NewMemoryClient("bench")
	ctx := context.Background()
	cancelFns := make([]func(), 0, 1000)
	for i := range 1000 {
		_, cancel, err := client.PSubscribe(ctx, fmt.Sprintf("tenant:org_%04d:*", i))
		if err != nil {
			b.Fatal(err)
		}
		cancelFns = append(cancelFns, cancel)
	}
	defer func() {
		for _, cancel := range cancelFns {
			cancel()
		}
	}()

	payload := []byte("event-ready")
	b.ReportAllocs()
	
	for b.Loop() {
		if err := client.Publish(ctx, "tenant:org_0999:signal", payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryClientStreamXAddReadAck(b *testing.B) {
	client := NewMemoryClient("bench")
	ctx := context.Background()

	b.ReportAllocs()
	
	for i := 0; b.Loop(); i++ {
		id, err := client.XAdd(ctx, "events", Values{Field("n", i)})
		if err != nil {
			b.Fatal(err)
		}
		messages, err := client.XReadGroup(ctx, "events", "workers", "worker-1", 1)
		if err != nil || len(messages) != 1 || messages[0].ID != id {
			b.Fatalf("XReadGroup() = %+v err=%v, want id %s", messages, err, id)
		}
		if err := client.XAck(ctx, "events", "workers", id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryClientLockUnlock(b *testing.B) {
	client := NewMemoryClient("bench")
	ctx := context.Background()

	b.ReportAllocs()
	
	for b.Loop() {
		token, err := client.Lock(ctx, "resource", time.Second)
		if err != nil {
			b.Fatal(err)
		}
		ok, err := client.Unlock(ctx, "resource", token)
		if err != nil || !ok {
			b.Fatalf("Unlock() = %v err=%v", ok, err)
		}
	}
}

// fnvShardIndexOracle reproduces the pre-refactor shard() routing exactly:
// stdlib FNV-1a hasher over []byte(key), then modulo. Parity against it proves
// the allocation-free shardIndex places every key on the same shard the old
// hasher-based implementation did — i.e. the optimization changes cost, not
// placement, so no sharded data is remapped.
func fnvShardIndexOracle(key string, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % n
}

func shardKeyCorpus() []string {
	keys := []string{
		"",
		"a",
		"tenant:123",
		"tenant:org_default:signals:ticks",
		"app:prod:billing:sub:cache:999",
		"ovasabi:prod:media:objects:upload:9f3c",
		"stream:events:ticks",
		"🧵:unicode:key:Ωμέγα",
	}
	for i := range 512 {
		keys = append(keys, fmt.Sprintf("tenant:org_%d:signals:ticks:%d", i, i*7+3))
	}
	return keys
}

// TestShardIndexMatchesStdlibFNVOracle is the no-regression proof: for every key
// in the corpus and every shard count, the inline router must select the exact
// same shard the previous stdlib-hasher router did.
func TestShardIndexMatchesStdlibFNVOracle(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 8, 16, 32, 64, 257, 768} {
		for _, key := range shardKeyCorpus() {
			if got, want := shardIndex(key, n), fnvShardIndexOracle(key, n); got != want {
				t.Fatalf("shardIndex(%q, %d) = %d, stdlib oracle = %d — placement drift", key, n, got, want)
			}
		}
	}
}

// TestShardIndexBounds pins the routing range: an index is always within
// [0, n) and degenerate shard counts collapse to shard 0.
func TestShardIndexBounds(t *testing.T) {
	for _, n := range []int{0, 1, 2, 8, 768} {
		for _, key := range shardKeyCorpus() {
			idx := shardIndex(key, n)
			hi := max(n, 1)
			if idx < 0 || idx >= hi {
				t.Fatalf("shardIndex(%q, %d) = %d, out of [0, %d)", key, n, idx, hi)
			}
		}
	}
}

// TestShardIndexIsDeterministic guards the core routing contract: the same key
// always resolves to the same shard (otherwise reads and writes would diverge).
func TestShardIndexIsDeterministic(t *testing.T) {
	for _, key := range shardKeyCorpus() {
		first := shardIndex(key, 16)
		for range 100 {
			if got := shardIndex(key, 16); got != first {
				t.Fatalf("shardIndex(%q, 16) is non-deterministic: %d != %d", key, got, first)
			}
		}
	}
}

// TestShardIndexDistributesKeys is a sanity check that the reduction spreads a
// realistic tenant keyspace across every shard rather than collapsing onto one.
func TestShardIndexDistributesKeys(t *testing.T) {
	const n = 8
	counts := make([]int, n)
	const keys = 10000
	for i := range keys {
		counts[shardIndex(fmt.Sprintf("tenant:org_%d:signals:ticks", i), n)]++
	}
	for s, c := range counts {
		if c == 0 {
			t.Fatalf("shard %d received no keys; hash is not distributing", s)
		}
		// A uniform hash puts ~keys/n on each shard; flag gross imbalance
		// (more than 2x the fair share) as a distribution regression.
		if c > (keys/n)*2 {
			t.Fatalf("shard %d received %d keys, far above the ~%d fair share", s, c, keys/n)
		}
	}
}

// TestShardIndexDoesNotAllocate is the allocation guard that keeps the router's
// hot path free of the hasher/[]byte churn the previous implementation paid on
// every sharded operation. Mirrors hermes's TestCountIndexedDoesNotAllocate.
func TestShardIndexDoesNotAllocate(t *testing.T) {
	key := "app:prod:billing:sub:cache:999"
	if avg := testing.AllocsPerRun(1000, func() {
		_ = shardIndex(key, 16)
	}); avg != 0 {
		t.Fatalf("shardIndex allocated %.2f times/op, want 0", avg)
	}
}

// BenchmarkShardRouting measures the router's key-routing hot path: the inline
// FNV-1a shardIndex against the previous stdlib-hasher shape. Every sharded
// Get/Set/Incr/XAdd routes through this, so its per-call cost is a tax on every
// sharded operation.
func BenchmarkShardRouting(b *testing.B) {
	keys := map[string]string{
		"short_key": "tenant:123",
		"long_key":  "ovasabi:prod:media:objects:upload:9f3c1e2a-4b8d-11ef-9c7a-0242ac120002:chunk:0007",
	}
	for name, key := range keys {
		b.Run(name+"/inline_fnv1a", func(b *testing.B) {
			b.ReportAllocs()
			var idx int
			for b.Loop() {
				idx = shardIndex(key, 16)
			}
			_ = idx
		})
		b.Run(name+"/stdlib_fnv_hasher", func(b *testing.B) {
			b.ReportAllocs()
			var idx int
			for b.Loop() {
				idx = fnvShardIndexOracle(key, 16)
			}
			_ = idx
		})
	}
}

// TestShardedClientPlacesKeysServiceBacked is the service-backed proof that the
// router's logical placement (shardIndex) matches physical storage: against a
// real multi-shard Redis, a key written through the sharded client is retrievable
// on exactly the shard shardIndex predicts and is absent from every other shard.
// This is the end-to-end check the pure parity test cannot make — it confirms
// the physical clients are indexed in the same order shardIndex assumes.
//
// Skipped unless FOUNDATION_SHARD_REDIS_URLS names >= 2 comma-separated redis
// URLs. Run it against a throwaway cluster, e.g.:
//
//	docker run -d --rm -p 6390:6379 redis:8-alpine
//	docker run -d --rm -p 6391:6379 redis:8-alpine
//	FOUNDATION_SHARD_REDIS_URLS=redis://localhost:6390,redis://localhost:6391 \
//	  go test -run TestShardedClientPlacesKeysServiceBacked ./redis
func TestShardedClientPlacesKeysServiceBacked(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("FOUNDATION_SHARD_REDIS_URLS"))
	if raw == "" {
		t.Skip("set FOUNDATION_SHARD_REDIS_URLS=redis://h1,redis://h2[,...] to run the service-backed shard placement test")
	}
	urls := strings.Split(raw, ",")
	if len(urls) < 2 {
		t.Skipf("need >= 2 shard URLs to exercise routing, got %d", len(urls))
	}

	client, err := ConnectWithOptions(Options{URLs: urls, Prefix: "svc-shardtest", Driver: DriverRedis})
	if err != nil {
		t.Fatalf("connect sharded client: %v", err)
	}
	defer func() { _ = client.Close() }()
	sc, ok := client.(*shardedClient)
	if !ok {
		t.Fatalf("expected *shardedClient, got %T", client)
	}

	ctx := context.Background()
	keys := shardKeyCorpus()[:64]
	placed := make([]int, len(sc.shards))

	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		val := "v-" + key
		if err := client.Set(ctx, key, val, time.Minute); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		want := shardIndex(key, len(sc.shards))
		placed[want]++

		for i, shard := range sc.shards {
			got, getErr := shard.Get(ctx, key)
			if i == want {
				if getErr != nil || string(got) != val {
					t.Fatalf("key %q missing on predicted shard %d: got=%q err=%v", key, want, got, getErr)
				}
				continue
			}
			if getErr == nil && got != nil {
				t.Fatalf("key %q leaked to shard %d (shardIndex predicted %d)", key, i, want)
			}
		}
	}

	used := 0
	for _, c := range placed {
		if c > 0 {
			used++
		}
	}
	if used < 2 {
		t.Fatalf("routed keys only touched %d shard(s); distribution not exercised", used)
	}

	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			_ = client.Del(ctx, key)
		}
	}
}
