package whatsappx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type testInbox struct {
	fail       bool
	deliveries []WebhookDelivery
}

func (i *testInbox) Accept(_ context.Context, d WebhookDelivery) error {
	if i.fail {
		return errors.New("storage offline")
	}
	i.deliveries = append(i.deliveries, d)
	return nil
}
func signedDelivery(h *WebhookHandler, body string) int {
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	res := httptest.NewRecorder()
	h.HandleEvent(res, req)
	return res.Code
}

const durablePayload = `{"object":"whatsapp_business_account","entry":[{"id":"account","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"phone"},"messages":[{"id":"m1","from":"2348012345678","type":"image","context":{"id":"original"},"image":{"id":"image1","caption":"Ọlá"}}]}}]}]}`

func TestDurableAcceptanceAndMapping(t *testing.T) {
	inbox := &testInbox{fail: true}
	cfg := WebhookConfig{VerifyToken: "verify", AppSecret: "secret", Accounts: []WebhookAccount{{BusinessAccountID: "account", PhoneNumberID: "phone", TenantID: "tenant"}}}
	h, err := NewDurableWebhookHandler(cfg, inbox)
	if err != nil {
		t.Fatal(err)
	}
	if got := signedDelivery(h, durablePayload); got != 503 {
		t.Fatalf("failed persistence acknowledged: %d", got)
	}
	inbox.fail = false
	for n := 0; n < 2; n++ {
		if got := signedDelivery(h, durablePayload); got != 200 {
			t.Fatal(got)
		}
	}
	if inbox.deliveries[0].Records[0].Key != inbox.deliveries[1].Records[0].Key {
		t.Fatal("retry identity changed")
	}
	d := inbox.deliveries[0]
	if d.Records[0].TenantID != "tenant" || d.Records[0].Message.Image.Caption != "Ọlá" || d.Records[0].Message.Context.ID != "original" || string(d.RawBody) != durablePayload {
		t.Fatal("evidence or mapping lost")
	}
	for _, body := range []string{strings.Replace(durablePayload, "account", "unknown", 1), strings.Replace(durablePayload, "messages", "other", 1), strings.Replace(durablePayload, "whatsapp_business_account", "wrong", 1), `{"object":"whatsapp_business_account","entry":[]}`} {
		if signedDelivery(h, body) == 200 {
			t.Fatal("unconfigured event accepted")
		}
	}
	if len(inbox.deliveries) != 2 {
		t.Fatal("invalid mapping reached storage")
	}
	cfg.VerifyToken = ""
	if _, err := NewDurableWebhookHandler(cfg, inbox); err == nil {
		t.Fatal("empty verification token accepted")
	}
}
func TestWebhookBodyBoundary(t *testing.T) {
	count := 0
	h := NewWebhookHandler("verify", "secret", func(WebhookEvent) { count++ })
	prefix := `{"object":"`
	suffix := `"}`
	body := prefix + strings.Repeat("a", maxWebhookBodyBytes-len(prefix)-len(suffix)) + suffix
	if got := signedDelivery(h, body); got != 200 {
		t.Fatal(got)
	}
	if got := signedDelivery(h, body+" "); got != 413 {
		t.Fatal(got)
	}
	if count != 1 {
		t.Fatal("oversize delivered")
	}
	empty := NewWebhookHandler("", "secret", func(WebhookEvent) { t.Fatal("empty token dispatched") })
	if signedDelivery(empty, `{}`) != 503 {
		t.Fatal("empty token did not fail closed")
	}
}

