package whatsappx

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func computeTestSig(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "my-secret-key"
	body := []byte(`{"entry":[]}`)

	validSig := computeTestSig(body, secret)
	if !VerifySignature(body, validSig, secret) {
		t.Fatal("expected valid signature to verify")
	}

	if VerifySignature(body, "sha256=invalidhex", secret) {
		t.Fatal("expected invalid hex to fail")
	}
	if VerifySignature(body, "invalidprefix", secret) {
		t.Fatal("expected invalid prefix to fail")
	}
	if VerifySignature(body, validSig, "wrong-secret") {
		t.Fatal("expected signature mismatch with wrong secret")
	}
}

func TestWebhookHandler(t *testing.T) {
	verifyToken := "verify-token-123"
	appSecret := "app-secret-456"

	var captured WebhookEvent
	handler := NewWebhookHandler(verifyToken, appSecret, func(e WebhookEvent) {
		captured = e
	})

	// 1. Test GET Verification Handshake
	req := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=verify-token-123&hub.challenge=challenge-789", nil)
	rr := httptest.NewRecorder()
	handler.HandleVerification(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if rr.Body.String() != "challenge-789" {
		t.Fatalf("expected challenge-789, got %s", rr.Body.String())
	}

	// Bad token
	badReq := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=challenge", nil)
	badRR := httptest.NewRecorder()
	handler.HandleVerification(badRR, badReq)
	if badRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bad token, got %d", badRR.Code)
	}

	// 2. Test POST Event with Signature
	eventBody := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "123",
			"changes": [{
				"field": "messages",
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "1234", "phone_number_id": "5678"},
					"messages": [{"id": "msg-1", "from": "111", "type": "text"}]
				}
			}]
		}]
	}`)
	sig := computeTestSig(eventBody, appSecret)

	postReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(eventBody))
	postReq.Header.Set("X-Hub-Signature-256", sig)
	postRR := httptest.NewRecorder()
	handler.HandleEvent(postRR, postReq)

	if postRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid event, got %d", postRR.Code)
	}
	if captured.Object != "whatsapp_business_account" || len(captured.Entry) == 0 {
		t.Fatalf("unexpected captured event: %+v", captured)
	}

	// Bad signature
	badPostReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(eventBody))
	badPostReq.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	badPostRR := httptest.NewRecorder()
	handler.HandleEvent(badPostRR, badPostReq)
	if badPostRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d", badPostRR.Code)
	}

	// 3. Test Inbound Voice Note and Button Reply
	voicePayload := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "123",
			"changes": [{
				"field": "messages",
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "1234", "phone_number_id": "5678"},
					"messages": [
						{
							"id": "voice-1",
							"from": "111",
							"type": "audio",
							"audio": {"id": "media-voice-99", "mime_type": "audio/ogg; codecs=opus", "voice": true}
						},
						{
							"id": "btn-1",
							"from": "111",
							"type": "interactive",
							"interactive": {"type": "button_reply", "button_reply": {"id": "btn_confirm", "title": "Confirm"}}
						}
					]
				}
			}]
		}]
	}`)
	voiceSig := computeTestSig(voicePayload, appSecret)
	voiceReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(voicePayload))
	voiceReq.Header.Set("X-Hub-Signature-256", voiceSig)
	voiceRR := httptest.NewRecorder()
	handler.HandleEvent(voiceRR, voiceReq)

	if voiceRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on voice event, got %d", voiceRR.Code)
	}
	messages := captured.Entry[0].Changes[0].Value.Messages
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Audio == nil || !messages[0].Audio.Voice {
		t.Fatal("expected voice note audio message")
	}
	if messages[1].Interactive == nil || messages[1].Interactive.ButtonReply.Title != "Confirm" {
		t.Fatal("expected button reply title Confirm")
	}
}
