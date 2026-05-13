package degradation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitForMode(t *testing.T, m *Manager, name string, want Mode) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := m.GetMode(name); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("mode = %s, want %s", m.GetMode(name), want)
}

func TestManagerStateAndFallbacks(t *testing.T) {
	checkErr := errors.New("down")
	changes := make(chan Mode, 4)
	m := NewManager()
	defer m.Stop()
	m.Register("redis", Config{
		HealthCheck:      func(context.Context) error { return checkErr },
		CheckInterval:    time.Hour,
		FailureThreshold: 1,
		Timeout:          time.Second,
		FallbackBehavior: FallbackFailOpen,
		OnModeChange: func(_ string, _, newMode Mode) {
			changes <- newMode
		},
	})
	waitForMode(t, m, "redis", ModeUnavailable)
	if m.IsHealthy("redis") || m.IsAvailable("redis") {
		t.Fatal("unavailable dependency should not be healthy or available")
	}
	status := m.GetStatus("redis")
	if status == nil || status.LastError != "down" || status.FallbackBehavior != FallbackFailOpen {
		t.Fatalf("unexpected status: %+v", status)
	}
	if len(m.AllStatus()) != 1 || m.GetStatus("missing") != nil {
		t.Fatal("all/missing status mismatch")
	}
	select {
	case <-changes:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected mode change callback")
	}

	called := false
	err := m.Execute(context.Background(), "redis", func() error { return nil }, func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("expected fallback success, err=%v called=%v", err, called)
	}
	if err := m.Execute(context.Background(), "missing", func() error { return nil }, nil); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestManagerStopIsIdempotent(t *testing.T) {
	m := NewManager()
	m.Stop()
	m.Stop()
}

func TestWithFallbackAndSentinel(t *testing.T) {
	m := NewManager()
	defer m.Stop()
	m.Register("cache", Config{
		HealthCheck:       func(context.Context) error { return nil },
		CheckInterval:     time.Hour,
		RecoveryThreshold: 1,
	})
	waitForMode(t, m, "cache", ModeNormal)
	wrapped := WithFallback(m, "cache",
		func() (string, error) { return "primary", nil },
		func() (string, error) { return "fallback", nil },
	)
	if got, err := wrapped(); err != nil || got != "primary" {
		t.Fatalf("primary = %q err=%v", got, err)
	}
	wrapped = WithFallback(m, "missing",
		func() (string, error) { return "primary", nil },
		func() (string, error) { return "fallback", nil },
	)
	if got, err := wrapped(); err != nil || got != "fallback" {
		t.Fatalf("fallback = %q err=%v", got, err)
	}
	sentinel := m.Sentinel("cache")
	if !sentinel.Guard() || !sentinel.Healthy() || sentinel.Mode() != ModeNormal {
		t.Fatalf("unexpected sentinel state: %v %v %s", sentinel.Guard(), sentinel.Healthy(), sentinel.Mode())
	}
	m.Unregister("cache")
	if sentinel.Guard() {
		t.Fatal("unregistered sentinel should not guard")
	}
}

func TestExecuteFallbackBehaviorsAndDependencyError(t *testing.T) {
	m := NewManager()
	defer m.Stop()
	m.Register("open", Config{
		HealthCheck:       func(context.Context) error { return nil },
		CheckInterval:     time.Hour,
		RecoveryThreshold: 1,
		FallbackBehavior:  FallbackFailOpen,
	})
	waitForMode(t, m, "open", ModeNormal)
	fallbackCalled := false
	if err := m.Execute(context.Background(), "open", func() error { return errors.New("primary down") }, func() error {
		fallbackCalled = true
		return nil
	}); err != nil || !fallbackCalled {
		t.Fatalf("expected fail-open fallback success, err=%v called=%v", err, fallbackCalled)
	}
	status := m.GetStatus("open")
	if status.ConsecutiveFails == 0 || status.LastError == "" {
		t.Fatalf("expected execute failure to be recorded: %+v", status)
	}

	m.Register("closed", Config{
		HealthCheck:       func(context.Context) error { return nil },
		CheckInterval:     time.Hour,
		RecoveryThreshold: 1,
		FallbackBehavior:  FallbackFailClosed,
	})
	waitForMode(t, m, "closed", ModeNormal)
	closedErr := errors.New("closed primary")
	if err := m.Execute(context.Background(), "closed", func() error { return closedErr }, func() error {
		t.Fatalf("fail-closed dependency should not run fallback")
		return nil
	}); !errors.Is(err, closedErr) {
		t.Fatalf("expected primary error for fail-closed dependency, got %v", err)
	}

	if got := ErrDependencyUnavailable.Error(); got != "dependency unavailable" {
		t.Fatalf("dependency error string = %q", got)
	}
	wrapped := WithFallback[string](m, "missing", func() (string, error) { return "primary", nil }, nil)
	if got, err := wrapped(); got != "" || !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("missing fallback got=%q err=%v", got, err)
	}
}
