package whatsappx

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

func TestWhatsAppAdapter(t *testing.T) {
	c, _ := NewClient(Config{
		PhoneNumberID: "123",
		AccessToken:   "tok",
		AppSecret:     "sec",
	})

	adp := NewAdapter("custom-wa", c)
	if adp.Name() != "custom-wa" {
		t.Fatalf("expected custom-wa, got %s", adp.Name())
	}
	if adp.Kind() != adapter.KindMessaging {
		t.Fatalf("expected messaging, got %s", adp.Kind())
	}

	h, err := adp.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing, got %v", h)
	}

	st := adp.Status()
	if st.Health != adapter.HealthServing {
		t.Fatalf("expected HealthServing, got %v", st.Health)
	}

	if err := adp.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	if adp.Client() != c {
		t.Fatal("unexpected client pointer")
	}

	nilAdp := NewAdapter("", nil)
	if nilAdp.Name() != "whatsapp" {
		t.Fatalf("expected default name whatsapp, got %s", nilAdp.Name())
	}
	nh, _ := nilAdp.Probe(context.Background())
	if nh != adapter.HealthNotServing {
		t.Fatalf("expected HealthNotServing for nil client, got %v", nh)
	}
}
