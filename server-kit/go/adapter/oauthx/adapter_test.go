package oauthx

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

type mockIDP struct {
	name string
}

func (m *mockIDP) Name() string { return m.name }
func (m *mockIDP) AuthURL(_ string, _ ...AuthOption) (string, string, error) {
	return "https://auth.example.com", "verifier", nil
}
func (m *mockIDP) Exchange(_ context.Context, _, _ string) (*TokenSet, error) {
	return &TokenSet{AccessToken: "token"}, nil
}
func (m *mockIDP) Refresh(_ context.Context, _ string) (*TokenSet, error) {
	return &TokenSet{AccessToken: "refreshed"}, nil
}
func (m *mockIDP) Revoke(_ context.Context, _ string) error { return nil }
func (m *mockIDP) UserInfo(_ context.Context, _ string) (*UserProfile, error) {
	return &UserProfile{Email: "test@example.com"}, nil
}
func (m *mockIDP) ValidateIDToken(_ context.Context, _ string) (*Claims, error) {
	return &Claims{Subject: "sub-1"}, nil
}

func TestOAuthAdapter(t *testing.T) {
	idp := &mockIDP{name: "custom-idp"}
	adp := NewAdapter("custom-oauth", idp)

	if adp.Name() != "custom-oauth" {
		t.Fatalf("expected name custom-oauth, got %s", adp.Name())
	}
	if adp.Kind() != adapter.KindOAuth {
		t.Fatalf("expected kind oauth, got %s", adp.Kind())
	}

	h, err := adp.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing, got %v (err: %v)", h, err)
	}

	st := adp.Status()
	if st.Health != adapter.HealthServing {
		t.Fatalf("expected status health serving, got %v", st.Health)
	}

	if err := adp.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	if adp.Provider() != idp {
		t.Fatal("unexpected provider pointer")
	}

	nilAdp := NewAdapter("", nil)
	if nilAdp.Name() != "google-oauth" {
		t.Fatalf("expected default name google-oauth, got %s", nilAdp.Name())
	}
	nh, _ := nilAdp.Probe(context.Background())
	if nh != adapter.HealthNotServing {
		t.Fatalf("expected HealthNotServing for nil provider, got %v", nh)
	}
}
