package pushdelivery

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type WebPushConfig struct{ PublicKey, PrivateKey, Subject string }

func (c WebPushConfig) Valid() bool {
	if len(c.PrivateKey) > 128 || len(c.PublicKey) > 128 || len(c.Subject) > 2048 {
		return false
	}
	private, err := base64.RawURLEncoding.DecodeString(c.PrivateKey)
	if err != nil {
		return false
	}
	key, err := ecdh.P256().NewPrivateKey(private)
	if err != nil {
		return false
	}
	subject, err := url.Parse(c.Subject)
	return err == nil && ((subject.Scheme == "mailto" && subject.Opaque != "") ||
		(subject.Scheme == "https" && subject.Host != "" && subject.User == nil)) &&
		base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) == c.PublicKey
}

// ValidWebPushEndpoint restricts egress to browser push services. Redirects must remain disabled.
func ValidWebPushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" ||
		host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") ||
		host == "wns.windows.com" || strings.HasSuffix(host, ".notify.windows.com")
}

func ValidateWebPushDestination(d Destination) error {
	if len(d.PublicKey) > 128 || len(d.AuthSecret) > 64 {
		return errors.New("subscription keys exceed limits")
	}
	if !ValidWebPushEndpoint(d.Endpoint) {
		return errors.New("unsupported push endpoint")
	}
	public, err := base64.RawURLEncoding.DecodeString(d.PublicKey)
	if err != nil {
		return errors.New("invalid subscription key")
	}
	if _, err = ecdh.P256().NewPublicKey(public); err != nil {
		return errors.New("invalid subscription key")
	}
	secret, err := base64.RawURLEncoding.DecodeString(d.AuthSecret)
	if err != nil || len(secret) != 16 {
		return errors.New("invalid subscription secret")
	}
	return nil
}

type WebPush struct {
	config WebPushConfig
	client *http.Client
}

func NewWebPush(config WebPushConfig) (*WebPush, error) {
	if !config.Valid() {
		return nil, errors.New("invalid Web Push configuration")
	}
	return &WebPush{config, &http.Client{Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Send encrypts one visible notification. Provider acceptance does not prove device receipt.
func (w *WebPush) Send(ctx context.Context, d Delivery) Result {
	if ValidateWebPushDestination(d.Destination) != nil || len(d.Payload) == 0 || len(d.Payload) > 3000 {
		return Result{Outcome: Rejected}
	}
	response, err := webpush.SendNotificationWithContext(ctx, d.Payload, &webpush.Subscription{
		Endpoint: d.Destination.Endpoint, Keys: webpush.Keys{P256dh: d.Destination.PublicKey, Auth: d.Destination.AuthSecret},
	}, &webpush.Options{Subscriber: w.config.Subject, VAPIDPublicKey: w.config.PublicKey,
		VAPIDPrivateKey: w.config.PrivateKey, TTL: 3600, Urgency: webpush.UrgencyNormal, HTTPClient: w.client})
	if err != nil {
		return Result{Outcome: Retry}
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	result := classify(response.StatusCode)
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		result.RetryAfter = time.Duration(min(seconds, 300)) * time.Second
	}
	if readErr != nil || closeErr != nil {
		// The status remains authoritative after acceptance, even when body cleanup fails.
		if result.Outcome != Accepted {
			result.RetryAfter = max(result.RetryAfter, 30*time.Second)
		}
	}
	return result
}

func classify(status int) Result {
	outcome := Rejected
	switch {
	case status >= 200 && status < 300:
		outcome = Accepted
	case status == 404 || status == 410:
		outcome = Expired
	case status == 408 || status == 429 || status >= 500:
		outcome = Retry
	}
	return Result{Outcome: outcome, StatusCode: status}
}
