# WhatsApp Cloud API adapter

`whatsappx` supplies bounded outbound messaging and authenticated webhook ingress.
Application databases own durable inboxes, sender/user mapping, consent and dispatch.
The shared adapter does not create logistics product tables.

## Public contracts and migration

- `NewClient(Config)` now requires an explicit `GraphAPIVersion`. Missing pins fail startup.
- `Message.Validate` runs before `Send`; unsupported or inconsistent variants never dispatch.
- `NewDurableWebhookHandler(WebhookConfig, WebhookInbox)` adds persistence-aware acknowledgment.
- `NewWebhookHandler` remains source compatible as a deprecated observer API. It cannot promise durable acceptance; migrate production routes to the new constructor.
- `ProviderError` exposes machine codes, subcodes, bounded request identifiers, retry hints and outcome classification without logging raw provider messages or credentials.
- `NewAdapter` exposes configured adapter readiness; its probe is not proof of live provider delivery.

## Durable ingress

Configure non-empty verification token and app secret at startup and an allowlist of
business account IDs and phone number IDs mapped to tenant IDs. Signature verification
precedes mapping. Display phone numbers and sender input never determine tenant identity.
The handler accepts only configured `messages` subscriptions for WhatsApp business accounts.
It validates the whole batch before invoking storage. Failed or uncertain persistence is
HTTP 503. Successful durable acceptance, including committed duplicates, is HTTP 200.
An extra sentinel byte detects bodies exceeding 1 MiB: exact-limit valid bodies pass and
limit-plus-one bodies return 413 before parsing. Configure upstream request deadlines too.

Implement `WebhookInbox.Accept` in application storage with one transaction:

1. Insert inbox records with a unique `(tenant_id, key)` constraint; committed duplicates are successful.
2. Retain the bounded `RawBody` evidence under application encryption and retention policy.
3. Insert pending processing jobs atomically with new inbox records.
4. Commit before returning nil; roll back the whole batch on failure. Do not dispatch inside acceptance.

Message keys identify account, phone and message ID. Receipt keys additionally include
status and timestamp. Retries retain the same keys, including when a commit outcome is
uncertain. The acceptance timeout defaults to five seconds and is bounded to thirty;
implementations must honor cancellation. A timeout does not prove that a commit failed.
The adapter supplies verified tenant/account metadata and the sender's `Message.From`;
application storage maps that sender to its own users and consent records.

Every supported event must be persisted before acknowledgment. Unknown message types
are retained for quarantine rather than silently discarded or executed. Raw evidence
preserves unmodeled fields; image captions, document/video metadata, reply context, Flow
responses and receipt error codes also have explicit models. Validate Flow response
schemas and authorization before enabling execution. Precise locations and personal
raw evidence need application-specific retention and disclosure controls.

## Supported outbound messages and actions

Text, text-parameter templates, images, documents, audio/voice notes, button/list/Flow
interactions, reactions, locations and contacts are modeled. Video's enum remains for
source compatibility, but sends are rejected until a complete product contract exists.
Validation checks required fields, mutually exclusive variants and media ID/link choices,
Unicode lengths, HTTPS links, coordinate bounds, button/list counts and IDs, and supported
interactive/template component types. Requests are additionally bounded to 256 KiB.
Template approval and language availability remain provider configuration obligations.

Existing presets remain available: `NewTextMessage`, `NewTemplateMessage`, `NewVoiceNote`,
`NewAudioMessage`, `NewDocumentMessage`, `NewPDFInvoiceMessage`, `NewQuickReply`,
`NewConfirmationPrompt`, `NewListMenu`, `NewFlowMessage`, `NewQuizFlow`, `NewSurveyFlow`,
`NewContactCard`, `NewOTPPreset`, `NewOrderStatusPreset`, `NewReaction`, and `NewLocation`.
`MarkRead` validates provider success rather than trusting HTTP status alone.

