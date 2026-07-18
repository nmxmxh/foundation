package wsrouting

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestNewRouter(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	if r.ServerID() != "server-1" {
		t.Errorf("ServerID() = %q, want %q", r.ServerID(), "server-1")
	}
}

func TestNewRouterWithOptions(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1", WithTTL(1*time.Hour), WithRegistrationBatchSize(64))
	if r.ttl != 1*time.Hour {
		t.Errorf("ttl = %v, want %v", r.ttl, 1*time.Hour)
	}
	if r.registerBatchSize != 64 {
		t.Errorf("registerBatchSize = %d, want 64", r.registerBatchSize)
	}
}

func TestNewRouterEmptyServerID(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "")
	if r.ServerID() == "" {
		t.Error("ServerID() should be auto-generated when empty")
	}
}

func TestRegisterAndGetLocalConnection(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	info := ConnectionInfo{
		ConnectionID: "conn-1",
		DeviceID:     "device-1",
		UserID:       "user-1",
	}

	if err := r.Register(ctx, info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := r.GetLocalConnection("conn-1")
	if !ok {
		t.Fatal("GetLocalConnection() returned false, want true")
	}
	if got.ConnectionID != "conn-1" {
		t.Errorf("ConnectionID = %q, want %q", got.ConnectionID, "conn-1")
	}
	if got.DeviceID != "device-1" {
		t.Errorf("DeviceID = %q, want %q", got.DeviceID, "device-1")
	}
	if got.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-1")
	}
	if got.ServerID != "server-1" {
		t.Errorf("ServerID = %q, want %q", got.ServerID, "server-1")
	}
}

func TestRegisterEmptyConnectionID(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	err := r.Register(ctx, ConnectionInfo{})
	if err == nil {
		t.Error("Register() should fail with empty connection_id")
	}
}

func TestRegisterManyIndexesAndPublishesBatch(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	registered, stop, err := client.Subscribe(ctx, "wsroute:register")
	if err != nil {
		t.Fatalf("Subscribe register channel error = %v", err)
	}
	defer stop()

	r := NewRouter(client, "server-1")
	err = r.RegisterMany(ctx, []ConnectionInfo{
		{ConnectionID: "conn-1", DeviceID: "device-1", UserID: "user-1"},
		{ConnectionID: "conn-2", DeviceID: "device-2", UserID: "user-1"},
		{ConnectionID: "conn-3", DeviceID: "device-3", UserID: "user-2"},
	})
	if err != nil {
		t.Fatalf("RegisterMany() error = %v", err)
	}
	if got := r.LocalConnectionCount(); got != 3 {
		t.Fatalf("LocalConnectionCount = %d, want 3", got)
	}
	userOne := r.GetLocalConnectionsByUser("user-1")
	if len(userOne) != 2 {
		t.Fatalf("user-1 local connections = %d, want 2", len(userOne))
	}
	for i := range 3 {
		select {
		case payload := <-registered:
			if !strings.Contains(string(payload), "server-1") {
				t.Fatalf("register payload %q missing server id", string(payload))
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for register payload %d", i)
		}
	}
}

func TestRegisterManyInvalidBatchDoesNotMutate(t *testing.T) {
	r := NewRouter(nil, "server-1")
	err := r.RegisterMany(context.Background(), []ConnectionInfo{
		{ConnectionID: "conn-1"},
		{},
	})
	if err == nil {
		t.Fatal("RegisterMany should reject empty connection_id")
	}
	if got := r.LocalConnectionCount(); got != 0 {
		t.Fatalf("LocalConnectionCount after invalid batch = %d, want 0", got)
	}
}

func TestRegisterManyChunksCoordinationBatches(t *testing.T) {
	client := &recordingCoordinationClient{}
	r := NewRouter(client, "server-1", WithRegistrationBatchSize(2))
	err := r.RegisterMany(context.Background(), []ConnectionInfo{
		{ConnectionID: "conn-1", DeviceID: "device-1", UserID: "user-1"},
		{ConnectionID: "conn-2", DeviceID: "device-2", UserID: "user-1"},
		{ConnectionID: "conn-3", DeviceID: "device-3", UserID: "user-2"},
		{ConnectionID: "conn-4", DeviceID: "device-4", UserID: "user-2"},
		{ConnectionID: "conn-5", DeviceID: "device-5", UserID: "user-3"},
	})
	if err != nil {
		t.Fatalf("RegisterMany() error = %v", err)
	}
	if got := r.LocalConnectionCount(); got != 5 {
		t.Fatalf("LocalConnectionCount = %d, want 5", got)
	}
	if got, want := client.keyBatches, []int{4, 4, 2}; !intSlicesEqual(got, want) {
		t.Fatalf("key batch sizes = %v, want %v", got, want)
	}
	if got, want := client.payloadBatches, []int{2, 2, 1}; !intSlicesEqual(got, want) {
		t.Fatalf("payload batch sizes = %v, want %v", got, want)
	}
}

func TestGetLocalConnectionByDevice(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-1",
		DeviceID:     "device-1",
	})

	got, ok := r.GetLocalConnectionByDevice("device-1")
	if !ok {
		t.Fatal("GetLocalConnectionByDevice() returned false, want true")
	}
	if got.ConnectionID != "conn-1" {
		t.Errorf("ConnectionID = %q, want %q", got.ConnectionID, "conn-1")
	}
}

