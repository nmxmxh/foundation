package wsrouting

import (
	"context"
	"strconv"
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

	r := NewRouter(client, "server-1", WithTTL(1*time.Hour))
	if r.ttl != 1*time.Hour {
		t.Errorf("ttl = %v, want %v", r.ttl, 1*time.Hour)
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < 1024; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ids, err := router.ResolveTargets(ctx, target)
		if err != nil {
			b.Fatal(err)
		}
		if len(ids) != 1024 {
			b.Fatalf("resolved %d targets, want 1024", len(ids))
		}
	}
}

func BenchmarkRouterForEachLocal1024(b *testing.B) {
	router := NewRouter(nil, "bench-server")
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
