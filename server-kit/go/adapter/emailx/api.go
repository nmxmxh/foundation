package emailx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

// APIConfig contains credentials for HTTP transactional email providers.
type APIConfig struct {
	Endpoint string
	APIKey   string
	Provider string // e.g. "resend", "sendgrid", "postmark"
	Timeout  time.Duration
}

// APISender delivers email via HTTP REST APIs.
type APISender struct {
	config APIConfig
	client *http.Client
	cb     *circuitbreaker.CircuitBreaker
}

// NewAPISender creates a configured HTTP API email sender.
func NewAPISender(cfg APIConfig) (*APISender, error) {
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		return nil, errors.New("emailx: HTTP API Endpoint and APIKey are required")
	}
	if cfg.Provider == "" {
		cfg.Provider = "http-api"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	return &APISender{
		config: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cb: circuitbreaker.New("email-"+cfg.Provider, circuitbreaker.DefaultConfig()),
	}, nil
}

type apiPayload struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Cc      []string          `json:"cc,omitempty"`
	Bcc     []string          `json:"bcc,omitempty"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Subject string            `json:"subject"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Send delivers one email message over the configured HTTP API.
func (a *APISender) Send(ctx context.Context, msg Message) (*DeliveryReceipt, error) {
	headers := make(map[string]string)
	for k, v := range msg.Headers {
		headers[k] = v
	}

	if msg.ListUnsubscribe != nil {
		if msg.ListUnsubscribe.URL != "" {
			headers["List-Unsubscribe"] = fmt.Sprintf("<%s>", msg.ListUnsubscribe.URL)
		}
		if msg.ListUnsubscribe.OneClick {
			headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
		}
	}

	payload := apiPayload{
		From:    msg.From,
		To:      msg.To,
		Cc:      msg.Cc,
		Bcc:     msg.Bcc,
		ReplyTo: msg.ReplyTo,
		Subject: msg.Subject,
		Text:    msg.TextBody,
		HTML:    msg.HTMLBody,
		Headers: headers,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	out, err := a.cb.Execute(ctx, func() (any, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, a.config.Endpoint, bytes.NewReader(data))
		if rerr != nil {
			return nil, rerr
		}

		req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		if corr := metadata.FromContext(ctx).CorrelationID; corr != "" {
			req.Header.Set("X-Correlation-ID", corr)
		}

		resp, derr := a.client.Do(req)
		if derr != nil {
			return nil, derr
		}
		defer func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
		}()

		body, rderr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if rderr != nil {
			return nil, rderr
		}

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("emailx api error (status %d): %s", resp.StatusCode, string(body))
		}

		var res struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &res)

		msgID := res.ID
		if msgID == "" {
			msgID = msg.MessageID
		}

		return &DeliveryReceipt{
			MessageID:  msgID,
			Provider:   a.config.Provider,
			StatusCode: resp.StatusCode,
			SentAt:     time.Now().UTC(),
		}, nil
	})

	if err != nil {
		return nil, err
	}
	return out.(*DeliveryReceipt), nil
}

// Probe checks API health status.
func (a *APISender) Probe(ctx context.Context) (adapter.Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, a.config.Endpoint, nil)
	if err != nil {
		return adapter.HealthNotServing, err
	}

	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	resp, err := a.client.Do(req)
	if err != nil {
		return adapter.HealthNotServing, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 500 {
		return adapter.HealthNotServing, fmt.Errorf("api probe status %d", resp.StatusCode)
	}
	return adapter.HealthServing, nil
}

func (a *APISender) Close() error {
	return nil
}
