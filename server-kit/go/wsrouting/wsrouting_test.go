package wsrouting

import (
	"context"
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
		target       TargetedDelivery
		expectedLen  int
		expectedIDs  []string
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
