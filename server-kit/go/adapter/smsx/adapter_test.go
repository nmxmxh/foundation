package smsx

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

type mockSMSSender struct {
	name string
}

func (m *mockSMSSender) Send(_ context.Context, _ Message) (*SendResult, error) {
	return &SendResult{MessageID: "sms-123", Status: "sent"}, nil
}

func (m *mockSMSSender) Probe(_ context.Context) (adapter.Health, error) {
	return adapter.HealthServing, nil
}

func (m *mockSMSSender) Close() error {
	return nil
}

func TestSMSAdapter(t *testing.T) {
	sender := &mockSMSSender{name: "test"}
	adp := NewAdapter("custom-sms", sender)

	if adp.Name() != "custom-sms" {
		t.Fatalf("expected custom-sms, got %s", adp.Name())
	}
	if adp.Kind() != adapter.KindSMS {
		t.Fatalf("expected sms, got %s", adp.Kind())
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

	if adp.Sender() != sender {
		t.Fatal("unexpected sender pointer")
	}

	nilAdp := NewAdapter("", nil)
	if nilAdp.Name() != "sms" {
		t.Fatalf("expected default name sms, got %s", nilAdp.Name())
	}
	nh, _ := nilAdp.Probe(context.Background())
	if nh != adapter.HealthNotServing {
		t.Fatalf("expected HealthNotServing for nil sender, got %v", nh)
	}
}