type recordingCoordinationClient struct {
	mu             sync.Mutex
	keyBatches     []int
	payloadBatches []int
}

func (c *recordingCoordinationClient) Publish(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (c *recordingCoordinationClient) Subscribe(context.Context, string) (<-chan []byte, func(), error) {
	ch := make(chan []byte)
	close(ch)
	return ch, func() {}, nil
}

func (c *recordingCoordinationClient) PSubscribe(context.Context, ...string) ([]<-chan []byte, func(), error) {
	return nil, func() {}, nil
}

func (c *recordingCoordinationClient) XAdd(context.Context, string, redis.Values) (string, error) {
	return "", nil
}

func (c *recordingCoordinationClient) XReadGroup(context.Context, string, string, string, int64) ([]redis.StreamMessage, error) {
	return nil, nil
}

func (c *recordingCoordinationClient) XReadGroupPending(context.Context, string, string, string, int64) ([]redis.StreamMessage, error) {
	return nil, nil
}

func (c *recordingCoordinationClient) XAck(context.Context, string, string, ...string) error {
	return nil
}

func (c *recordingCoordinationClient) Incr(context.Context, string) (int64, error) {
	return 1, nil
}

func (c *recordingCoordinationClient) Expire(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (c *recordingCoordinationClient) IncrExpireMany(_ context.Context, keys []string, _ time.Duration) []error {
	c.mu.Lock()
	c.keyBatches = append(c.keyBatches, len(keys))
	c.mu.Unlock()
	return make([]error, len(keys))
}

func (c *recordingCoordinationClient) PublishMany(_ context.Context, _ string, payloads [][]byte) []error {
	c.mu.Lock()
	c.payloadBatches = append(c.payloadBatches, len(payloads))
	c.mu.Unlock()
	return make([]error, len(payloads))
}

func (c *recordingCoordinationClient) Lock(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (c *recordingCoordinationClient) Unlock(context.Context, string, string) (bool, error) {
	return true, nil
}

func (c *recordingCoordinationClient) PFAdd(context.Context, string, ...any) (int64, error) {
	return 1, nil
}

func (c *recordingCoordinationClient) PFCount(context.Context, ...string) (int64, error) {
	return 1, nil
}

func (c *recordingCoordinationClient) Set(context.Context, string, any, time.Duration) error {
	return nil
}

func (c *recordingCoordinationClient) Get(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (c *recordingCoordinationClient) Del(context.Context, ...string) error {
	return nil
}

func (c *recordingCoordinationClient) Close() error {
	return nil
}

func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGetLocalConnectionsByUser(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-1",
		DeviceID:     "device-1",
		UserID:       "user-1",
	})
	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-2",
		DeviceID:     "device-2",
		UserID:       "user-1",
	})
	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-3",
		DeviceID:     "device-3",
		UserID:       "user-2",
	})

	conns := r.GetLocalConnectionsByUser("user-1")
	if len(conns) != 2 {
		t.Errorf("got %d connections, want 2", len(conns))
	}
}

