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
	"net/textproto"
	"net/url"
	"os"
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
	maxGraphResponse    = 64 << 10
	maxMediaUploadBytes = 100 << 20
)

// Client executes bounded calls; mutations are never automatically retried.
type Client struct {
	config  Config
	baseURL string
	http    *http.Client
	cb      *circuitbreaker.CircuitBreaker
	uploads chan struct{}
}

func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Valid(); err != nil {
		return nil, err
	}
	if cfg.MaxMediaBytes == 0 {
		cfg.MaxMediaBytes = 8 << 20
	}
	if cfg.MaxConcurrentUploads == 0 {
		cfg.MaxConcurrentUploads = 2
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultGraphBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("whatsappx: invalid Graph base URL")
	}
	return &Client{config: cfg, baseURL: strings.TrimRight(base, "/") + "/" + cfg.GraphAPIVersion, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cb: circuitbreaker.New("whatsapp-api", circuitbreaker.DefaultConfig()), uploads: make(chan struct{}, cfg.MaxConcurrentUploads)}, nil
}

// graphCall decodes bounded evidence, without including raw provider text in errors.
func (c *Client) graphCall(ctx context.Context, operation, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/"+path, body)
	if err != nil {
		return err
	}
	if sized, ok := body.(interface{ Size() int64 }); ok {
		req.ContentLength = sized.Size()
	}
	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	req.Header.Set("Content-Type", contentType)
	c.applyCorrelation(ctx, req)
	resp, err := c.http.Do(req)
	if err != nil {
		class := OutcomeTransient
		if method != http.MethodGet {
			class = OutcomeUnknown
		}
		return &ProviderError{Operation: operation, Class: class}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGraphResponse+1))
	if err != nil || len(raw) > maxGraphResponse {
		return providerFailure(operation, resp, nil, method != http.MethodGet)
	}
	var envelope struct {
		Error *ProviderFailure `json:"error"`
	}
	decodeErr := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Error != nil {
		return providerFailure(operation, resp, envelope.Error, false)
	}
	if decodeErr != nil || json.Unmarshal(raw, out) != nil {
		return providerFailure(operation, resp, nil, method != http.MethodGet)
	}
	return nil
}

