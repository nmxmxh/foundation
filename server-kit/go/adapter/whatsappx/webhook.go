package whatsappx

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const (
	maxWebhookBodyBytes = 1 << 20 // 1 MiB
	signaturePrefix     = "sha256="
)

// VerifySignature validates that the request body matches the HMAC-SHA256 signature.
func VerifySignature(rawBody []byte, signatureHeader, appSecret string) bool {
	if !strings.HasPrefix(signatureHeader, signaturePrefix) || appSecret == "" {
		return false
	}
	actualSig, err := hex.DecodeString(signatureHeader[len(signaturePrefix):])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(rawBody)
	expectedSig := mac.Sum(nil)

	return subtle.ConstantTimeCompare(actualSig, expectedSig) == 1
}

// WebhookHandler handles Meta verification handshakes and event dispatch.
type WebhookHandler struct {
	verifyToken string
	appSecret   string
	onEvent     func(WebhookEvent)
}

// NewWebhookHandler creates an initialized webhook handler.
func NewWebhookHandler(verifyToken, appSecret string, onEvent func(WebhookEvent)) *WebhookHandler {
	return &WebhookHandler{
		verifyToken: verifyToken,
		appSecret:   appSecret,
		onEvent:     onEvent,
	}
}

// HandleVerification processes the GET subscription verification handshake.
func (h *WebhookHandler) HandleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode != "subscribe" || subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, challenge)
}

// HandleEvent processes inbound POST events with signature verification.
func (h *WebhookHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		http.Error(w, "Unauthorized: missing signature", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if !VerifySignature(body, sig, h.appSecret) {
		http.Error(w, "Unauthorized: signature mismatch", http.StatusUnauthorized)
		return
	}

	var event WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Bad Request: malformed JSON", http.StatusBadRequest)
		return
	}

	if h.onEvent != nil {
		h.onEvent(event)
	}

	w.WriteHeader(http.StatusOK)
}

// WebhookEvent represents an incoming webhook payload from Meta.
type WebhookEvent struct {
	Object string         `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

// WebhookEntry contains changes for one WhatsApp Business Account.
type WebhookEntry struct {
	ID      string          `json:"id"`
	Changes []WebhookChange `json:"changes"`
}

// WebhookChange contains the notification change payload.
type WebhookChange struct {
	Field string      `json:"field"`
	Value ChangeValue `json:"value"`
}

// ChangeValue holds inbound messages and status updates.
type ChangeValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Metadata         ChangeMetadata   `json:"metadata"`
	Messages         []InboundMessage `json:"messages,omitempty"`
	Statuses         []MessageStatus  `json:"statuses,omitempty"`
}

type ChangeMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

// InboundMessage represents a received WhatsApp message.
type InboundMessage struct {
	From        string              `json:"from"`
	ID          string              `json:"id"`
	Timestamp   string              `json:"timestamp"`
	Type        string              `json:"type"` // text, audio, interactive, location, reaction, image
	Text        *InboundText        `json:"text,omitempty"`
	Audio       *InboundAudio       `json:"audio,omitempty"`
	Interactive *InboundInteractive `json:"interactive,omitempty"`
	Location    *LocationBody       `json:"location,omitempty"`
	Reaction    *ReactionBody       `json:"reaction,omitempty"`
}

// InboundText holds plain text message content.
type InboundText struct {
	Body string `json:"body"`
}

// InboundAudio holds audio metadata, including voice notes.
type InboundAudio struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Sha256   string `json:"sha256"`
	Voice    bool   `json:"voice"` // True if received as a push-to-talk voice note
}

// InboundInteractive holds responses from quick replies or list menus.
type InboundInteractive struct {
	Type        string           `json:"type"` // button_reply, list_reply
	ButtonReply *InboundReplyRef `json:"button_reply,omitempty"`
	ListReply   *InboundReplyRef `json:"list_reply,omitempty"`
}

// InboundReplyRef contains the ID and Title selected by the user.
type InboundReplyRef struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// MessageStatus represents delivery lifecycle status updates.
type MessageStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // sent, delivered, read, failed
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"`
}
