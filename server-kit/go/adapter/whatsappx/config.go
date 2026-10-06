package whatsappx

import (
	"errors"
	"regexp"

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

	// GraphAPIVersion is the Meta Graph API version (required explicit provider-supported version).
	GraphAPIVersion string

	// BaseURL can override the Graph API endpoint for testing.
	BaseURL string

	// OutboundPolicy customizes egress URL safety validation for media downloads.
	OutboundPolicy *security.OutboundURLPolicy

	// MaxMediaBytes defaults to 8 MiB; larger limits require an explicit product decision.
	MaxMediaBytes int64
	// MaxConcurrentUploads defaults to two per client.
	MaxConcurrentUploads int
}

var graphVersionPattern = regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,2}$`)

// Valid validates that required fields are present and bounded.
func (c Config) Valid() error {
	if !validPathID(c.PhoneNumberID) {
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
	if !graphVersionPattern.MatchString(c.GraphAPIVersion) {
		return errors.New("whatsappx: explicit GraphAPIVersion is required")
	}
	if c.MaxMediaBytes < 0 || c.MaxMediaBytes > maxMediaUploadBytes || c.MaxConcurrentUploads < 0 || c.MaxConcurrentUploads > 16 {
		return errors.New("whatsappx: invalid media limits")
	}
	return nil
}

func validPathID(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
