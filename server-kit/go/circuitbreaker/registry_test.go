package circuitbreaker

import (
	"testing"
	"time"
)

func TestRegistryLifecycle(t *testing.T) {
	registry := NewRegistry(Config{FailureThreshold: 2, Timeout: time.Second})
	first := registry.Get("payments")
	second := registry.Get("payments")
	if first != second {
		t.Fatalf("Get should return cached breaker")
	}
	if first.Stats().Config.FailureThreshold != 2 {
		t.Fatalf("default config was not applied")
	}

	custom := registry.GetWithConfig("search", Config{FailureThreshold: 7})
	if custom.Stats().Config.FailureThreshold != 7 {
		t.Fatalf("custom config was not applied")
	}
	if again := registry.GetWithConfig("search", Config{FailureThreshold: 9}); again != custom {
		t.Fatalf("existing custom breaker should be reused")
	}
	if len(registry.All()) != 2 {
		t.Fatalf("expected two breakers")
	}
	if len(registry.AllStats()) != 2 {
		t.Fatalf("expected two stats entries")
	}

	first.failures = 1
	registry.Reset()
	failures, _ := first.Counts()
	if failures != 0 {
		t.Fatalf("Reset should clear failures")
	}
	registry.Remove("payments")
	if len(registry.All()) != 1 {
		t.Fatalf("Remove should delete breaker")
	}
}

func TestGlobalRegistryExists(t *testing.T) {
	if Global() == nil {
		t.Fatalf("Global() returned nil")
	}
	SetGlobalConfig(Config{FailureThreshold: 9})
	if Global() == nil {
		t.Fatalf("Global() returned nil after SetGlobalConfig")
	}
}

func TestRegistryNormalizesEmptyNameAndZeroGlobalConfig(t *testing.T) {
	registry := NewRegistry(Config{})
	first := registry.Get("")
	if first.Name() != "" {
		t.Fatalf("empty breaker name changed to %q", first.Name())
	}
	if again := registry.Get(""); again != first {
		t.Fatal("normalized empty name did not reuse breaker")
	}
	SetGlobalConfig(Config{})
	if Global() == nil {
		t.Fatal("global registry missing after zero config")
	}
}
