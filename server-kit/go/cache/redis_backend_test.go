package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func newSharedRedisFixture(t *testing.T) (cacheA, cacheB *Cache, client rediskit.Client) {
	t.Helper()
	client = rediskit.NewMemoryClient("cachetest")
	config := Config{Backend: NewRedisBackend(client), DefaultTTL: time.Minute}
	// Two Cache instances model two processes sharing one Redis deployment.
	return New(config), New(config), client
}

func TestRedisBackend_RoundTripAndPatternDelete(t *testing.T) {
	c, _, _ := newSharedRedisFixture(t)
	ctx := context.Background()

	if err := c.Set(ctx, "user:1", "alice"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if err := c.Set(ctx, "user:2", "bob"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	var val string
	if err := c.Get(ctx, "user:1", &val); err != nil || val != "alice" {
		t.Fatalf("Get user:1 = %q, %v", val, err)
	}
	exists, err := c.Exists(ctx, "user:1")
	if err != nil || !exists {
		t.Fatalf("Exists user:1 = %v, %v", exists, err)
	}
	exists, _ = c.Exists(ctx, "user:missing")
	if exists {
		t.Fatal("expected Exists=false for missing key")
	}

	deleted, err := c.DeletePattern(ctx, "user:*")
	if err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted keys, got %d (%v)", len(deleted), deleted)
	}
	if err := c.Get(ctx, "user:1", &val); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after pattern delete, got %v", err)
	}
}

func TestRedisBackend_GetOrSet(t *testing.T) {
	c, _, _ := newSharedRedisFixture(t)
	ctx := context.Background()

	computes := 0
	result, err := GetOrSet(ctx, c, "hot:key", func() (string, error) {
		computes++
		return "value", nil
	})
	if err != nil {
		t.Fatalf("GetOrSet failed: %v", err)
	}
	if result != "value" || computes != 1 {
		t.Fatalf("result=%q computes=%d", result, computes)
	}

	if _, err := GetOrSet(ctx, c, "hot:key", func() (string, error) {
		computes++
		return "", nil
	}); err != nil {
		t.Fatalf("second GetOrSet failed: %v", err)
	}
	if computes != 1 {
		t.Fatalf("expected cache hit, computes=%d", computes)
	}
}

// TestInvalidator_CrossInstanceInvalidation pins the production lesson:
// invalidation registered by one process must reach readers in another.
func TestInvalidator_CrossInstanceInvalidation(t *testing.T) {
	cacheA, cacheB, _ := newSharedRedisFixture(t)
	ctx := context.Background()

	if err := cacheA.Set(ctx, "summary:org-9", "stale"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	invA, invB := NewInvalidator(cacheA), NewInvalidator(cacheB)
	if err := invA.Tag(ctx, "summary:org-9", "summaries"); err != nil {
		t.Fatalf("Tag failed: %v", err)
	}

	// A different process invalidates the tag; instance A must observe it.
	if err := invB.InvalidateTag(ctx, "summaries"); err != nil {
		t.Fatalf("InvalidateTag failed: %v", err)
	}

	var val string
	if err := cacheA.Get(ctx, "summary:org-9", &val); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-process invalidation failed: got %v", err)
	}
}

func TestInvalidator_TagEdgeCases(t *testing.T) {
	c, _, _ := newSharedRedisFixture(t)
	ctx := context.Background()
	inv := NewInvalidator(c)

	if err := inv.Tag(ctx, "", "users"); err != nil {
		t.Fatalf("empty key Tag should no-op: %v", err)
	}
	if err := inv.Tag(ctx, "user:1"); err != nil {
		t.Fatalf("no tags Tag should no-op: %v", err)
	}
	if err := inv.Tag(ctx, "user:1", " ", ""); err != nil {
		t.Fatalf("blank tags should be skipped: %v", err)
	}
	if err := inv.InvalidateTag(ctx, ""); err != nil {
		t.Fatalf("empty tag invalidate should no-op: %v", err)
	}

	// Foreign or corrupt markers are dropped without failing the sweep.
	if err := c.config.Backend.Set(ctx, c.key("__tag__:users:not-hex"), []byte{1}, time.Minute); err != nil {
		t.Fatalf("marker seed failed: %v", err)
	}
	if err := inv.Tag(ctx, "user:1", "users"); err != nil {
		t.Fatalf("Tag failed: %v", err)
	}
	if err := inv.InvalidateTag(ctx, "users"); err != nil {
		t.Fatalf("InvalidateTag with corrupt marker failed: %v", err)
	}
}

