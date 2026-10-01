# Transactional Email Adapter

`emailx` provides transactional email delivery capabilities for Foundation applications.
It supports SMTP with STARTTLS and queue identifier parsing, Stalwart JMAP mail submission (RFC 8621),
modern HTTP REST email APIs, RFC 5322 MIME multipart construction, and in-line DKIM1 signing.

## Public contract

- `Sender`: universal email interface with `Send`, `Probe`, and `Close`.
- `SMTPSender`: delivery via SMTP (STARTTLS on port 587 or TLS on port 465) with automatic Stalwart hex-to-decimal queue ID parsing.
- `JMAPSender`: high-throughput delivery to Stalwart mailboxes via RFC 8621 `EmailSubmission/set`.
- `APISender`: transactional HTTP REST delivery (Resend, SendGrid, Postmark) with circuit breaker protection.
- `BuildRFC5322`: constructs standards-compliant MIME payloads with RFC 2047 header encoding.
- `SignDKIM`: in-line DKIM1 RSA-SHA256 (2048-bit) relaxed/relaxed canonicalization and signing.
- `FormatDKIMRecord`: generates RFC 1035 chunked strings (`<= 255` bytes) for public key DNS records.
- `NewAdapter`: wraps any email sender into an `adapter.Provider`.

## Deliverability Features

1. **One-Click Unsubscribe (RFC 8058)**: Injects `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers for compliance with bulk-sender regulations.
2. **In-Line DKIM1 Signing**: Cryptographically signs outbound messages directly at the adapter boundary before SMTP submission.
3. **Queue ID Telemetry**: Extracts decimal queue references from SMTP replies (`queued with id <hex>` or `queued as <id>`) for delivery event correlation.
4. **MIME Multi-part Support**: Handles plain text, HTML alternatives, base64 file attachments, and inline images (`cid:`).
