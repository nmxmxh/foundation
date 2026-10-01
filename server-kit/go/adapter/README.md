# Adapter

The `adapter` module provides a unified framework for external service integrations.
It manages outbound communication with third-party APIs.

## Public contract

- `Provider` interface: standardized contract implemented by all adapter subpackages.
- `Manager`: thread-safe coordinator for adapter registration, health checks, and lifecycle management.
- `Health`: tri-state-plus operational status (`serving`, `degraded`, `not_serving`, `unknown`).
- `Status`: immutable health snapshot with latency and error metrics.

## Subpackages

| Subpackage | Purpose | Protocols & Standards |
| :--- | :--- | :--- |
| `pushx` | Bounded notification outbox drainer | Web Push (RFC 8291/8292), FCM v1 REST, Apple APNs HTTP/2 |
| `oauthx` | Identity and access management | Google OAuth 2.0 (PKCE S256), OpenID Connect 1.0 |
| `whatsappx` | Meta WhatsApp Cloud API | Text, template, media messaging, HMAC-SHA256 webhooks |
| `emailx` | Transactional email delivery | SMTP (STARTTLS/TLS), Stalwart JMAP, HTTP REST, DKIM1 |
| `smsx` | SMS gateway delivery | Twilio REST API, E.164 phone normalization |

## Architecture

The adapter framework isolates third-party protocols from application logic.
Applications interact through standard interfaces.
Each adapter incorporates circuit breaking, bounded retries, and correlation propagation.

```text
Application Domain Services / Workers
                  │
                  ▼
          adapter.Manager
   ┌──────────────┼──────────────┬──────────────┬──────────────┐
   ▼              ▼              ▼              ▼              ▼
pushx/         oauthx/       whatsappx/      emailx/        smsx/
(Push/APNs/FCM) (Google OAuth) (Meta Graph)   (SMTP/JMAP)    (Twilio)
```

## Security

1. Credentials, access tokens, and private keys must never appear in logs or error messages.
2. Webhooks require constant-time signature verification (`crypto/subtle.ConstantTimeCompare`).
3. Outbound network requests enforce bounded read limits (`io.LimitReader`).
4. HTTP redirects are disabled by default to prevent SSRF vulnerabilities.
5. All mutating operations require correlation identifiers.
