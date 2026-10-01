package whatsappx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

const (
	defaultGraphBaseURL = "https://graph.facebook.com"
	defaultAPIVersion   = "v21.0"
	maxGraphResponse    = 64 * 1024         // 64 KiB
	maxMediaUploadBytes = 100 * 1024 * 1024 // 100 MiB Meta Cloud API limit
)

// Client executes calls against the WhatsApp Cloud API.
type Client struct {
	config  Config
	baseURL string
	http    *http.Client
	cb      *circuitbreaker.CircuitBreaker
}

// NewClient initializes a WhatsApp Cloud API client.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Valid(); err != nil {
		return nil, err
	}

	version := cfg.GraphAPIVersion
	if version == "" {
		version = defaultAPIVersion
	}

	base := cfg.BaseURL
	if base == "" {
		base = defaultGraphBaseURL
	}
	base = fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), version)

	return &Client{
		config:  cfg,
		baseURL: base,
		http: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cb: circuitbreaker.New("whatsapp-api", circuitbreaker.DefaultConfig()),
	}, nil
}

type graphSendMessagePayload struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type,omitempty"`
	To               string           `json:"to,omitempty"`
	Type             MessageType      `json:"type,omitempty"`
	Status           string           `json:"status,omitempty"`
	MessageID        string           `json:"message_id,omitempty"`
	Text             *TextBody        `json:"text,omitempty"`
	Template         *TemplateBody    `json:"template,omitempty"`
	Image            *MediaBody       `json:"image,omitempty"`
	Audio            *AudioBody       `json:"audio,omitempty"`
	Document         *DocumentBody    `json:"document,omitempty"`
	Interactive      *InteractiveBody `json:"interactive,omitempty"`
	Reaction         *ReactionBody    `json:"reaction,omitempty"`
	Location         *LocationBody    `json:"location,omitempty"`
	Contacts         []ContactCard    `json:"contacts,omitempty"`
}

type graphResponse struct {
	MessagingProduct string `json:"messaging_product"`
	Contacts         []struct {
		Input string `json:"input"`
		WaID  string `json:"wa_id"`
	} `json:"contacts"`
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// Send delivers one message to a recipient through WhatsApp Cloud API.
func (c *Client) Send(ctx context.Context, msg Message) (*SendResult, error) {
	if msg.To == "" {
		return nil, errors.New("whatsappx: recipient 'To' is required")
	}

	payload := graphSendMessagePayload{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               msg.To,
		Type:             msg.Type,
		Text:             msg.Text,
		Template:         msg.Template,
		Image:            msg.Media,
		Audio:            msg.Audio,
		Document:         msg.Document,
		Interactive:      msg.Interactive,
		Reaction:         msg.Reaction,
		Location:         msg.Location,
		Contacts:         msg.Contacts,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("whatsappx: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", c.baseURL, c.config.PhoneNumberID)

	out, err := c.cb.Execute(ctx, func() (any, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if rerr != nil {
			return nil, rerr
		}

		req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		c.applyCorrelation(ctx, req)

		resp, derr := c.http.Do(req)
		if derr != nil {
			return nil, derr
		}
		defer func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
		}()

		body, rderr := io.ReadAll(io.LimitReader(resp.Body, maxGraphResponse))
		if rderr != nil {
			return nil, rderr
		}

		var gResp graphResponse
		if uerr := json.Unmarshal(body, &gResp); uerr != nil {
			return nil, fmt.Errorf("decode response: %w", uerr)
		}

		if resp.StatusCode >= 400 || gResp.Error != nil {
			msg := "unknown error"
			if gResp.Error != nil {
				msg = gResp.Error.Message
			}
			return nil, fmt.Errorf("whatsappx api error (status %d): %s", resp.StatusCode, msg)
		}

		if len(gResp.Messages) == 0 {
			return nil, errors.New("whatsappx: empty message list in response")
		}

		waID := ""
		if len(gResp.Contacts) > 0 {
			waID = gResp.Contacts[0].WaID
		}

		return &SendResult{
			MessageID: gResp.Messages[0].ID,
			Contact:   waID,
		}, nil
	})

	if err != nil {
		return nil, err
	}
	return out.(*SendResult), nil
}

// MarkRead sends a read receipt to Meta, updating message status with blue checkmarks.
func (c *Client) MarkRead(ctx context.Context, messageID string) error {
	if messageID == "" {
		return errors.New("whatsappx: message ID cannot be empty")
	}

	payload := graphSendMessagePayload{
		MessagingProduct: "whatsapp",
		Status:           "read",
		MessageID:        messageID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/%s/messages", c.baseURL, c.config.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	c.applyCorrelation(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("whatsappx: mark read returned status %d", resp.StatusCode)
	}
	return nil
}

// UploadMedia uploads binary media and returns the Meta media ID.
// It verifies content magic bytes via objectstore.SniffUpload, sanitizes the filename,
// and enforces bounded streaming upload to protect against memory exhaustion.
func (c *Client) UploadMedia(ctx context.Context, mediaType string, data io.Reader, filename string) (string, error) {
	if data == nil {
		return "", errors.New("whatsappx: media data cannot be nil")
	}

	// 1. Sanitize filename against path traversal and control characters
	cleanFilename := filepath.Base(filepath.Clean(filename))
	cleanFilename = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, cleanFilename)
	if cleanFilename == "" || cleanFilename == "." {
		cleanFilename = "attachment.bin"
	}

	// 2. Validate content type using objectstore sniffing when supported
	if objectstore.SniffSupported(mediaType) {
		sniffed, err := objectstore.SniffUpload(data, mediaType)
		if err != nil {
			return "", fmt.Errorf("whatsappx: media content validation failed: %w", err)
		}
		data = sniffed
	}

	// 3. Bound upload size to prevent unbounded memory consumption
	boundedData := io.LimitReader(data, maxMediaUploadBytes+1)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("messaging_product", "whatsapp"); err != nil {
		return "", err
	}
	if err := writer.WriteField("type", mediaType); err != nil {
		return "", err
	}

	part, err := writer.CreateFormFile("file", cleanFilename)
	if err != nil {
		return "", err
	}
	copied, err := io.Copy(part, boundedData)
	if err != nil {
		return "", err
	}
	if copied > maxMediaUploadBytes {
		return "", fmt.Errorf("whatsappx: media size exceeds maximum allowed (%d bytes)", maxMediaUploadBytes)
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/%s/media", c.baseURL, c.config.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyCorrelation(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxGraphResponse))
	if err != nil {
		return "", err
	}

	var res struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 || res.Error != nil {
		return "", fmt.Errorf("upload media failed: status %d", resp.StatusCode)
	}
	return res.ID, nil
}

// GetMediaURL retrieves the temporary download URL for a media ID.
func (c *Client) GetMediaURL(ctx context.Context, mediaID string) (string, error) {
	if mediaID == "" {
		return "", errors.New("whatsappx: media ID cannot be empty")
	}

	url := fmt.Sprintf("%s/%s", c.baseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	c.applyCorrelation(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxGraphResponse))
	if err != nil {
		return "", err
	}

	var res struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return "", err
	}
	return res.URL, nil
}