func TestInvalidator_BackendFailurePropagates(t *testing.T) {
	c := New(Config{
		Backend:    NewRedisBackend(failingAdminRedis{err: errStub}),
		DefaultTTL: time.Minute,
	})
	ctx := context.Background()
	inv := NewInvalidator(c)

	if err := inv.Tag(ctx, "user:1", "users"); !errors.Is(err, errStub) {
		t.Fatalf("Tag error = %v", err)
	}
	if err := inv.InvalidateTag(ctx, "users"); !errors.Is(err, errStub) {
		t.Fatalf("InvalidateTag error = %v", err)
	}
}

func TestRedisBackend_WithoutAdminCapability(t *testing.T) {
	minimal := &minimalRedisClient{}
	backend := NewRedisBackend(minimal)
	ctx := context.Background()

	// Exists falls back to a GET probe when the client cannot EXISTS.
	if err := backend.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	exists, err := backend.Exists(ctx, "k")
	if err != nil || !exists {
		t.Fatalf("probe Exists = %v, %v", exists, err)
	}
	exists, err = backend.Exists(ctx, "missing")
	if err != nil || exists {
		t.Fatalf("probe Exists missing = %v, %v", exists, err)
	}

	if _, err := backend.DeletePattern(ctx, "k*"); err == nil {
		t.Fatal("pattern deletion must fail loudly without KeyAdminClient")
	}
}

func TestRedisBackend_NilClientPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil client")
		}
	}()
	NewRedisBackend(nil)
}

func TestRedisBackend_ErrorPathsPropagate(t *testing.T) {
	backend := NewRedisBackend(failingAdminRedis{err: errStub})
	ctx := context.Background()

	if _, err := backend.Get(ctx, "k"); !errors.Is(err, errStub) {
		t.Fatalf("Get error = %v", err)
	}
	if err := backend.Set(ctx, "k", []byte("v"), time.Minute); !errors.Is(err, errStub) {
		t.Fatalf("Set error = %v", err)
	}
	if err := backend.Delete(ctx, "k"); !errors.Is(err, errStub) {
		t.Fatalf("Delete error = %v", err)
	}
	if _, err := backend.Exists(ctx, "k"); !errors.Is(err, errStub) {
		t.Fatalf("Exists error = %v", err)
	}
	if _, err := backend.DeletePattern(ctx, "k*"); !errors.Is(err, errStub) {
		t.Fatalf("DeletePattern error = %v", err)
	}
}

func TestCache_DeleteRemovesEntry(t *testing.T) {
	c, _, _ := newSharedRedisFixture(t)
	ctx := context.Background()

	if err := c.Set(ctx, "user:1", "alice"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	var val string
	if err := c.Get(ctx, "user:1", &val); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Delete, got %v", err)
	}
}

func TestInvalidationBus_BroadcastPublishFailure(t *testing.T) {
	bus := NewInvalidationBus(failingAdminRedis{err: errStub}, "", New(Config{Backend: NewMemoryBackend()}))
	if err := bus.BroadcastTag(context.Background(), "summaries"); !errors.Is(err, errStub) {
		t.Fatalf("BroadcastTag error = %v", err)
	}
}

var errStub = errors.New("stub failure")

type failingAdminRedis struct {
	rediskit.Client
	err error
}

func (f failingAdminRedis) Get(context.Context, string) ([]byte, error) {
	return nil, errStub
}

func (f failingAdminRedis) Set(context.Context, string, any, time.Duration) error {
	return errStub
}

func (f failingAdminRedis) Del(context.Context, ...string) error {
	return errStub
}

func (f failingAdminRedis) Exists(context.Context, string) (bool, error) {
	return false, errStub
}

func (f failingAdminRedis) DeletePattern(context.Context, string, int64) ([]string, error) {
	return nil, errStub
}

func (f failingAdminRedis) Publish(context.Context, string, []byte) error {
	return errStub
}

// minimalRedisClient implements only the core Client primitives; it is
// intentionally not a KeyAdminClient.
type minimalRedisClient struct {
	rediskit.Client
	values map[string][]byte
}

func (m *minimalRedisClient) Get(_ context.Context, key string) ([]byte, error) {
	if m.values == nil {
		m.values = map[string][]byte{}
	}
	value, ok := m.values[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value...), nil
}

func (m *minimalRedisClient) Set(_ context.Context, key string, value any, _ time.Duration) error {
	if m.values == nil {
		m.values = map[string][]byte{}
	}
	data, ok := value.([]byte)
	if !ok {
		return errors.New("minimal client stores only bytes")
	}
	m.values[key] = append([]byte(nil), data...)
	return nil
}

func (m *minimalRedisClient) Del(_ context.Context, keys ...string) error {
	for _, key := range keys {
		delete(m.values, key)
	}
	return nil
}