// Send validates every modeled variant before dispatch.
func (c *Client) Send(ctx context.Context, msg Message) (*SendResult, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	payload := struct {
		MessagingProduct string `json:"messaging_product"`
		RecipientType    string `json:"recipient_type"`
		Message
	}{"whatsapp", "individual", msg}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(data) > 256<<10 {
		return nil, errInvalidMessage
	}
	out, err := c.cb.Execute(ctx, func() (any, error) {
		var response struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			Contacts []struct {
				WaID string `json:"wa_id"`
			} `json:"contacts"`
		}
		if err := c.graphCall(ctx, "send", http.MethodPost, c.config.PhoneNumberID+"/messages", "application/json", bytes.NewReader(data), &response); err != nil {
			return nil, err
		}
		if len(response.Messages) == 0 || response.Messages[0].ID == "" {
			return nil, &ProviderError{Operation: "send", Class: OutcomeUnknown}
		}
		result := &SendResult{MessageID: response.Messages[0].ID}
		if len(response.Contacts) > 0 {
			result.Contact = response.Contacts[0].WaID
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	return out.(*SendResult), nil
}
func (c *Client) MarkRead(ctx context.Context, messageID string) error {
	if !boundedText(messageID, 256, true) {
		return errInvalidMessage
	}
	data, err := json.Marshal(map[string]string{"messaging_product": "whatsapp", "status": "read", "message_id": messageID})
	if err != nil {
		return err
	}
	var response struct {
		Success bool `json:"success"`
	}
	if err := c.graphCall(ctx, "mark-read", http.MethodPost, c.config.PhoneNumberID+"/messages", "application/json", bytes.NewReader(data), &response); err != nil {
		return err
	}
	if !response.Success {
		return &ProviderError{Operation: "mark-read", Class: OutcomeUnknown}
	}
	return nil
}

// UploadMedia spools a bounded multipart object; only validated complete input is sent.
func (c *Client) UploadMedia(ctx context.Context, mediaType string, data io.Reader, filename string) (id string, err error) {
	if data == nil || !objectstore.SniffSupported(mediaType) {
		return "", errors.New("whatsappx: unsupported or missing media data")
	}
	select {
	case c.uploads <- struct{}{}:
		defer func() { <-c.uploads }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	data, err = objectstore.SniffUpload(contextReader{ctx, data}, mediaType)
	if err != nil {
		return "", err
	}
	spool, err := os.CreateTemp("", "whatsapp-upload-*")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, spool.Close(), os.Remove(spool.Name())) }()
	writer := multipart.NewWriter(spool)
	if err = writer.WriteField("messaging_product", "whatsapp"); err != nil {
		return "", err
	}
	if err = writer.WriteField("type", mediaType); err != nil {
		return "", err
	}
	filename = filepath.Base(filepath.Clean(filename))
	filename = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	if filename == "" || filename == "." {
		filename = "attachment.bin"
	}
	filename = truncateString(filename, 240)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf("form-data; name=\"file\"; filename=\"%s\"", filename))
	header.Set("Content-Type", objectstore.NormalizeContentType(mediaType))
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(part, io.LimitReader(contextReader{ctx, data}, c.config.MaxMediaBytes+1))
	if err != nil {
		return "", err
	}
	if n == 0 || n > c.config.MaxMediaBytes {
		return "", errors.New("whatsappx: media size exceeds product limit")
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	info, err := spool.Stat()
	if err != nil {
		return "", err
	}
	var response struct {
		ID string `json:"id"`
	}
	if err = c.graphCall(ctx, "upload", http.MethodPost, c.config.PhoneNumberID+"/media", writer.FormDataContentType(), io.NewSectionReader(spool, 0, info.Size()), &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", &ProviderError{Operation: "upload", Class: OutcomeUnknown}
	}
	return response.ID, nil
}
func (c *Client) GetMediaURL(ctx context.Context, mediaID string) (string, error) {
	if !validPathID(mediaID) {
		return "", errors.New("whatsappx: invalid media ID")
	}
	var response struct {
		URL string `json:"url"`
	}
	if err := c.graphCall(ctx, "media-url", http.MethodGet, mediaID, "", nil, &response); err != nil {
		return "", err
	}
	parsed, err := url.Parse(response.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", &ProviderError{Operation: "media-url", Class: OutcomePermanent}
	}
	return response.URL, nil
}

// DownloadMedia validates the complete bounded source before publishing to dst.
// A destination write failure can still leave partial output: its owner must abort it.
func (c *Client) DownloadMedia(ctx context.Context, mediaURL string, dst io.Writer, maxBytes int64) (written int64, err error) {
	if mediaURL == "" || dst == nil {
		return 0, errors.New("whatsappx: missing media URL or destination")
	}
	if maxBytes <= 0 {
		maxBytes = c.config.MaxMediaBytes
	}
	if maxBytes > maxMediaUploadBytes {
		return 0, errors.New("whatsappx: invalid media byte limit")
	}
	policy := c.config.OutboundPolicy
	if policy == nil {
		policy = &security.OutboundURLPolicy{AllowedSchemes: []string{"https"}}
	}
	parsed, err := security.ValidateOutboundURL(ctx, mediaURL, *policy)
	if err != nil {
		return 0, fmt.Errorf("whatsappx: media download url safety violation: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || !(host == "lookaside.fbsbx.com" || host == "graph.facebook.com" || strings.HasSuffix(host, ".fbcdn.net") || strings.HasSuffix(host, ".facebook.com") || strings.HasSuffix(host, ".whatsapp.net")) {
		return 0, errors.New("whatsappx: not an authorized Meta domain")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	c.applyCorrelation(ctx, req)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, &ProviderError{Operation: "download", Class: OutcomeTransient}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, providerFailure("download", resp, nil, false)
	}
	spool, err := os.CreateTemp("", "whatsapp-download-*")
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, spool.Close(), os.Remove(spool.Name())) }()
	n, err := io.Copy(spool, io.LimitReader(contextReader{ctx, resp.Body}, maxBytes+1))
	if err != nil {
		return 0, err
	}
	if n > maxBytes {
		return 0, errors.New("whatsappx: media exceeds byte limit")
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return 0, io.ErrUnexpectedEOF
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return io.Copy(dst, spool)
}
func (c *Client) applyCorrelation(ctx context.Context, req *http.Request) {
	if corr := metadata.FromContext(ctx).CorrelationID; corr != "" {
		req.Header.Set("X-Correlation-ID", corr)
	}
}

// Reader owners must unblock an already running Read on cancellation.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
