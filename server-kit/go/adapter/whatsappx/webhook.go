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
	durable     *durableIngress
}

// NewWebhookHandler creates a legacy observer handler.
// Deprecated: use NewDurableWebhookHandler for persistence-aware acknowledgment.
func NewWebhookHandler(verifyToken, appSecret string, onEvent func(WebhookEvent)) *WebhookHandler {
	return &WebhookHandler{
		verifyToken: verifyToken,
		appSecret:   appSecret,
		onEvent:     onEvent,
	}
}

// HandleVerification processes the GET subscription verification handshake.
func (h *WebhookHandler) HandleVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.verifyToken == "" || h.appSecret == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	query := r.URL.Query()
	for _, key := range []string{"hub.mode", "hub.verify_token", "hub.challenge"} {
		if len(query[key]) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	mode := query.Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if challenge == "" || mode != "subscribe" || subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, challenge)
}

// HandleEvent processes inbound POST events with signature verification.
func (h *WebhookHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.verifyToken == "" || h.appSecret == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		http.Error(w, "Unauthorized: missing signature", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if len(body) > maxWebhookBodyBytes {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
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

	if h.durable != nil {
		if err := h.durable.accept(r.Context(), event, body); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	} else if h.onEvent != nil {
		h.onEvent(event)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
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
	Context     *InboundContext     `json:"context,omitempty"`
	Image       *InboundMedia       `json:"image,omitempty"`
	Video       *InboundMedia       `json:"video,omitempty"`
	Document    *InboundMedia       `json:"document,omitempty"`
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
	Type        string            `json:"type"` // button_reply, list_reply, nfm_reply
	FlowReply   *InboundFlowReply `json:"nfm_reply,omitempty"`
	ButtonReply *InboundReplyRef  `json:"button_reply,omitempty"`
	ListReply   *InboundReplyRef  `json:"list_reply,omitempty"`
}

// InboundReplyRef contains the ID and Title selected by the user.
type InboundReplyRef struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// MessageStatus represents delivery lifecycle status updates.
type MessageStatus struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"` // sent, delivered, read, failed
	Timestamp   string            `json:"timestamp"`
	RecipientID string            `json:"recipient_id"`
	Errors      []ProviderFailure `json:"errors,omitempty"`
}

// InboundContext links a reply to its original message.
type InboundContext struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Forwarded bool   `json:"forwarded,omitempty"`
}

// InboundMedia preserves image captions and document metadata.
type InboundMedia struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Sha256   string `json:"sha256"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// InboundFlowReply retains the bounded response for application schema validation.
type InboundFlowReply struct {
	Name         string `json:"name"`
	Body         string `json:"body"`
	ResponseJSON string `json:"response_json"`
}
