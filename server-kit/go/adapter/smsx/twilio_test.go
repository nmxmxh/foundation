package smsx

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

func TestNormalizeE164(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		err      bool
	}{
		{"+15551234567", "+15551234567", false},
		{"15551234567", "+15551234567", false},
		{"+44 (0) 20 7946 0991", "+4402079460991", false},
		{"", "", true},
		{"123", "", true},                   // too short
		{strings.Repeat("9", 20), "", true}, // too long
	}

	for _, tc := range tests {
		got, err := NormalizeE164(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("input %q: expected error, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("input %q: unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, got)
			}
		}
	}
}

func TestTwilioSender(t *testing.T) {
	_, err := NewTwilioSender(TwilioConfig{})
	if err == nil {
		t.Fatal("expected error with empty config")
	}

	cfg := TwilioConfig{
		AccountSID: "AC1234567890",
		AuthToken:  "auth-token-secret",
		FromNumber: "+15559990000",
	}

	sender, err := NewTwilioSender(cfg)
	if err != nil {
		t.Fatalf("failed to create Twilio sender: %v", err)
	}

	sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "AC1234567890" || p != "auth-token-secret" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		if r.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"sid": "SM123456789",
				"status": "queued"
			}`)),
		}, nil
	})

	msg := Message{
		To:   "+15551234567",
		Body: "Your verification code is 492049.",
	}

	res, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("failed to send SMS: %v", err)
	}
	if res.MessageID != "SM123456789" || res.Status != "queued" || res.Provider != "twilio" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Missing body
	if _, err := sender.Send(context.Background(), Message{To: "+15551234567"}); err == nil {
		t.Fatal("expected error for empty body")
	}

	// Bad phone number
	if _, err := sender.Send(context.Background(), Message{To: "bad", Body: "test"}); err == nil {
		t.Fatal("expected error for invalid phone number")
	}

	h, err := sender.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing on probe, got %v", h)
	}
}
