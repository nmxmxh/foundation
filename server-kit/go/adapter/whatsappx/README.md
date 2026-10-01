# WhatsApp Cloud API Adapter

`whatsappx` provides messaging capabilities, voice tools, and webhook processing for the Meta WhatsApp Cloud API (Graph API v21.0).

## Public contract

- `Client`: executes outbound text, template, interactive, voice, media, location, and reaction messages with circuit breaker protection.
- `Client.MarkRead`: marks messages as read, triggering blue checkmarks.
- `WebhookHandler`: processes verification handshakes (`hub.challenge`) and validates HMAC-SHA256 signatures (`X-Hub-Signature-256`).
- `VerifySignature`: performs constant-time HMAC-SHA256 signature verification.
- `NewAdapter`: wraps the client into an `adapter.Provider`.

## Presets & Voice Tools

| Preset / Tool | Function | Purpose |
| :--- | :--- | :--- |
| **Voice Note (PTT)** | `NewVoiceNote(to, mediaIDOrURL)` | Sends a voice memo with waveform and green microphone icon. |
| **Audio Track** | `NewAudioMessage(to, mediaIDOrURL)` | Sends a standard audio file. |
| **Quick Replies** | `NewQuickReply(to, body, buttons...)` | Sends 1 to 3 interactive reply buttons. |
| **Confirmation** | `NewConfirmationPrompt(to, head, body, yes, no)` | Sends a two-button confirmation dialog. |
| **List Menu** | `NewListMenu(to, body, btn, sections...)` | Sends an interactive dropdown menu with sections and rows. |
| **Authentication OTP** | `NewOTPPreset(to, tpl, lang, code)` | Sends a one-time passcode with copy-code button. |
| **Order Status** | `NewOrderStatusPreset(to, tpl, lang, cust, id, status)` | Sends transactional order status updates. |
| **Location Card** | `NewLocation(to, lat, long, name, addr)` | Sends map pin coordinates and location details. |
| **Emoji Reaction** | `NewReaction(to, targetMsgID, emoji)` | Attaches an emoji reaction to an existing message. |

## Inbound Webhook Features

Inbound webhooks automatically parse:
1. **Customer Voice Notes**: audio payloads with `mime_type` and `voice: true` ready for audio processing and transcription pipelines.
2. **Interactive Selections**: `button_reply` and `list_reply` payload IDs.
3. **Delivery Receipts**: status events (`sent`, `delivered`, `read`, `failed`).
4. **Geolocation Pins**: inbound coordinates and addresses.
