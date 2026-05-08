package startup

import (
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func TestNewRuntimeValidationAndDefaults(t *testing.T) {
	if _, err := NewRuntime(Options{}); err == nil {
		t.Fatalf("expected missing service error")
	}
	if _, err := NewRuntime(Options{Service: "api"}); err == nil {
		t.Fatalf("expected missing jwt secret error")
	}

	runtime, err := NewRuntime(Options{Service: "api", JWTSecret: "secret-at-least-32-bytes-long-for-tests"})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if runtime.Registry == nil || runtime.Handler == nil || runtime.JWT == nil || runtime.RBAC == nil || runtime.Handler.Version != "v1" {
		t.Fatalf("runtime defaults not populated: %+v", runtime)
	}
}

func TestNewRuntimeUsesProvidedManagersAndCloseOrder(t *testing.T) {
	jwtManager, err := auth.NewJWTManager("secret-at-least-32-bytes-long-for-tests")
	if err != nil {
		t.Fatalf("NewJWTManager() error = %v", err)
	}
	authorizer := security.NewAuthorizer([]security.RoleTemplate{{Role: "viewer", Capabilities: []string{"orders.view"}}})
	runtime, err := NewRuntime(Options{
		Service:      "api",
		Version:      " v2 ",
		JWTManager:   jwtManager,
		Authorizer:   authorizer,
		EventEnabled: true,
		BusCloser: func() error {
			return errors.New("first")
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if runtime.JWT != jwtManager || runtime.RBAC != authorizer || runtime.Handler.Version != "v2" {
		t.Fatalf("provided managers/options not used")
	}
	calls := []string{}
	runtime.AddCloser(func() error {
		calls = append(calls, "second")
		return nil
	})
	runtime.AddCloser(func() error {
		calls = append(calls, "third")
		return errors.New("third-error")
	})
	runtime.AddCloser(nil)
	if err := runtime.Close(); err == nil || err.Error() != "third-error" {
		t.Fatalf("Close() error = %v", err)
	}
	if len(calls) != 2 || calls[0] != "third" || calls[1] != "second" {
		t.Fatalf("close order = %+v", calls)
	}
	if err := (*Runtime)(nil).Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	(*Runtime)(nil).AddCloser(func() error { return nil })
}

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion(""); got != "v1" {
		t.Fatalf("blank version = %q", got)
	}
	if got := normalizeVersion(" v3 "); got != "v3" {
		t.Fatalf("trimmed version = %q", got)
	}
}
