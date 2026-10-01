package emailx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	jmapCoreURN       = "urn:ietf:params:jmap:core"
	jmapSubmissionURN = "urn:ietf:params:jmap:submission"
	defaultJMAPPath   = "/jmap"
)

// JMAPConfig contains connection settings for Stalwart JMAP.
type JMAPConfig struct {
	BaseURL   string
	Username  string
	Password  string
	AccountID string
	Timeout   time.Duration
}

// JMAPSender submits email using the JMAP protocol (RFC 8621).
type JMAPSender struct {
	config JMAPConfig
	client *http.Client
}

// NewJMAPSender creates an initialized JMAP email sender.
func NewJMAPSender(cfg JMAPConfig) (*JMAPSender, error) {
	if cfg.BaseURL == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("emailx: JMAP BaseURL, Username, and Password are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	return &JMAPSender{
		config: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type jmapRequestPayload struct {
	Using       []string `json:"using"`
	MethodCalls [][]any  `json:"methodCalls"`
}

type jmapResponsePayload struct {
	MethodResponses [][]any `json:"methodResponses"`
}

// Send delivers one email message through JMAP EmailSubmission.
func (j *JMAPSender) Send(ctx context.Context, msg Message) (*DeliveryReceipt, error) {
	rawBytes, msgID, err := BuildRFC5322(msg)
	if err != nil {
		return nil, fmt.Errorf("emailx: build rfc5322: %w", err)
	}

	if msg.DKIM != nil {
		signed, dkimErr := SignDKIM(rawBytes, *msg.DKIM)
		if dkimErr != nil {
			return nil, fmt.Errorf("emailx: dkim sign: %w", dkimErr)
		}
		rawBytes = signed
	}

	allRecipients := append([]string{}, msg.To...)
	allRecipients = append(allRecipients, msg.Cc...)
	allRecipients = append(allRecipients, msg.Bcc...)

	submissionArgs := map[string]any{
		"accountId": j.config.AccountID,
		"create": map[string]any{
			"sub-1": map[string]any{
				"envelope": map[string]any{
					"mailFrom": map[string]string{"email": msg.From},
					"rcptTo":   formatRcptList(allRecipients),
				},
				"blob": base64.StdEncoding.EncodeToString(rawBytes),
			},
		},
	}

	payload := jmapRequestPayload{
		Using: []string{jmapCoreURN, jmapSubmissionURN},
		MethodCalls: [][]any{
			{"EmailSubmission/set", submissionArgs, "c1"},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(j.config.BaseURL, "/") + defaultJMAPPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(j.config.Username + ":" + j.config.Password))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")
	if corr := metadata.FromContext(ctx).CorrelationID; corr != "" {
		req.Header.Set("X-Correlation-ID", corr)
	}

	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("emailx: jmap request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("emailx: jmap error status %d", resp.StatusCode)
	}

	var res jmapResponsePayload
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("emailx: decode jmap response: %w", err)
	}

	return &DeliveryReceipt{
		MessageID:  msgID,
		Provider:   "stalwart-jmap",
		StatusCode: resp.StatusCode,
		SentAt:     time.Now().UTC(),
	}, nil
}

func formatRcptList(rcpts []string) []map[string]string {
	out := make([]map[string]string, 0, len(rcpts))
	for _, r := range rcpts {
		out = append(out, map[string]string{"email": r})
	}
	return out
}

// Probe checks connectivity with the JMAP server.
func (j *JMAPSender) Probe(ctx context.Context) (adapter.Health, error) {
	endpoint := strings.TrimRight(j.config.BaseURL, "/") + defaultJMAPPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[]}`)))
	if err != nil {
		return adapter.HealthNotServing, err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(j.config.Username + ":" + j.config.Password))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return adapter.HealthNotServing, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 500 {
		return adapter.HealthNotServing, fmt.Errorf("jmap probe status %d", resp.StatusCode)
	}
	return adapter.HealthServing, nil
}

func (j *JMAPSender) Close() error {
	return nil
}
