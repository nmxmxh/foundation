package smsx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Sender defines the contract for delivering SMS messages.
type Sender interface {
	// Send delivers one SMS to a recipient.
	Send(ctx context.Context, msg Message) (*SendResult, error)

	// Probe checks health and connectivity of the SMS provider.
	Probe(ctx context.Context) (adapter.Health, error)

	// Close releases any resources held by the sender.
	Close() error
}

// Message represents an outbound SMS message.
type Message struct {
	From              string `json:"from"`
	To                string `json:"to"`
	Body              string `json:"body"`
	StatusCallbackURL string `json:"status_callback_url,omitempty"`
}

// SendResult contains confirmation details for a sent SMS.
type SendResult struct {
	MessageID string    `json:"message_id"`
	Status    string    `json:"status"` // queued, sent, failed
	Provider  string    `json:"provider"`
	SentAt    time.Time `json:"sent_at"`
}

// NormalizeE164 validates and standardizes phone numbers into E.164 format (+[country][number]).
func NormalizeE164(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("smsx: phone number cannot be empty")
	}

	var b strings.Builder
	hasPlus := strings.HasPrefix(s, "+")
	if hasPlus {
		b.WriteRune('+')
	}

	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}

	res := b.String()
	if !hasPlus {
		res = "+" + res
	}

	digits := strings.TrimPrefix(res, "+")
	if len(digits) < 7 || len(digits) > 15 {
		return "", fmt.Errorf("smsx: invalid E.164 phone length (%d digits), expected 7-15", len(digits))
	}

	return res, nil
}
