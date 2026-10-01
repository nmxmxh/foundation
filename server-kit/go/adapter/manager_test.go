package adapter

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockProvider struct {
	name   string
	kind   Kind
	health Health
	closed bool
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Kind() Kind   { return m.kind }
func (m *mockProvider) Probe(_ context.Context) (Health, error) {
	if m.health == HealthNotServing {
		return m.health, errors.New("failing probe")
	}
	return m.health, nil
}
func (m *mockProvider) Status() Status {
	return Status{
		Name:       m.name,
		Kind:       m.kind,
		Health:     m.health,
		LastProbed: time.Now(),
	}
}
func (m *mockProvider) Close() error {
	m.closed = true
	return nil
}

func TestManagerRegistration(t *testing.T) {
	mgr := NewManager()

	if err := mgr.Register(nil); !errors.Is(err, ErrNilAdapter) {
		t.Fatalf("expected ErrNilAdapter, got %v", err)
	}

	p1 := &mockProvider{name: "test-push", kind: KindPush, health: HealthServing}
	if err := mgr.Register(p1); err != nil {
		t.Fatalf("failed to register provider: %v", err)
	}

	// Duplicate registration must fail.
	if err := mgr.Register(p1); !errors.Is(err, ErrDuplicateAdapter) {
		t.Fatalf("expected ErrDuplicateAdapter, got %v", err)
	}

	// Empty name must fail.
	pEmpty := &mockProvider{name: "", kind: KindEmail}
	if err := mgr.Register(pEmpty); err == nil {
		t.Fatal("expected error for empty provider name")
	}

	got, ok := mgr.Get("test-push")
	if !ok || got.Name() != "test-push" {
		t.Fatalf("expected provider test-push, got %v", got)
	}

	_, ok = mgr.Get("unknown")
	if ok {
		t.Fatal("expected false for unknown provider")
	}

	list := mgr.List()
	if len(list) != 1 || list[0].Name() != "test-push" {
		t.Fatalf("unexpected list: %v", list)
	}
}

func TestManagerHealthAndStatuses(t *testing.T) {
	mgr := NewManager()

	p1 := &mockProvider{name: "push", kind: KindPush, health: HealthServing}
	p2 := &mockProvider{name: "email", kind: KindEmail, health: HealthDegraded}

	_ = mgr.Register(p1)
	_ = mgr.Register(p2)

	if !mgr.Healthy() {
		t.Fatal("expected manager to be healthy when serving and degraded")
	}

	p3 := &mockProvider{name: "sms", kind: KindSMS, health: HealthNotServing}
	_ = mgr.Register(p3)

	if mgr.Healthy() {
		t.Fatal("expected manager to report unhealthy when one adapter is not serving")
	}

	statuses := mgr.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
	// Verify sorted order.
	if statuses[0].Name != "email" || statuses[1].Name != "push" || statuses[2].Name != "sms" {
		t.Fatalf("statuses not sorted alphabetically: %v", statuses)
	}

	probed := mgr.ProbeAll(context.Background())
	if len(probed) != 3 {
		t.Fatalf("expected 3 probed results, got %d", len(probed))
	}
	if probed["push"] != HealthServing || probed["email"] != HealthDegraded || probed["sms"] != HealthNotServing {
		t.Fatalf("unexpected probe outcomes: %v", probed)
	}
}

func TestManagerClose(t *testing.T) {
	mgr := NewManager()

	p1 := &mockProvider{name: "p1", kind: KindOAuth, health: HealthServing}
	p2 := &mockProvider{name: "p2", kind: KindMessaging, health: HealthServing}

	_ = mgr.Register(p1)
	_ = mgr.Register(p2)

	if err := mgr.Close(); err != nil {
		t.Fatalf("failed to close manager: %v", err)
	}

	if !p1.closed || !p2.closed {
		t.Fatalf("providers not closed: p1=%v, p2=%v", p1.closed, p2.closed)
	}

	if len(mgr.List()) != 0 {
		t.Fatal("manager should have no active providers after close")
	}
}

func TestHealthStringAndOK(t *testing.T) {
	tests := []struct {
		h      Health
		name   string
		expect bool
	}{
		{HealthUnknown, "unknown", false},
		{HealthServing, "serving", true},
		{HealthDegraded, "degraded", true},
		{HealthNotServing, "not_serving", false},
		{Health(999), "unknown", false},
	}

	for _, tc := range tests {
		if tc.h.String() != tc.name {
			t.Errorf("expected string %s, got %s", tc.name, tc.h.String())
		}
		if tc.h.OK() != tc.expect {
			t.Errorf("expected OK %v, got %v", tc.expect, tc.h.OK())
		}
	}
}
