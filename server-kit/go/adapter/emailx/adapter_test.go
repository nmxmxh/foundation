package emailx

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

type mockEmailSender struct {
	name string
}

func (m *mockEmailSender) Send(_ context.Context, _ Message) (*DeliveryReceipt, error) {
	return &DeliveryReceipt{MessageID: "msg-123", StatusCode: 250}, nil
}

func (m *mockEmailSender) Probe(_ context.Context) (adapter.Health, error) {
	return adapter.HealthServing, nil
}

func (m *mockEmailSender) Close() error {
	return nil
}

func TestEmailAdapter(t *testing.T) {
	sender := &mockEmailSender{name: "test"}
	adp := NewAdapter("custom-email", sender)

	if adp.Name() != "custom-email" {
		t.Fatalf("expected name custom-email, got %s", adp.Name())
	}
	if adp.Kind() != adapter.KindEmail {
		t.Fatalf("expected kind email, got %s", adp.Kind())
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
	if nilAdp.Name() != "email" {
		t.Fatalf("expected default name email, got %s", nilAdp.Name())
	}
	nh, _ := nilAdp.Probe(context.Background())
	if nh != adapter.HealthNotServing {
		t.Fatalf("expected HealthNotServing for nil sender, got %v", nh)
	}
}