func TestUnregister(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-1",
		DeviceID:     "device-1",
	})

	if _, ok := r.GetLocalConnection("conn-1"); !ok {
		t.Fatal("connection should exist after register")
	}

	if err := r.Unregister(ctx, "conn-1"); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}

	if _, ok := r.GetLocalConnection("conn-1"); ok {
		t.Error("connection should not exist after unregister")
	}
}

func TestUpdateAuth(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{
		ConnectionID: "conn-1",
		DeviceID:     "device-1",
	})

	if err := r.UpdateAuth(ctx, "conn-1", "user-1"); err != nil {
		t.Fatalf("UpdateAuth() error = %v", err)
	}

	info, _ := r.GetLocalConnection("conn-1")
	if info.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", info.UserID, "user-1")
	}
}

func TestLocalOnlyRouterBranches(t *testing.T) {
	r := NewRouter(nil, "server-1", WithTTL(0))
	ctx := context.Background()

	if r.ttl != DefaultTTL {
		t.Fatalf("zero TTL option should preserve default, got %s", r.ttl)
	}
	if err := r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1"}); err != nil {
		t.Fatalf("Register() with nil client error = %v", err)
	}
	info, ok := r.GetLocalConnection("conn-1")
	if !ok || info.DeviceID != "conn-1" || info.ServerID != "server-1" || info.ConnectedAt.IsZero() {
		t.Fatalf("defaulted local connection = %+v ok=%v", info, ok)
	}
	info.UserID = "mutated"
	info, _ = r.GetLocalConnection("conn-1")
	if info.UserID == "mutated" {
		t.Fatal("GetLocalConnection should return a copy")
	}
	if err := r.UpdateAuth(ctx, "", "user-1"); err == nil {
		t.Fatal("expected empty connection id update error")
	}
	if err := r.UpdateAuth(ctx, "missing", "user-1"); err != nil {
		t.Fatalf("UpdateAuth missing with nil client error = %v", err)
	}
	if err := r.UpdateAuth(ctx, "conn-1", ""); err != nil {
		t.Fatalf("UpdateAuth blank user with nil client error = %v", err)
	}
	if err := r.Unregister(ctx, ""); err == nil {
		t.Fatal("expected empty unregister error")
	}
	if err := r.Unregister(ctx, "missing"); err != nil {
		t.Fatalf("Unregister missing with nil client error = %v", err)
	}
	if err := r.Unregister(ctx, "conn-1"); err != nil {
		t.Fatalf("Unregister existing with nil client error = %v", err)
	}
}

func TestForEachLocalStopsEarlyAndHealth(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1"})
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-2"})

	var seen int
	r.ForEachLocal(func(info *ConnectionInfo) bool {
		seen++
		info.ConnectionID = "mutated"
		return false
	})
	if seen != 1 {
		t.Fatalf("ForEachLocal seen = %d, want 1", seen)
	}
	if _, ok := r.GetLocalConnection("mutated"); ok {
		t.Fatal("ForEachLocal should pass copies")
	}
	health := r.Health()
	if health.ServerID != "server-1" || health.LocalConnections != 2 || health.Timestamp.IsZero() {
		t.Fatalf("bad health snapshot: %+v", health)
	}
}

func TestForEachLocalValueStopsEarlyAndReturnsCopies(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1"})
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-2"})

	var seen int
	r.ForEachLocalValue(func(info ConnectionInfo) bool {
		seen++
		info.ConnectionID = "mutated"
		return false
	})
	if seen != 1 {
		t.Fatalf("ForEachLocalValue seen = %d, want 1", seen)
	}
	if _, ok := r.GetLocalConnection("mutated"); ok {
		t.Fatal("ForEachLocalValue should pass value copies")
	}
}

