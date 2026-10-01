package emailx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

func TestAPISender(t *testing.T) {
	_, err := NewAPISender(APIConfig{})
	if err == nil {
		t.Fatal("expected error with empty config")
	}

	cfg := APIConfig{
		Endpoint: "https://api.resend.com/emails",
		APIKey:   "re_123456",
		Provider: "resend",
	}

	sender, err := NewAPISender(cfg)
	if err != nil {
		t.Fatalf("failed to create API sender: %v", err)
	}

	sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer re_123456" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		if r.Method == http.MethodHead {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id": "resend_msg_001"}`)),
		}, nil
	})

	msg := Message{
		From:     "onboarding@resend.dev",
		To:       []string{"delivered@resend.dev"},
		Subject:  "Hello from Resend",
		TextBody: "Welcome to our platform.",
		ListUnsubscribe: &ListUnsubscribe{
			URL:      "https://example.com/unsub",
			OneClick: true,
		},
	}

	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("api send failed: %v", err)
	}
	if receipt.MessageID != "resend_msg_001" || receipt.Provider != "resend" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	h, err := sender.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing on probe, got %v (err: %v)", h, err)
	}
}
