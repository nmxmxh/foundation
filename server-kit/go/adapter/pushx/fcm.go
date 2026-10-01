package pushx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FCMConfig contains configuration for Firebase Cloud Messaging v1.
type FCMConfig struct {
	ProjectID string
	// TokenProvider returns an active Google OAuth2 bearer token.
	TokenProvider func(context.Context) (string, error)
	// BaseURL can override the FCM endpoint for testing.
	BaseURL string
}

func (c FCMConfig) Valid() bool {
	return c.ProjectID != "" && c.TokenProvider != nil
}

// FCMSender sends notifications using the FCM v1 REST API.
type FCMSender struct {
	config FCMConfig
	client *http.Client
}

// NewFCMSender creates a configured FCM v1 sender.
func NewFCMSender(config FCMConfig) (*FCMSender, error) {
	if !config.Valid() {
		return nil, errors.New("pushx: invalid FCM configuration")
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://fcm.googleapis.com"
	}
	config.BaseURL = strings.TrimRight(baseURL, "/")

	return &FCMSender{
		config: config,
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type fcmPayload struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token string          `json:"token"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Send delivers one notification to FCM v1.
func (f *FCMSender) Send(ctx context.Context, d Delivery) Result {
	if d.Destination.Endpoint == "" || len(d.Payload) == 0 || len(d.Payload) > 3000 {
		return Result{Outcome: Rejected}
	}

	token, err := f.config.TokenProvider(ctx)
	if err != nil {
		return Result{Outcome: Retry}
	}

	var raw json.RawMessage
	if json.Unmarshal(d.Payload, &raw) == nil {
		// Valid JSON payload
	} else {
		// Wrap raw string into json string
		wrapped, _ := json.Marshal(map[string]string{"body": string(d.Payload)})
		raw = wrapped
	}

	bodyData, err := json.Marshal(fcmPayload{
		Message: fcmMessage{
			Token: d.Destination.Endpoint,
			Data:  raw,
		},
	})
	if err != nil {
		return Result{Outcome: Rejected}
	}

	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", f.config.BaseURL, f.config.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyData))
	if err != nil {
		return Result{Outcome: Rejected}
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if d.CorrelationID != "" {
		req.Header.Set("X-Correlation-ID", d.CorrelationID)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return Result{Outcome: Retry}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	res := classifyFCM(resp.StatusCode)
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		res.RetryAfter = time.Duration(min(seconds, 300)) * time.Second
	}
	return res
}

func classifyFCM(status int) Result {
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
