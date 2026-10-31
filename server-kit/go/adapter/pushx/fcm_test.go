package pushx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFCMConfigValidation(t *testing.T) {
	c := FCMConfig{}
	if c.Valid() {
		t.Fatal("empty config should be invalid")
	}

	c.ProjectID = "proj-123"
	if c.Valid() {
		t.Fatal("config without token provider should be invalid")
	}

	c.TokenProvider = func(context.Context) (string, error) { return "token", nil }
	if !c.Valid() {
		t.Fatal("valid config rejected")
	}

	sender, err := NewFCMSender(c)
	if err != nil || sender == nil {
		t.Fatalf("failed to create FCM sender: %v", err)
	}

	_, err = NewFCMSender(FCMConfig{})
	if err == nil {
		t.Fatal("expected error with invalid config")
	}
}

func TestFCMSend(t *testing.T) {
	for _, tc := range []struct {
		status  int
		outcome Outcome
	}{
		{http.StatusOK, Accepted},
		{http.StatusNotFound, Expired},
		{http.StatusGone, Expired},
		{http.StatusTooManyRequests, Retry},
		{http.StatusInternalServerError, Retry},
		{http.StatusBadRequest, Rejected},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			cfg := FCMConfig{
				ProjectID: "test-proj",
				TokenProvider: func(context.Context) (string, error) {
					return "mock-bearer-token", nil
				},
			}
			sender, err := NewFCMSender(cfg)
			if err != nil {
				t.Fatalf("failed to create sender: %v", err)
			}

			sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer mock-bearer-token" {
					t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
				}
				return &http.Response{
					StatusCode: tc.status,
					Body:       io.NopCloser(strings.NewReader(`{"name":"projects/test-proj/messages/123"}`)),
					Header:     http.Header{"Retry-After": []string{"60"}},
				}, nil
			})

			d := item()
			d.Destination = Destination{Endpoint: "device-fcm-token"}
			d.Payload = []byte(`{"title":"hello"}`)

			res := sender.Send(context.Background(), d)
			if res.Outcome != tc.outcome {
				t.Fatalf("expected outcome %s, got %s", tc.outcome, res.Outcome)
			}
			if tc.status == http.StatusTooManyRequests && res.RetryAfter != 60*time.Second {
				t.Fatalf("expected 60s retry after, got %v", res.RetryAfter)
			}
		})
	}

	// Test token provider failure.
	cfg := FCMConfig{
		ProjectID: "test-proj",
		TokenProvider: func(context.Context) (string, error) {
			return "", errors.New("token failed")
		},
	}
	sender, _ := NewFCMSender(cfg)
	d := item()
	d.Destination = Destination{Endpoint: "device-fcm-token"}
	res := sender.Send(context.Background(), d)
	if res.Outcome != Retry {
		t.Fatalf("expected Retry on token provider failure, got %s", res.Outcome)
	}

	// Test invalid payload.
	d.Payload = nil
	if sender.Send(context.Background(), d).Outcome != Rejected {
		t.Fatal("expected Rejected for empty payload")
	}
}

// TestFCMSendWrapsPlainTextAndRetriesTransportErrors covers the non-JSON wrap
// path, the correlation header, and transport failure classification.
func TestFCMSendWrapsPlainTextAndRetriesTransportErrors(t *testing.T) {
	sender, err := NewFCMSender(FCMConfig{
		ProjectID:     "test-proj",
		TokenProvider: func(context.Context) (string, error) { return "tok", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var body string
	sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		if r.Header.Get("X-Correlation-ID") != "corr-1" {
			t.Fatalf("correlation header = %q", r.Header.Get("X-Correlation-ID"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})
	d := item()
	d.Destination = Destination{Endpoint: "device"}
	d.CorrelationID = "corr-1"
	d.Payload = []byte(`plain "text" message`)
	if res := sender.Send(context.Background(), d); res.Outcome != Accepted {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !strings.Contains(body, `"data":{"body":"plain \"text\" message"}`) {
		t.Fatalf("plain text not wrapped as a JSON body field: %s", body)
	}

	sender.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset")
	})
	if res := sender.Send(context.Background(), d); res.Outcome != Retry {
		t.Fatalf("transport error outcome = %s, want Retry", res.Outcome)
	}

	d.Payload = make([]byte, 3001)
	if res := sender.Send(context.Background(), d); res.Outcome != Rejected {
		t.Fatalf("oversize payload outcome = %s, want Rejected", res.Outcome)
	}
}
