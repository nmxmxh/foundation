package pushx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testAPNsKey(t testing.TB) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}))
}

func TestAPNsConfigValidation(t *testing.T) {
	c := APNsConfig{}
	if c.Valid() {
		t.Fatal("empty config should be invalid")
	}

	c = APNsConfig{
		KeyID:      "KEY123",
		TeamID:     "TEAM123",
		BundleID:   "com.example.app",
		PrivateKey: testAPNsKey(t),
	}
	if !c.Valid() {
		t.Fatal("valid config should be valid")
	}

	sender, err := NewAPNsSender(c)
	if err != nil || sender == nil {
		t.Fatalf("failed to create APNs sender: %v", err)
	}

	// Invalid PEM key.
	badCfg := c
	badCfg.PrivateKey = "invalid-pem"
	if _, err := NewAPNsSender(badCfg); err == nil {
		t.Fatal("expected error with bad PEM key")
	}
}

func TestAPNsSend(t *testing.T) {
	keyPEM := testAPNsKey(t)
	for _, tc := range []struct {
		status  int
		outcome Outcome
	}{
		{http.StatusOK, Accepted},
		{http.StatusGone, Expired},
		{http.StatusTooManyRequests, Retry},
		{http.StatusInternalServerError, Retry},
		{http.StatusBadRequest, Rejected},
		{http.StatusForbidden, Rejected},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			cfg := APNsConfig{
				KeyID:      "KEY123",
				TeamID:     "TEAM123",
				BundleID:   "com.example.app",
				PrivateKey: keyPEM,
			}
			sender, err := NewAPNsSender(cfg)
			if err != nil {
				t.Fatalf("failed to create sender: %v", err)
			}

			sender.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if !strings.HasPrefix(r.Header.Get("authorization"), "bearer ") {
					t.Fatalf("missing or invalid authorization: %s", r.Header.Get("authorization"))
				}
				if r.Header.Get("apns-topic") != "com.example.app" {
					t.Fatalf("missing or invalid apns-topic: %s", r.Header.Get("apns-topic"))
				}
				return &http.Response{
					StatusCode: tc.status,
					Body:       io.NopCloser(strings.NewReader(`{"reason":"BadDeviceToken"}`)),
					Header:     http.Header{"Retry-After": []string{"30"}},
				}, nil
			})

			d := item()
			d.Destination = Destination{Endpoint: "device-apns-token-hex"}
			d.Payload = []byte(`{"aps":{"alert":"test"}}`)

			res := sender.Send(context.Background(), d)
			if res.Outcome != tc.outcome {
				t.Fatalf("expected outcome %s, got %s", tc.outcome, res.Outcome)
			}
			if tc.status == http.StatusTooManyRequests && res.RetryAfter != 30*time.Second {
				t.Fatalf("expected 30s retry after, got %v", res.RetryAfter)
			}
		})
	}
}
