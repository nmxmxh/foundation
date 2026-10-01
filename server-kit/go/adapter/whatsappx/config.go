package whatsappx

import (
	"errors"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// Config contains credentials and settings for WhatsApp Cloud API.
type Config struct {
	// PhoneNumberID is the unique identifier for the sending phone number.
	PhoneNumberID string

	// AccessToken is the permanent System User Bearer token.
	// NOTE: This value must never appear in logs or error messages.
	AccessToken string

	// AppSecret is used for verifying inbound webhook HMAC-SHA256 signatures.
	AppSecret string

	// WebhookVerifyToken is the secret string for the GET webhook verification handshake.
	WebhookVerifyToken string

	// GraphAPIVersion is the Meta Graph API version (defaults to "v21.0").
	GraphAPIVersion string

	// BaseURL can override the Graph API endpoint for testing.
	BaseURL string

	// OutboundPolicy customizes egress URL safety validation for media downloads.
	OutboundPolicy *security.OutboundURLPolicy
}

// Valid validates that required fields are present and bounded.
func (c Config) Valid() error {
	if c.PhoneNumberID == "" {
		return errors.New("whatsappx: PhoneNumberID is required")
	}
	if c.AccessToken == "" {
		return errors.New("whatsappx: AccessToken is required")
	}
	if c.AppSecret == "" {
		return errors.New("whatsappx: AppSecret is required")
	}
	if len(c.AccessToken) > 1024 || len(c.AppSecret) > 512 {
		return errors.New("whatsappx: credential length exceeds limits")
	}
	return nil
}
