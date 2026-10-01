package emailx

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Sender defines the contract for delivering email messages.
type Sender interface {
	// Send delivers one email message through the provider.
	Send(ctx context.Context, msg Message) (*DeliveryReceipt, error)

	// Probe checks health and connectivity of the mail transport.
	Probe(ctx context.Context) (adapter.Health, error)

	// Close releases network connections or idle pools.
	Close() error
}

// Attachment represents an email attachment or inline asset.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"-"`
	ContentID   string `json:"content_id,omitempty"` // For inline images (cid:<content_id>)
}

// ListUnsubscribe contains configuration for RFC 8058 one-click unsubscribe.
type ListUnsubscribe struct {
	URL      string `json:"url,omitempty"`
	Email    string `json:"email,omitempty"`
	OneClick bool   `json:"one_click"` // Injects List-Unsubscribe-Post: List-Unsubscribe=One-Click
}

// DKIMConfig configures in-line cryptographic signing of outbound messages.
type DKIMConfig struct {
	Domain     string `json:"domain"`
	Selector   string `json:"selector"`
	PrivateKey string `json:"-"` // PKCS#1 or PKCS#8 PEM private key
}

// Message represents an outbound email message.
type Message struct {
	From            string            `json:"from"`
	To              []string          `json:"to"`
	Cc              []string          `json:"cc,omitempty"`
	Bcc             []string          `json:"bcc,omitempty"`
	ReplyTo         string            `json:"reply_to,omitempty"`
	Subject         string            `json:"subject"`
	TextBody        string            `json:"text_body,omitempty"`
	HTMLBody        string            `json:"html_body,omitempty"`
	Attachments     []Attachment      `json:"attachments,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	ListUnsubscribe *ListUnsubscribe  `json:"list_unsubscribe,omitempty"`
	ThreadKey       string            `json:"thread_key,omitempty"`
	ChannelRef      string            `json:"channel_ref,omitempty"`
	MessageID       string            `json:"message_id,omitempty"`
	DKIM            *DKIMConfig       `json:"dkim,omitempty"`
}

// DeliveryReceipt contains outcome metadata for a sent message.
type DeliveryReceipt struct {
	MessageID  string    `json:"message_id"`
	QueueID    string    `json:"queue_id,omitempty"` // Provider queue identifier (e.g. Stalwart)
	Provider   string    `json:"provider"`
	StatusCode int       `json:"status_code"`
	SentAt     time.Time `json:"sent_at"`
}
