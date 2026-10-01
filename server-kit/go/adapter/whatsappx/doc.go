// Package whatsappx provides a WhatsApp Cloud API adapter for messaging and webhooks.
//
// Outbound messages use the Meta Graph API (v21.0). Inbound webhooks
// verify HMAC-SHA256 signatures and handshake tokens.
//
// Operations use circuit breaking, bounded retries, and correlation propagation.
package whatsappx