func TestIngressConfigurationAndReceipts(t *testing.T) {
	inbox := &testInbox{}
	base := WebhookConfig{VerifyToken: "verify", AppSecret: "secret", Accounts: []WebhookAccount{{BusinessAccountID: "account", PhoneNumberID: "phone", TenantID: "tenant"}}}
	for _, cfg := range []WebhookConfig{{}, {VerifyToken: "v", AppSecret: "s"}, {VerifyToken: "v", AppSecret: "s", Accounts: []WebhookAccount{{}}}, {VerifyToken: "v", AppSecret: "s", Accounts: append(base.Accounts, base.Accounts...)}, {VerifyToken: "v", AppSecret: "s", Accounts: base.Accounts, AcceptanceTimeout: -1}} {
		if _, err := NewDurableWebhookHandler(cfg, inbox); err == nil {
			t.Fatal("invalid ingress accepted")
		}
	}
	h, err := NewDurableWebhookHandler(base, inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Replace(durablePayload, `"id":"m1"`, `"id":""`, 1), strings.Replace(durablePayload, `"from":"2348012345678"`, `"from":""`, 1), `{"object":"whatsapp_business_account","entry":[{"id":"account","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"phone"},"statuses":[{"id":"m1","status":"failed","timestamp":"1","errors":[{"code":131000,"error_subcode":1,"fbtrace_id":"trace"}]}]}}]}]}`} {
		status := signedDelivery(h, body)
		if strings.Contains(body, `"statuses"`) {
			if status != 200 || inbox.deliveries[len(inbox.deliveries)-1].Records[0].Status.Errors[0].Code != 131000 {
				t.Fatal("receipt lost")
			}
		} else if status == 200 {
			t.Fatal("invalid identity accepted")
		}
	}
	res := httptest.NewRecorder()
	h.HandleEvent(res, httptest.NewRequest("GET", "/", nil))
	if res.Code != 405 {
		t.Fatal(res.Code)
	}
	res = httptest.NewRecorder()
	h.HandleVerification(res, httptest.NewRequest("POST", "/", nil))
	if res.Code != 405 {
		t.Fatal(res.Code)
	}
	res = httptest.NewRecorder()
	NewWebhookHandler("", "secret", nil).HandleVerification(res, httptest.NewRequest("GET", "/", nil))
	if res.Code != 503 {
		t.Fatal(res.Code)
	}
	if signedDelivery(NewWebhookHandler("verify", "secret", nil), `{}`) != 503 {
		t.Fatal("missing callback acknowledged")
	}
	if signedDelivery(h, `broken`) != 400 {
		t.Fatal("malformed JSON acknowledged")
	}
}

func TestDurableUnsupportedAndEmptyEvents(t *testing.T) {
	inbox := &testInbox{}
	h, err := NewDurableWebhookHandler(WebhookConfig{VerifyToken: "verify", AppSecret: "secret", Accounts: []WebhookAccount{{"account", "phone", "tenant"}}}, inbox)
	if err != nil {
		t.Fatal(err)
	}
	base := WebhookEvent{Object: "whatsapp_business_account", Entry: []WebhookEntry{{ID: "account", Changes: []WebhookChange{{Field: "messages", Value: ChangeValue{MessagingProduct: "whatsapp", Metadata: ChangeMetadata{PhoneNumberID: "phone"}}}}}}}
	if err := h.durable.accept(context.Background(), base, nil); err == nil {
		t.Fatal("empty event accepted")
	}
	base.Entry[0].Changes[0].Value.Statuses = []MessageStatus{{ID: "id"}}
	if err := h.durable.accept(context.Background(), base, nil); err == nil {
		t.Fatal("incomplete status accepted")
	}
	base.Entry[0].Changes[0].Value.Statuses = nil
	base.Entry[0].Changes[0].Value.Messages = make([]InboundMessage, 1001)
	for i := range base.Entry[0].Changes[0].Value.Messages {
		base.Entry[0].Changes[0].Value.Messages[i] = InboundMessage{ID: "id", From: "sender", Type: "unknown"}
	}
	if err := h.durable.accept(context.Background(), base, nil); err == nil {
		t.Fatal("event cap ignored")
	}
	base.Entry[0].Changes[0].Value.Messages = base.Entry[0].Changes[0].Value.Messages[:1]
	if err := h.durable.accept(context.Background(), base, []byte(`{"unknown":"evidence"}`)); err != nil {
		t.Fatal(err)
	}
	if inbox.deliveries[0].Records[0].Message.Type != "unknown" {
		t.Fatal("unsupported evidence discarded")
	}
	h = NewWebhookHandler("verify", "secret", nil)
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Hub-Signature-256", "sha256=00")
	r.Body = io.NopCloser(failingReader{})
	res := httptest.NewRecorder()
	h.HandleEvent(res, r)
	if res.Code != 400 {
		t.Fatal(res.Code)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	res = httptest.NewRecorder()
	h.HandleEvent(res, r)
	if res.Code != 401 {
		t.Fatal(res.Code)
	}
}
