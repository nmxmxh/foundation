package emailx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJMAPSender(t *testing.T) {
	_, err := NewJMAPSender(JMAPConfig{})
	if err == nil {
		t.Fatal("expected error with empty config")
	}

	cfg := JMAPConfig{
		BaseURL:   "https://mail.example.com",
		Username:  "admin",
		Password:  "secret",
		AccountID: "acc-1",
	}

	sender, err := NewJMAPSender(cfg)
	if err != nil {
		t.Fatalf("failed to create JMAP sender: %v", err)
	}

	sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"methodResponses": [["EmailSubmission/set", {"created":{"sub-1":{"id":"msg-999"}}}, "c1"]]
			}`)),
		}, nil
	})

	msg := Message{
		From:     "sender@example.com",
		To:       []string{"rcpt@example.com"},
		Subject:  "JMAP Test",
		TextBody: "Hello JMAP",
	}

	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("jmap send failed: %v", err)
	}
	if receipt.Provider != "stalwart-jmap" || receipt.StatusCode != 200 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	h, err := sender.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing on probe, got %v (err: %v)", h, err)
	}
}
