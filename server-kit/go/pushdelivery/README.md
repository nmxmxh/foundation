# Push delivery

`pushdelivery` drains an application outbox through a bounded transport adapter.
It does not keep client applications awake.

## Public contract

- `Store.Claim` atomically leases eligible delivery records.
- `Store.Complete` records an outcome for the same tenant, recipient, and lease attempt.
- `Sender.Send` returns `accepted`, `retry`, `expired`, or `rejected`.
- `Runner.Once` handles one batch. `Runner.Run` adds cancellation, wake signals, and adaptive idle waits.
- `WebPush` encrypts browser payloads and signs requests with VAPID keys.
- `Options.Observe` supplies delivery identifiers, correlation identifiers, attempts, and outcomes.

Applications own authorization, subscription storage, notification preferences, recipient policy, and copy.
Create outbox records in the domain transaction. Never send a notification before that transaction commits.
Scope every record with a tenant, recipient, correlation identifier, and stable event identifier.

## Resource budget

Defaults use eight records per batch, one active send, eight seconds per attempt, and five seconds per store operation.
Idle waits grow from 250 milliseconds to 30 seconds. A coalesced wake signal interrupts the wait.
No empty River jobs are created. A signal channel must have bounded capacity.
Use committed database notifications or an existing event subscription for wake signals.

A store lease must exceed the full batch budget.
For default limits, use at least 120 seconds and compare the attempt number when completing a lease.
Store operations and senders must obey context cancellation.
Long-lived loops end when their host context ends.

Retries stop after five attempts. Retry delays cannot exceed five minutes.
The Web Push adapter caps payloads at 3000 bytes and uses a one-hour provider TTL.
Provider acceptance does not prove device receipt.
A process crash can repeat an accepted send. Use stable notification tags for device deduplication.

## Security and fallback

Web Push uses HTTPS and an explicit browser-service host allowlist. Redirects are disabled.
Subscription keys require valid P-256 and authentication data.
Private keys, subscription endpoints, payloads, and authentication secrets must never appear in logs.
Store implementations must recheck recipient eligibility when claiming records.
Expired destinations should remove their subscription. Rejected messages should stop retrying.

Provider failures preserve application success and durable retry state.
Lost wake signals fall back to bounded reads. Empty stores perform about two reads per minute after backoff.
Missing credentials must disable delivery in the host application.
Notifications are optional hints. Applications must fetch authorized current state when users return.

## Web and native boundary

Web Push wakes browser service workers, including supported installed PWAs.
The browser and operating system control notification presentation and background sound.
Custom audio can play after user interaction while a web app or Tauri view is open.

Mobile operating systems can suspend application networking and execution.
Native applications use their operating system push transport for reliable background delivery.
APNs and FCM can implement `Sender`; this package currently ships only the Web Push adapter.
Native token registration, credentials, entitlements, notification channels, and custom sound packaging remain separate integration work.
An always-running application socket is not a portable background transport.

References: [Push API](https://developer.mozilla.org/en-US/docs/Web/API/Push_API),
[Android Doze](https://developer.android.com/training/monitoring-device-state/doze-standby),
[Apple background notifications](https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app).

## Evidence ledger

Contract: new additive `pushdelivery` package; no existing Foundation API changed.
Invariant: bounded delivery preserves tenant, recipient, correlation, and lease identity.
Scope: Foundation owns dispatch policy and transports. Applications own durable storage and product events.
Fallback: bounded retries, lease recovery, idle reads, and optional delivery.
Regression guards: configuration bounds, cancellation, closed channels, identity validation, encryption, SSRF restrictions, and transport outcomes.

Validation on Apple M1 Pro, Go 1.26:

- `go test -race -coverprofile=coverage.out ./pushdelivery`: 99.1% statement coverage.
- `go test -run '^$' -bench . -benchmem ./pushdelivery`: empty batch, 255.5 ns/op, 272 B/op, four allocations.

The benchmark measures scheduler overhead with a stub store. It excludes database, provider, and device latency.
No live provider credentials or physical mobile devices were used.
