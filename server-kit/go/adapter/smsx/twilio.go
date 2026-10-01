package smsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	defaultTwilioBaseURL = "https://api.twilio.com"
	maxTwilioResponse    = 64 * 1024 // 64 KiB
)

// TwilioConfig contains credentials for Twilio SMS.
type TwilioConfig struct {
	AccountSID string
	AuthToken  string
	FromNumber string
	BaseURL    string // Override for testing
	Timeout    time.Duration
}

func (c TwilioConfig) Valid() error {
	if c.AccountSID == "" {
		return errors.New("smsx: Twilio AccountSID is required")
	}
	if c.AuthToken == "" {
		return errors.New("smsx: Twilio AuthToken is required")
	}
	return nil
}

// TwilioSender delivers SMS messages through the Twilio REST API.
type TwilioSender struct {
	config  TwilioConfig
	client  *http.Client
	baseURL string
	cb      *circuitbreaker.CircuitBreaker
}

// NewTwilioSender creates a configured Twilio SMS sender.
func NewTwilioSender(cfg TwilioConfig) (*TwilioSender, error) {
	if err := cfg.Valid(); err != nil {
		return nil, err
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultTwilioBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &TwilioSender{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cb: circuitbreaker.New("sms-twilio", circuitbreaker.DefaultConfig()),
	}, nil
}

type twilioResponse struct {
	SID          string `json:"sid"`
	Status       string `json:"status"` // queued, sending, sent, failed, undelivered
	ErrorCode    *int   `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// Send delivers one SMS to a recipient via Twilio.
func (t *TwilioSender) Send(ctx context.Context, msg Message) (*SendResult, error) {
	to, err := NormalizeE164(msg.To)
	if err != nil {
		return nil, err
	}

	from := msg.From
	if from == "" {
		from = t.config.FromNumber
	}
	if from == "" {
		return nil, errors.New("smsx: sender 'From' number is required")
	}

	if msg.Body == "" {
		return nil, errors.New("smsx: SMS message body cannot be empty")
	}

	form := url.Values{
		"To":   {to},
		"From": {from},
		"Body": {msg.Body},
	}
	if msg.StatusCallbackURL != "" {
		form.Set("StatusCallback", msg.StatusCallbackURL)
	}

	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", t.baseURL, t.config.AccountSID)

	out, err := t.cb.Execute(ctx, func() (any, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if rerr != nil {
			return nil, rerr
		}

		req.SetBasicAuth(t.config.AccountSID, t.config.AuthToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if corr := metadata.FromContext(ctx).CorrelationID; corr != "" {
			req.Header.Set("X-Correlation-ID", corr)
		}

		resp, derr := t.client.Do(req)
		if derr != nil {
			return nil, derr
		}
		defer func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
		}()

		body, rderr := io.ReadAll(io.LimitReader(resp.Body, maxTwilioResponse))
		if rderr != nil {
			return nil, rderr
		}

		var res twilioResponse
		if uerr := json.Unmarshal(body, &res); uerr != nil {
			return nil, fmt.Errorf("smsx: decode twilio response: %w", uerr)
		}

		if resp.StatusCode >= 400 || res.ErrorCode != nil {
			msg := res.ErrorMessage
			if msg == "" {
				msg = fmt.Sprintf("status code %d", resp.StatusCode)
			}
			return nil, fmt.Errorf("smsx: twilio error: %s", msg)
		}

		return &SendResult{
			MessageID: res.SID,
			Status:    res.Status,
			Provider:  "twilio",
			SentAt:    time.Now().UTC(),
		}, nil
	})

	if err != nil {
		return nil, err
	}
	return out.(*SendResult), nil
}

// Probe checks connectivity with the Twilio API.
func (t *TwilioSender) Probe(ctx context.Context) (adapter.Health, error) {
	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s.json", t.baseURL, t.config.AccountSID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return adapter.HealthNotServing, err
	}

	req.SetBasicAuth(t.config.AccountSID, t.config.AuthToken)
	resp, err := t.client.Do(req)
	if err != nil {
		return adapter.HealthNotServing, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 500 {
		return adapter.HealthNotServing, fmt.Errorf("twilio probe status %d", resp.StatusCode)
	}
	return adapter.HealthServing, nil
}

func (t *TwilioSender) Close() error {
	return nil
}
