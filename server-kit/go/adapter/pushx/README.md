# Push Delivery Adapter

`pushx` drains an application outbox through bounded notification transport adapters.
It supports Web Push (RFC 8291/8292), Firebase Cloud Messaging (FCM v1 REST), and Apple Push Notification service (APNs HTTP/2).

## Public contract

- `Store.Claim`: atomically leases eligible delivery records.
- `Store.Complete`: records an outcome for the same tenant, recipient, and lease attempt.
- `Sender.Send`: returns `accepted`, `retry`, `expired`, or `rejected`.
- `Runner.Once`: handles one batch. `Runner.Run` adds cancellation, wake signals, and adaptive idle waits.
- `WebPush`: encrypts browser payloads and signs requests with VAPID keys.
- `FCMSender`: delivers messages using FCM v1 REST API with bearer token authentication.
- `APNsSender`: delivers notifications via Apple APNs HTTP/2 with ES256 token authentication.
- `NewAdapter`: wraps the runner into a managed `adapter.Provider`.

## Resource budget

Defaults use eight records per batch, one active send, eight seconds per attempt, and five seconds per store operation.
Idle waits grow from 250 milliseconds to 30 seconds. A coalesced wake signal interrupts the wait.

Retries stop after five attempts. Retry delays cannot exceed five minutes.
The Web Push, FCM, and APNs adapters cap payloads at 3000 bytes.
Expired destinations must remove their device token or subscription. Rejected messages stop retrying.

## Security

Web Push and APNs enforce HTTPS and explicit provider host allowlists. Redirects remain disabled.
Private keys, device tokens, payloads, and authentication secrets must never appear in logs.
Missing credentials must disable delivery in the host application.
