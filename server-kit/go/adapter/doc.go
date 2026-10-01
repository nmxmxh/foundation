// Package adapter provides a unified framework for external service integrations.
//
// Subpackages implement the Provider interface. The Manager coordinates
// lifecycle, health checks, and status reporting across all registered adapters.
//
// Subpackages:
//
//   - pushx:     Bounded push notification delivery (Web Push, APNs, FCM).
//   - oauthx:    Google OAuth 2.0 authorization and OIDC token validation.
//   - whatsappx: WhatsApp Cloud API messaging and webhook verification.
//   - emailx:    Transactional email delivery with SMTP, JMAP, and HTTP.
//   - smsx:      SMS delivery with the Twilio REST API.
//
// The core package does not import concrete adapter subpackages. Adding
// an adapter never touches core code.
package adapter