Quick-reply and confirmation helpers generate unique opaque IDs and truncate labels by
Unicode rune. Persist their conversation binding before sending. For cryptographic binding,
use `SignActionToken` with a separate secret of at least 32 bytes and `ActionScope` containing
tenant, recipient, conversation and action version. Supply tokens via `NewQuickReplyActions`.
On receipt, `VerifyActionToken` checks scope and expiry. The application must authorize and
atomically consume the action to prevent replay within the expiry window; a valid token
alone does not grant permission to dispatch. Use trustworthy expiry clocks.

## Bounded media and provider outcomes

`MaxMediaBytes` defaults to 8 MiB, with an adapter ceiling of 100 MiB. This ceiling does not
assert provider support for 100 MiB in every format. Apply smaller format-specific product
limits. `MaxConcurrentUploads` defaults to two per client, with a maximum of sixteen.
Reuse clients and impose fleet-wide concurrency limits when using multiple processes.
Uploads sniff supported content types and spool bounded multipart content into private
temporary files instead of buffering entire payloads in RAM. Unknown types are rejected.
Multipart files carry the declared validated MIME type and an explicit request length.

Downloads validate HTTPS, outbound URL policy and Meta CDN hosts, then spool complete
bounded content before publishing to the caller's writer. Overflow uses a sentinel byte;
partial responses and declared-length mismatches fail. Both paths remove temporary files
and report cleanup failures. A destination writer failure may still leave partial output:
its owner must abort/delete that object. Custom reader owners must unblock a Read already
in progress on cancellation. Temporary disk capacity and download concurrency need
application limits. `GetMediaURL` rejects HTTP/provider failures and malformed/empty URLs.

Provider response evidence is bounded to 64 KiB plus a sentinel. `ProviderError` exposes
`permanent`, `transient` or `unknown` outcomes. Transport/malformed-success errors for
mutations and mutation HTTP 5xx responses are uncertain. Reconcile uncertain outcomes
before retrying a send. Transient classification alone does not prove replay is safe.
No automatic mutation retries occur. Retry hints support bounded seconds or HTTP dates;
raw provider error text and signed URLs are excluded from the error log string.

## Version and deployment verification

The implicit v21.0 fallback was removed. Callers must explicitly pin a version verified
against the [Meta Graph API version schedule](https://developers.facebook.com/docs/graph-api/changelog/versions/)
and current WhatsApp contracts before deployment. The official schedule could not be
retrieved during this change; no replacement default or current support claim is made.
Test fixture v21.0 pins only verify URL construction. Provider sandbox verification of
handshake, account permissions, approved templates, media formats and sends remains
required; mock tests do not establish live delivery.

## Finding treatment ledger

| ID | Shared treatment | Application or deployment boundary |
| --- | --- | --- |
| WA-01 | Additive durable acceptance constructor; failure returns 503 | Migrate legacy production routes |
| WA-02 | Explicit overflow detection before parsing | Upstream request deadlines |
| WA-03 | Verified account/phone mapping and stable deduplication keys | Transactional inbox, unique constraint, sender/user mapping |
| WA-04 | Constructor rejects empty ingress credentials; legacy fails closed | Validate ingress at startup |
| WA-05 | Per-variant and aggregate request validation | Product permissions and provider approvals |
| WA-06 | Unicode bounds, unique IDs, scoped signed tokens | Authorization and atomic token consumption |
| WA-07 | HTTP/provider failures and invalid URL responses rejected | Egress checks before downloads |
| WA-08 | Sentinel spool, partial-response rejection and cleanup | Abort destination on writer failure |
| WA-09 | Small default, disk spool and bounded upload concurrency | Fleet and format-specific limits |
| WA-10 | Video sends rejected; enum retained for compatibility | No video support advertised |
| WA-11 | Missing inbound fields modeled; bounded raw evidence preserved | Quarantine unsupported types and validate schemas |
| WA-12 | Structured codes, retry hints and uncertain outcomes | Reconcile before retrying mutations |
| WA-13 | Explicit pin mandatory; implicit default removed | Verify current version lifetime before release |
