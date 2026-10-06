package whatsappx

import (
	"bytes"
	"context"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWhatsAppConfigValidation(t *testing.T) {
	c := Config{GraphAPIVersion: "v21.0"}
	if err := c.Valid(); err == nil {
		t.Fatal("empty config should be invalid")
	}

	c = Config{GraphAPIVersion: "v21.0",
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

	_, err = NewClient(Config{GraphAPIVersion: "v21.0"})
	if err == nil {
		t.Fatal("expected error with empty config")
	}
}

func TestWhatsAppSend(t *testing.T) {
	cfg := Config{GraphAPIVersion: "v21.0",
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
	cfg := Config{GraphAPIVersion: "v21.0",
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
	cfg := Config{GraphAPIVersion: "v21.0",
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
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("downloaded-file-content")),
			ContentLength: int64(len("downloaded-file-content")),
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
	cfg := Config{GraphAPIVersion: "v21.0",
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

func TestMediaAndProviderFailureBoundaries(t *testing.T) {
	cfg := Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "secret-token", AppSecret: "secret", MaxMediaBytes: 8, OutboundPolicy: &security.OutboundURLPolicy{AllowedSchemes: []string{"https"}, AllowPrivateNetworks: true}}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{8, 9} {
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(strings.NewReader(strings.Repeat("a", size)))}, nil
		})
		var dst bytes.Buffer
		n, err := c.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/media", &dst, 8)
		if size == 8 && (err != nil || n != 8) {
			t.Fatalf("exact limit: %d %v", n, err)
		}
		if size == 9 && (err == nil || n != 0 || dst.Len() != 0) {
			t.Fatalf("partial published: %d %v", n, err)
		}
	}
	for _, body := range []string{`{}`, `{"url":""}`, `{"url":"http://lookaside.fbsbx.com/media"}`, `broken`} {
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := c.GetMediaURL(context.Background(), "id"); err == nil {
			t.Fatal("invalid URL response accepted")
		}
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"12"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"secret-token","code":4,"is_transient":true,"fbtrace_id":"trace"}}`))}, nil
	})
	_, err = c.Send(context.Background(), NewTextMessage("2348012345678", "hello"))
	failure, ok := err.(*ProviderError)
	if !ok || failure.Class != OutcomeTransient || failure.Failure.Code != 4 || failure.RetryAfter.Seconds() != 12 || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe/unstructured error: %v", err)
	}
	cfg.GraphAPIVersion = ""
	if _, err := NewClient(cfg); err == nil {
		t.Fatal("implicit version accepted")
	}
}

func TestUploadProductLimit(t *testing.T) {
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", MaxMediaBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"media"}`))}, nil
	})
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if _, err := c.UploadMedia(context.Background(), "image/png", bytes.NewReader(png), "a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UploadMedia(context.Background(), "image/png", bytes.NewReader(append(png, 'x')), "a.png"); err == nil {
		t.Fatal("oversize upload accepted")
	}
	if calls != 1 {
		t.Fatal("oversize reached provider")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestGraphFailureClassification(t *testing.T) {
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status int
		body   string
		class  OutcomeClass
	}{{500, `{"error":{"code":1}}`, OutcomeUnknown}, {400, `{"error":{"code":100}}`, OutcomePermanent}, {200, `broken`, OutcomeUnknown}, {200, `{}`, OutcomeUnknown}, {200, strings.Repeat("x", maxGraphResponse+1), OutcomeUnknown}, {302, `{}`, OutcomePermanent}} {
		c, err = NewClient(c.config)
		if err != nil {
			t.Fatal(err)
		}
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		_, err := c.Send(context.Background(), NewTextMessage("2348012345678", "hello"))
		p, ok := err.(*ProviderError)
		if !ok || p.Class != tc.class {
			t.Fatalf("%d: %v", tc.status, err)
		}
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
	if _, err := c.GetMediaURL(context.Background(), "id"); err == nil {
		t.Fatal("transport error ignored")
	}
	if err := c.MarkRead(context.Background(), "id"); err == nil {
		t.Fatal("transport error ignored")
	}
	if _, err := c.Send(context.Background(), NewTextMessage("", "hello")); err == nil {
		t.Fatal("invalid send")
	}
	if err := c.MarkRead(context.Background(), ""); err == nil {
		t.Fatal("invalid receipt")
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(failingReader{})}, nil
	})
	if _, err := c.GetMediaURL(context.Background(), "id"); err == nil {
		t.Fatal("read error ignored")
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	if err := c.MarkRead(context.Background(), "id"); err == nil {
		t.Fatal("missing success ignored")
	}
	if _, err := c.UploadMedia(context.Background(), "image/png", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}), ""); err == nil {
		t.Fatal("missing upload ID ignored")
	}
	for _, cfg := range []Config{{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token"}, {GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AppSecret: "secret"}, {GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: strings.Repeat("t", 1025), AppSecret: "secret"}, {GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", MaxMediaBytes: -1}, {GraphAPIVersion: "v21.0", PhoneNumberID: "../x", AccessToken: "token", AppSecret: "secret"}, {GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", BaseURL: "broken"}} {
		if _, err := NewClient(cfg); err == nil {
			t.Fatal("invalid config")
		}
	}
}
func TestDownloadFailureCleanup(t *testing.T) {
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", OutboundPolicy: &security.OutboundURLPolicy{AllowedSchemes: []string{"https"}, AllowPrivateNetworks: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status int
		length int64
		body   io.Reader
	}{{206, 3, strings.NewReader("abc")}, {200, 4, strings.NewReader("abc")}, {200, -1, failingReader{}}} {
		c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Body: io.NopCloser(tc.body)}, nil
		})
		var dst bytes.Buffer
		if _, err := c.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/media", &dst, 8); err == nil || dst.Len() != 0 {
			t.Fatal("partial content published")
		}
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(strings.NewReader("abc"))}, nil
	})
	if _, err := c.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/media", failingWriter{}, 0); err == nil {
		t.Fatal("writer error ignored")
	}
	for _, args := range []struct {
		url   string
		dst   io.Writer
		limit int64
	}{{"", io.Discard, 8}, {"https://lookaside.fbsbx.com/media", nil, 8}, {"https://lookaside.fbsbx.com/media", io.Discard, maxMediaUploadBytes + 1}} {
		if _, err := c.DownloadMedia(context.Background(), args.url, args.dst, args.limit); err == nil {
			t.Fatal("invalid download")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.UploadMedia(ctx, "image/png", strings.NewReader("abc"), "a.png"); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := c.UploadMedia(context.Background(), "unknown/type", strings.NewReader("abc"), "a.png"); err == nil {
		t.Fatal("unknown type")
	}
}

func TestCorrelationAndLocalFailures(t *testing.T) {
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", BaseURL: "http://localhost"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.IntoContext(context.Background(), metadata.EnvelopeMetadata{CorrelationID: "correlation"})
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Correlation-ID") != "correlation" {
			t.Fatal("missing correlation")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"messages":[{"id":"id"}]}`))}, nil
	})
	if _, err := c.Send(ctx, NewTextMessage("2348012345678", "hello")); err != nil {
		t.Fatal(err)
	}
	m := NewFlowMessage("2348012345678", "", "body", "", "id", "token", "start", "SCREEN", map[string]any{"bad": make(chan int)})
	if _, err := c.Send(context.Background(), m); err == nil {
		t.Fatal("non JSON data accepted")
	}
	m.Interactive.Action.Parameters.FlowActionPayload.Data = map[string]any{"huge": strings.Repeat("x", 256<<10)}
	if _, err := c.Send(context.Background(), m); err == nil {
		t.Fatal("request cap ignored")
	}
	p := providerFailure("upload", &http.Response{StatusCode: 500, Header: http.Header{"X-Fb-Request-Id": []string{strings.Repeat("x", 257)}}}, nil, false)
	if p.Class != OutcomeUnknown || p.Failure.RequestID != "" {
		t.Fatal("unsafe evidence")
	}
	NewVoiceNote("2348012345678", "id")
	NewDocumentMessage("2348012345678", "id", "file", "caption")
	if NewAdapter("", nil).Status().Health != adapter.HealthNotServing {
		t.Fatal("missing health")
	}
}

func TestTemporaryStorageFailures(t *testing.T) {
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", OutboundPolicy: &security.OutboundURLPolicy{AllowedSchemes: []string{"https"}, AllowPrivateNetworks: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	if _, err := c.UploadMedia(context.Background(), "image/png", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}), "a.png"); err == nil {
		t.Fatal("spool failure ignored")
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(strings.NewReader("abc"))}, nil
	})
	var dst bytes.Buffer
	if _, err := c.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/media", &dst, 8); err == nil || dst.Len() != 0 {
		t.Fatal("spool failure published")
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
	if _, err := c.DownloadMedia(context.Background(), "https://lookaside.fbsbx.com/media", io.Discard, 8); err == nil {
		t.Fatal("network failure ignored")
	}
}

func TestUploadConcurrencyAndCleanup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	c, err := NewClient(Config{GraphAPIVersion: "v21.0", PhoneNumberID: "phone", AccessToken: "token", AppSecret: "secret", MaxConcurrentUploads: 1})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.ContentLength <= 0 {
			t.Error("multipart length not bounded")
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
		} else if r.MultipartForm.File["file"][0].Header.Get("Content-Type") != "image/png" {
			t.Error("missing MIME type")
		}
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"media"}`))}, nil
	})
	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}
	done := make(chan error, 1)
	go func() {
		_, err := c.UploadMedia(context.Background(), "image/png", bytes.NewReader(png), "a.png")
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.UploadMedia(ctx, "image/png", bytes.NewReader(png), "b.png"); err != context.Canceled {
		t.Error(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("temporary upload leaked")
	}
}