// DownloadMedia securely downloads media from a temporary Meta URL.
// It verifies the URL against SSRF, rejects private networks, validates Meta domains,
// and streams directly into dst up to maxBytes without unbounded buffering.
func (c *Client) DownloadMedia(ctx context.Context, mediaURL string, dst io.Writer, maxBytes int64) (int64, error) {
	if mediaURL == "" {
		return 0, errors.New("whatsappx: media URL cannot be empty")
	}
	if dst == nil {
		return 0, errors.New("whatsappx: destination writer cannot be nil")
	}
	if maxBytes <= 0 {
		maxBytes = maxMediaUploadBytes
	}

	// 1. SSRF and outbound URL safety check
	policy := c.config.OutboundPolicy
	if policy == nil {
		policy = &security.OutboundURLPolicy{
			AllowedSchemes:       []string{"https"},
			AllowPrivateNetworks: false,
		}
	}
	parsedURL, err := security.ValidateOutboundURL(ctx, mediaURL, *policy)
	if err != nil {
		return 0, fmt.Errorf("whatsappx: media download url safety violation: %w", err)
	}

	// 2. Validate that destination host is an authorized Meta / Facebook CDN host
	host := strings.ToLower(parsedURL.Hostname())
	isMetaHost := host == "lookaside.fbsbx.com" ||
		host == "graph.facebook.com" ||
		strings.HasSuffix(host, ".fbcdn.net") ||
		strings.HasSuffix(host, ".facebook.com") ||
		strings.HasSuffix(host, ".whatsapp.net")

	if !isMetaHost {
		return 0, fmt.Errorf("whatsappx: media download host %q is not an authorized Meta domain", host)
	}

	// 3. Perform authenticated download request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	c.applyCorrelation(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("whatsappx: media download failed with status %d", resp.StatusCode)
	}

	// 4. Bounded streaming directly into target writer
	written, err := io.Copy(dst, io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return written, err
	}
	return written, nil
}

func (c *Client) applyCorrelation(ctx context.Context, req *http.Request) {
	corr := metadata.FromContext(ctx).CorrelationID
	if corr != "" {
		req.Header.Set("X-Correlation-ID", corr)
	}
}
