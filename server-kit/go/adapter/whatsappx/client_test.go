package whatsappx

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWhatsAppConfigValidation(t *testing.T) {
	c := Config{}
	if err := c.Valid(); err == nil {
		t.Fatal("empty config should be invalid")
	}

	c = Config{
		PhoneNumberID: "phone-123",
		AccessToken:   "token-456",
		AppSecret:     "secret-789",
	}
	if err := c.Valid(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	client, err := NewClient(c)
	if err != nil || client == nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = NewClient(Config{})
	if err == nil {
		t.Fatal("expected error with empty config")
	}
}

func TestWhatsAppSend(t *testing.T) {
	cfg := Config{
		PhoneNumberID: "10987654321",
		AccessToken:   "valid-token",
		AppSecret:     "app-secret",
	}
	client, _ := NewClient(cfg)

	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unauthorized"}}`))}, nil
		}
		if !strings.HasSuffix(r.URL.Path, "/10987654321/messages") {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"messaging_product": "whatsapp",
				"contacts": [{"input": "+1234567890", "wa_id": "1234567890"}],
				"messages": [{"id": "wamid.HBgL"}]
			}`)),
		}, nil
	})

	// Text message
	msg := NewTextMessage("+1234567890", "Order confirmed")
	res, err := client.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("failed to send text message: %v", err)
	}
	if res.MessageID != "wamid.HBgL" || res.Contact != "1234567890" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Template message
	tplMsg := NewTemplateMessage("+1234567890", "order_update", "en_US", "Alex", "ORD-123")
	res, err = client.Send(context.Background(), tplMsg)
	if err != nil {
		t.Fatalf("failed to send template message: %v", err)
	}
	if res.MessageID != "wamid.HBgL" {
		t.Fatalf("unexpected message ID: %s", res.MessageID)
	}

	// Document message
	docMsg := NewPDFInvoiceMessage("+1234567890", "https://cdn.example.com/invoice.pdf", "INV-101")
	res, err = client.Send(context.Background(), docMsg)
	if err != nil {
		t.Fatalf("failed to send document message: %v", err)
	}
	if res.MessageID != "wamid.HBgL" {
		t.Fatalf("unexpected message ID: %s", res.MessageID)
	}

	// Flow / Quiz message
	quizMsg := NewQuizFlow("+1234567890", "flow_99", "sess_1", "Daily Trivia", "Answer 3 questions")
	res, err = client.Send(context.Background(), quizMsg)
	if err != nil {
		t.Fatalf("failed to send flow message: %v", err)
	}
	if res.MessageID != "wamid.HBgL" {
		t.Fatalf("unexpected message ID: %s", res.MessageID)
	}

	// Contacts message
	contactMsg := NewContactCard("+1234567890", "John Smith", "+15550009999", "john@ovasabi.com", "Ovasabi", "Architect")
	res, err = client.Send(context.Background(), contactMsg)
	if err != nil {
		t.Fatalf("failed to send contact message: %v", err)
	}
	if res.MessageID != "wamid.HBgL" {
		t.Fatalf("unexpected message ID: %s", res.MessageID)
	}

	// Empty recipient
	if _, err := client.Send(context.Background(), Message{}); err == nil {
		t.Fatal("expected error with empty To")
	}
}

func TestUploadAndGetMedia(t *testing.T) {
	cfg := Config{
		PhoneNumberID: "10987654321",
		AccessToken:   "valid-token",
		AppSecret:     "app-secret",
	}
	client, _ := NewClient(cfg)

	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/media") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id": "media-id-999"}`)),
			}, nil
		}
		if strings.HasSuffix(r.URL.Path, "/media-id-999") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"url": "https://lookaside.fbsbx.com/v21.0/media-id-999"}`)),
			}, nil
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	validPNG := "\x89PNG\r\n\x1a\nimage-data-payload"
	id, err := client.UploadMedia(context.Background(), "image/png", strings.NewReader(validPNG), "../../test.png")
	if err != nil {
		t.Fatalf("upload media failed: %v", err)
	}
	if id != "media-id-999" {
		t.Fatalf("unexpected media ID: %s", id)
	}

	// Verify that mismatched content is rejected
	_, err = client.UploadMedia(context.Background(), "image/png", strings.NewReader("<html>malicious</html>"), "hack.png")
	if err == nil {
		t.Fatal("expected content mismatch error for html masquerading as png")
	}

	mediaURL, err := client.GetMediaURL(context.Background(), id)
	if err != nil {
		t.Fatalf("get media URL failed: %v", err)
	}
	if mediaURL != "https://lookaside.fbsbx.com/v21.0/media-id-999" {
		t.Fatalf("unexpected media URL: %s", mediaURL)
	}

	if _, err := client.GetMediaURL(context.Background(), ""); err == nil {
		t.Fatal("expected error with empty media ID")
	}
}

func TestDownloadMediaSecurity(t *testing.T) {
	mockResolver := func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("157.240.22.35")}, nil // Public IP
	}
	cfg := Config{
		PhoneNumberID: "10987654321",
		AccessToken:   "valid-token",
		AppSecret:     "app-secret",
		OutboundPolicy: &security.OutboundURLPolicy{
			AllowedSchemes:       []string{"https"},
			AllowPrivateNetworks: false,
			Resolver:             mockResolver,
		},
	}
	client, _ := NewClient(cfg)

	// 1. SSRF rejection for private loopback / non-https
	var outBuf bytes.Buffer
	_, err := client.DownloadMedia(context.Background(), "http://127.0.0.1:8080/secret", &outBuf, 1024)
	if err == nil || !strings.Contains(err.Error(), "safety violation") {
		t.Fatalf("expected SSRF safety violation, got: %v", err)
	}

	// 2. Rejection for non-Meta host
	_, err = client.DownloadMedia(context.Background(), "https://attacker.example.com/exploit.jpg", &outBuf, 1024)
	if err == nil || !strings.Contains(err.Error(), "not an authorized Meta domain") {
		t.Fatalf("expected unauthorized Meta domain error, got: %v", err)
	}

	// 3. Authorized Meta CDN download
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("unauthorized"))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("downloaded-file-content")),
		}, nil
	})

	outBuf.Reset()
	n, err := client.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/v21.0/media-id-999", &outBuf, 1024)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if n != int64(len("downloaded-file-content")) || outBuf.String() != "downloaded-file-content" {
		t.Fatalf("unexpected downloaded content: %s", outBuf.String())
	}
}

func TestMarkRead(t *testing.T) {
	cfg := Config{
		PhoneNumberID: "10987654321",
		AccessToken:   "valid-token",
		AppSecret:     "app-secret",
	}
	client, _ := NewClient(cfg)

	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"status":"read"`) {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
		}, nil
	})

	if err := client.MarkRead(context.Background(), "wamid.msg123"); err != nil {
		t.Fatalf("failed to mark read: %v", err)
	}

	if err := client.MarkRead(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty message ID")
	}
}
