package pushx

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNsConfig contains credentials for Apple Push Notification service.
type APNsConfig struct {
	KeyID      string
	TeamID     string
	BundleID   string
	PrivateKey string // PKCS#8 PEM encoded P-256 private key
	Sandbox    bool
	BaseURL    string // Optional override for testing
}

func (c APNsConfig) Valid() bool {
	return c.KeyID != "" && c.TeamID != "" && c.BundleID != "" && c.PrivateKey != ""
}

// APNsSender delivers push notifications via Apple APNs HTTP/2.
type APNsSender struct {
	config  APNsConfig
	key     *ecdsa.PrivateKey
	client  *http.Client
	baseURL string
	tokenMu sync.Mutex
	token   string
	issued  time.Time
}

// NewAPNsSender creates a configured APNs sender.
func NewAPNsSender(config APNsConfig) (*APNsSender, error) {
	if !config.Valid() {
		return nil, errors.New("pushx: invalid APNs configuration")
	}

	block, _ := pem.Decode([]byte(config.PrivateKey))
	if block == nil {
		return nil, errors.New("pushx: failed to decode APNs private key PEM")
	}

	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pushx: parse APNs private key: %w", err)
	}

	ecKey, ok := parsedKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("pushx: APNs key must be an ECDSA P-256 private key")
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		if config.Sandbox {
			baseURL = "https://api.sandbox.push.apple.com"
		} else {
			baseURL = "https://api.push.apple.com"
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &APNsSender{
		config:  config,
		key:     ecKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (a *APNsSender) bearerToken(now time.Time) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// Apple tokens are valid for up to 60 minutes; refresh every 50 minutes.
	if a.token != "" && now.Sub(a.issued) < 50*time.Minute {
		return a.token, nil
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": a.config.TeamID,
		"iat": now.Unix(),
	})
	token.Header["kid"] = a.config.KeyID

	signed, err := token.SignedString(a.key)
	if err != nil {
		return "", fmt.Errorf("sign APNs JWT: %w", err)
	}

	a.token = signed
	a.issued = now
	return signed, nil
}

// Send delivers one notification to APNs.
func (a *APNsSender) Send(ctx context.Context, d Delivery) Result {
	if d.Destination.Endpoint == "" || len(d.Payload) == 0 || len(d.Payload) > 3000 {
		return Result{Outcome: Rejected}
	}

	token, err := a.bearerToken(time.Now())
	if err != nil {
		return Result{Outcome: Retry}
	}

	url := fmt.Sprintf("%s/3/device/%s", a.baseURL, d.Destination.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(d.Payload))
	if err != nil {
		return Result{Outcome: Rejected}
	}

	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", a.config.BundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("Content-Type", "application/json")
	if d.CorrelationID != "" {
		req.Header.Set("X-Correlation-ID", d.CorrelationID)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return Result{Outcome: Retry}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	res := classifyAPNs(resp.StatusCode)
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		res.RetryAfter = time.Duration(min(seconds, 300)) * time.Second
	}
	return res
}

func classifyAPNs(status int) Result {
	outcome := Rejected
	switch {
	case status >= 200 && status < 300:
		outcome = Accepted
	case status == 410:
		outcome = Expired
	case status == 408 || status == 429 || status >= 500:
		outcome = Retry
	}
	return Result{Outcome: outcome, StatusCode: status}
}