func TestLocalConnectionCount(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	if r.LocalConnectionCount() != 0 {
		t.Errorf("LocalConnectionCount() = %d, want 0", r.LocalConnectionCount())
	}

	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1", DeviceID: "d1"})
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-2", DeviceID: "d2"})

	if r.LocalConnectionCount() != 2 {
		t.Errorf("LocalConnectionCount() = %d, want 2", r.LocalConnectionCount())
	}
}

func TestResolveTargets(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1", DeviceID: "device-1", UserID: "user-1"})
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-2", DeviceID: "device-2", UserID: "user-1"})

	tests := []struct {
		target      TargetedDelivery
		expectedLen int
		expectedIDs []string
	}{
		{
			target:      TargetedDelivery{TargetType: "connection", TargetID: "conn-1"},
			expectedLen: 1,
			expectedIDs: []string{"conn-1"},
		},
		{
			target:      TargetedDelivery{TargetType: "device", TargetID: "device-1"},
			expectedLen: 1,
			expectedIDs: []string{"conn-1"},
		},
		{
			target:      TargetedDelivery{TargetType: "user", TargetID: "user-1"},
			expectedLen: 2,
		},
		{
			target:      TargetedDelivery{TargetType: "broadcast"},
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		ids, err := r.ResolveTargets(ctx, tt.target)
		if err != nil {
			t.Errorf("ResolveTargets(%v) error = %v", tt.target.TargetType, err)
			continue
		}
		if len(ids) != tt.expectedLen {
			t.Errorf("ResolveTargets(%v) = %d connections, want %d", tt.target.TargetType, len(ids), tt.expectedLen)
		}
	}
}

func TestForEachTarget(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1", DeviceID: "device-1", UserID: "user-1"})
	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-2", DeviceID: "device-2", UserID: "user-1"})

	var seen []string
	count, err := r.ForEachTarget(ctx, TargetedDelivery{TargetType: "broadcast"}, func(connectionID string) bool {
		seen = append(seen, connectionID)
		return true
	})
	if err != nil || count != 2 || len(seen) != 2 {
		t.Fatalf("ForEachTarget broadcast count=%d seen=%v err=%v", count, seen, err)
	}

	count, err = r.ForEachTarget(ctx, TargetedDelivery{TargetType: "user", TargetID: "user-1"}, func(string) bool {
		return false
	})
	if err != nil || count != 1 {
		t.Fatalf("ForEachTarget stop count=%d err=%v", count, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.ForEachTarget(cancelled, TargetedDelivery{TargetType: "broadcast"}, func(string) bool { return true }); err == nil {
		t.Fatal("expected canceled context error")
	}
	if _, err := r.ForEachTarget(ctx, TargetedDelivery{TargetType: "unknown"}, func(string) bool { return true }); err == nil {
		t.Fatal("expected unknown target error")
	}
}

func TestForEachTargetBatch(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()
	for i := range 5 {
		if err := r.Register(ctx, ConnectionInfo{
			ConnectionID: fmt.Sprintf("conn-%d", i),
			DeviceID:     fmt.Sprintf("device-%d", i),
			UserID:       "user-1",
		}); err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
	}

	var batchLens []int
	count, err := r.ForEachTargetBatch(ctx, TargetedDelivery{TargetType: "broadcast"}, 2, func(ids []string) bool {
		batchLens = append(batchLens, len(ids))
		return true
	})
	if err != nil || count != 5 {
		t.Fatalf("ForEachTargetBatch broadcast count=%d err=%v", count, err)
	}
	if got := fmt.Sprint(batchLens); got != "[2 2 1]" {
		t.Fatalf("batch lengths = %s, want [2 2 1]", got)
	}

	count, err = r.ForEachTargetBatch(ctx, TargetedDelivery{TargetType: "user", TargetID: "user-1"}, 3, func([]string) bool {
		return false
	})
	if err != nil || count != 3 {
		t.Fatalf("ForEachTargetBatch stop count=%d err=%v", count, err)
	}

	count, err = r.ForEachTargetBatch(ctx, TargetedDelivery{TargetType: "device", TargetID: "device-1"}, 0, func(ids []string) bool {
		return len(ids) == 1 && ids[0] == "conn-1"
	})
	if err != nil || count != 1 {
		t.Fatalf("ForEachTargetBatch device count=%d err=%v", count, err)
	}

	if _, err := r.ForEachTargetBatch(ctx, TargetedDelivery{TargetType: "unknown"}, 0, func([]string) bool { return true }); err == nil {
		t.Fatal("expected unknown target error")
	}
	if got := normalizeTargetBatchSize(MaxTargetBatchSize*2, 1_000_000); got != MaxTargetBatchSize {
		t.Fatalf("capped batch size = %d, want %d", got, MaxTargetBatchSize)
	}
	if got := normalizeTargetBatchSize(0, 1_000_000); got != 4096 {
		t.Fatalf("adaptive 1M batch size = %d, want 4096", got)
	}
}

func TestRouterConcurrentChurnMaintainsLocalOwnership(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()
	const connections = 512

	var wg sync.WaitGroup
	wg.Add(connections)
	for i := range connections {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("conn-%03d", i)
			if err := r.Register(ctx, ConnectionInfo{
				ConnectionID: id,
				DeviceID:     fmt.Sprintf("device-%03d", i),
				UserID:       fmt.Sprintf("user-%02d", i%16),
			}); err != nil {
				t.Errorf("register %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	if got := r.LocalConnectionCount(); got != connections {
		t.Fatalf("LocalConnectionCount after register = %d, want %d", got, connections)
	}

	for u := range 16 {
		ids, err := r.ResolveTargets(ctx, TargetedDelivery{TargetType: "user", TargetID: fmt.Sprintf("user-%02d", u)})
		if err != nil {
			t.Fatalf("ResolveTargets user-%02d: %v", u, err)
		}
		if len(ids) != connections/16 {
			t.Fatalf("user-%02d resolved %d connections, want %d", u, len(ids), connections/16)
		}
	}

	wg.Add(connections / 2)
	for i := range connections / 2 {
		go func(i int) {
			defer wg.Done()
			if err := r.Unregister(ctx, fmt.Sprintf("conn-%03d", i)); err != nil {
				t.Errorf("unregister conn-%03d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := r.LocalConnectionCount(); got != connections/2 {
		t.Fatalf("LocalConnectionCount after churn = %d, want %d", got, connections/2)
	}
	ids, err := r.ResolveTargets(ctx, TargetedDelivery{TargetType: "broadcast"})
	if err != nil {
		t.Fatalf("ResolveTargets broadcast: %v", err)
	}
	if len(ids) != connections/2 {
		t.Fatalf("broadcast resolved %d connections, want %d", len(ids), connections/2)
	}
}

func TestHealth(t *testing.T) {
	client := redis.NewMemoryClient("test")
	defer func() { _ = client.Close() }()

	r := NewRouter(client, "server-1")
	ctx := context.Background()

	_ = r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1", DeviceID: "d1"})

	health := r.Health()
	if health.ServerID != "server-1" {
		t.Errorf("ServerID = %q, want %q", health.ServerID, "server-1")
	}
	if health.LocalConnections != 1 {
		t.Errorf("LocalConnections = %d, want 1", health.LocalConnections)
	}
	if health.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestNilClient(t *testing.T) {
	r := NewRouter(nil, "server-1")
	ctx := context.Background()

	// Should not panic with nil client
	err := r.Register(ctx, ConnectionInfo{ConnectionID: "conn-1", DeviceID: "d1"})
	if err != nil {
		t.Errorf("Register() with nil client should succeed, got error: %v", err)
	}

	if r.LocalConnectionCount() != 1 {
		t.Error("Local tracking should work without Redis")
	}
}

func BenchmarkRouterRegisterLocalOnly(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	b.ReportAllocs()
	
	for i := 0; b.Loop(); i++ {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
			UserID:       "user-1",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterResolveTargetsUserLocal(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := range 1024 {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
			UserID:       "user-1",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	target := TargetedDelivery{TargetType: "user", TargetID: "user-1", LocalOnly: true}
	b.ReportAllocs()
	
	for b.Loop() {
		ids, err := router.ResolveTargets(ctx, target)
		if err != nil {
			b.Fatal(err)
		}
		if len(ids) != 1024 {
			b.Fatalf("resolved %d targets, want 1024", len(ids))
		}
	}
}

func BenchmarkRouterResolveTargetsUserSparseLocal(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := range 16384 {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
			UserID:       "user-" + strconv.Itoa(i%1024),
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	target := TargetedDelivery{TargetType: "user", TargetID: "user-777", LocalOnly: true}
	b.ReportAllocs()
	
	for b.Loop() {
		ids, err := router.ResolveTargets(ctx, target)
		if err != nil {
			b.Fatal(err)
		}
		if len(ids) != 16 {
			b.Fatalf("resolved %d targets, want 16", len(ids))
		}
	}
}

func BenchmarkRouterResolveTargetsDeviceLocal(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := range 16384 {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
			UserID:       "user-" + strconv.Itoa(i%1024),
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	target := TargetedDelivery{TargetType: "device", TargetID: "device-8191", LocalOnly: true}
	b.ReportAllocs()
	
	for b.Loop() {
		ids, err := router.ResolveTargets(ctx, target)
		if err != nil {
			b.Fatal(err)
		}
		if len(ids) != 1 {
			b.Fatalf("resolved %d targets, want 1", len(ids))
		}
	}
}

func BenchmarkRouterForEachLocal1024(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := range 1024 {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	
	for b.Loop() {
		count := 0
		router.ForEachLocal(func(*ConnectionInfo) bool {
			count++
			return true
		})
		if count != 1024 {
			b.Fatalf("iterated %d connections, want 1024", count)
		}
	}
}

func BenchmarkRouterForEachLocalValue1024(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := range 1024 {
		id := strconv.Itoa(i)
		err := router.Register(ctx, ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	
	for b.Loop() {
		count := 0
		router.ForEachLocalValue(func(ConnectionInfo) bool {
			count++
			return true
		})
		if count != 1024 {
			b.Fatalf("iterated %d connections, want 1024", count)
		}
	}
}

type mockSingleflightRedisClient struct {
	redis.Client
	mu        sync.Mutex
	incrCount int
	pubCount  int
	delay     time.Duration
}

func (m *mockSingleflightRedisClient) Incr(ctx context.Context, key string) (int64, error) {
	m.mu.Lock()
	m.incrCount++
	m.mu.Unlock()
	time.Sleep(m.delay)
	return 1, nil
}

func (m *mockSingleflightRedisClient) Publish(ctx context.Context, channel string, message []byte) error {
	m.mu.Lock()
	m.pubCount++
	m.mu.Unlock()
	return nil
}

func (m *mockSingleflightRedisClient) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return true, nil
}

func (m *mockSingleflightRedisClient) Close() error { return nil }

func TestRouter_Register_SingleflightCoalescing(t *testing.T) {
	mockClient := &mockSingleflightRedisClient{
		delay: 50 * time.Millisecond,
	}
	r := NewRouter(mockClient, "server-1")
	ctx := context.Background()

	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := range concurrency {
		go func(id int) {
			defer wg.Done()
			err := r.Register(ctx, ConnectionInfo{
				ConnectionID: fmt.Sprintf("conn-%d", id),
				DeviceID:     "device-sf-1",
				UserID:       "user-sf-1",
			})
			if err != nil {
				t.Errorf("Register failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	mockClient.mu.Lock()
	incrs := mockClient.incrCount
	pubs := mockClient.pubCount
	mockClient.mu.Unlock()

	// Since they are concurrent and same DeviceID, singleflight should coalesce them to 1.
	if incrs > 2 { // Give a tiny buffer for scheduling, but normally 1.
		t.Errorf("expected Incr calls to be coalesced, got %d", incrs)
	}
	if pubs > 2 {
		t.Errorf("expected Publish calls to be coalesced, got %d", pubs)
	}
}
