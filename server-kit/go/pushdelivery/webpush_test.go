package pushdelivery

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func keys(t testing.TB) WebPushConfig {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return WebPushConfig{base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(key.Bytes()), "mailto:team@example.com"}
}
func destination(t testing.TB) Destination {
	return Destination{"https://fcm.googleapis.com/fcm/send/test", keys(t).PublicKey, base64.RawURLEncoding.EncodeToString(make([]byte, 16))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type badBody struct{}

func (badBody) Read([]byte) (int, error) { return 0, errors.New("body failed") }
func (badBody) Close() error             { return errors.New("close failed") }
func TestWebPushValidation(t *testing.T) {
	c := keys(t)
	if !c.Valid() {
		t.Fatal("valid config rejected")
	}
	c.Subject = "https://example.com/contact"
	if !c.Valid() {
		t.Fatal("HTTPS subject rejected")
	}
	for _, change := range []func(*WebPushConfig){func(c *WebPushConfig) { c.PrivateKey = "?" }, func(c *WebPushConfig) { c.PrivateKey = "YWJj" }, func(c *WebPushConfig) { c.PublicKey = "other" }, func(c *WebPushConfig) { c.Subject = "file:///key" }, func(c *WebPushConfig) { c.Subject = "http://[" }} {
		v := keys(t)
		change(&v)
		if _, err := NewWebPush(v); err == nil {
			t.Fatal("bad config accepted")
		}
	}
	for _, url := range []string{"http://fcm.googleapis.com/x", "https://user@fcm.googleapis.com/x", "https://fcm.googleapis.com:443/x", "https://fcm.googleapis.com/x#secret", "https://fcm.googleapis.com.evil.test/x", "https://127.0.0.1/x", "https://[", "https://evil.test/push.apple.com", strings.Repeat("x", 2049)} {
		if ValidWebPushEndpoint(url) {
			t.Fatal(url)
		}
	}
	for _, url := range []string{"https://updates.push.services.mozilla.com/x", "https://web.push.apple.com/x", "https://p42.push.apple.com/x", "https://wns.windows.com/x", "https://s.notify.windows.com/x"} {
		if !ValidWebPushEndpoint(url) {
			t.Fatal(url)
		}
	}
	d := destination(t)
	if err := ValidateWebPushDestination(d); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Destination){func(d *Destination) { d.Endpoint = "https://evil.test" }, func(d *Destination) { d.PublicKey = "?" }, func(d *Destination) { d.PublicKey = "YWJj" }, func(d *Destination) { d.AuthSecret = "?" }, func(d *Destination) { d.AuthSecret = "YWJj" }} {
		v := d
		change(&v)
		if ValidateWebPushDestination(v) == nil {
			t.Fatal("bad destination accepted")
		}
	}
}
func TestWebPushSend(t *testing.T) {
	for _, tc := range []struct {
		status int
		out    Outcome
	}{{201, Accepted}, {404, Expired}, {410, Expired}, {400, Rejected}, {302, Rejected}, {408, Retry}, {429, Retry}, {503, Retry}} {
		t.Run(string(tc.out)+http.StatusText(tc.status), func(t *testing.T) {
			w, _ := NewWebPush(keys(t))
			w.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") == "" || r.Header.Get("Content-Encoding") != "aes128gcm" {
					t.Fatal("missing encryption or VAPID")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || strings.Contains(string(body), "secret text") {
					t.Fatal("unencrypted payload")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Retry-After": []string{"900"}}}, nil
			})
			d := item()
			d.Destination = destination(t)
			d.Payload = []byte("secret text")
			got := w.Send(context.Background(), d)
			if got.Outcome != tc.out || got.RetryAfter != 5*time.Minute {
				t.Fatal(got)
			}
			if w.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("redirect allowed")
			}
		})
	}
	w, _ := NewWebPush(keys(t))
	d := item()
	d.Destination = destination(t)
	w.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network") })
	if w.Send(context.Background(), d).Outcome != Retry {
		t.Fatal("network failure not retried")
	}
	for _, status := range []int{201, 503} {
		w.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: badBody{}, Header: make(http.Header)}, nil
		})
		got := w.Send(context.Background(), d)
		if status == 201 && got.Outcome != Accepted {
			t.Fatal(got)
		}
		if status == 503 && got.RetryAfter != 30*time.Second {
			t.Fatal(got)
		}
	}
	d.Payload = nil
	if w.Send(context.Background(), d).Outcome != Rejected {
		t.Fatal("empty payload")
	}
}

func TestRejectOversizedKeys(t *testing.T) {
	c := keys(t)
	c.PrivateKey = strings.Repeat("A", 129)
	if c.Valid() {
		t.Fatal("oversized private key accepted")
	}
	d := destination(t)
	d.PublicKey = strings.Repeat("A", 129)
	if ValidateWebPushDestination(d) == nil {
		t.Fatal("oversized public key accepted")
	}
}
