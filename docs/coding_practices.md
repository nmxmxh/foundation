# Ovasabi Coding Practices (Pragmatic Strict-Core)

Status: v2.6
Date: 2026-05-01
Owner: Platform Architecture

## Purpose and scope

This document defines enforceable coding practices for Ovasabi services and clients built with Go, TypeScript, PostgreSQL, Redis, WebSocket event contracts, and River workers.

It is intentionally strict on reliability-critical behavior and practical on delivery speed. Rules are designed to be checkable by tooling or code review.

Primary references used for synthesis:

- `/Users/okhai/Desktop/OVASABI STUDIOS/blueprint/JSF-AV-rules.pdf`
- `/Users/okhai/Desktop/OVASABI STUDIOS/blueprint/P10.pdf`

Related architecture blueprint:

- `/Users/okhai/Desktop/OVASABI STUDIOS/blueprint/standalone_apps_architecture_blueprint.md`

Related frontend practice docs:

- `/Users/okhai/Desktop/OVASABI STUDIOS/foundation/docs/styling_design_practices.md`
- `/Users/okhai/Desktop/OVASABI STUDIOS/foundation/docs/references/README.md`

## Rule levels

- `Mandatory`: required for merge unless a documented exception is approved.
- `Recommended`: strong default; deviations require rationale in PR notes.
- `Contextual`: apply when the condition is present (for example, hot paths, external integrations, safety-sensitive flows).

## Security posture assumptions

1. Assume deployment in a hostile environment with anonymous internet users, authenticated users, tenant adversaries, third-party API consumers/webhook callers, and limited insiders.
2. Treat browser state, route params, headers, cookies, websocket payloads, uploaded files, queue payloads, object-storage callbacks, and third-party responses as untrusted until validated.
3. Protect tokens, secrets, organization-scoped records, admin capabilities, signed URLs, audit trails, and billing/publish/approval flows as sensitive assets.
4. Review new externally reachable features for entry points, trust boundaries, and plausible chained exploits, not only single-issue failures.

## Rules (`CP-*`)

### CP-01: Keep control flow simple and explicit

Level: `Mandatory`

Requirements:

1. Do not use hidden or confusing control flow patterns in production paths.
2. `goto` is disallowed except tightly scoped cleanup exits where alternatives reduce clarity.
3. Recursion is disallowed in request handlers, workers, and critical business logic unless explicitly approved.

Enforcement:

- Code review check on changed files.
- Static lint checks for prohibited constructs where tool support exists.

### CP-02: Bound loops, retries, and time-consuming operations

Level: `Mandatory`

Requirements:

1. Loops over variable-size inputs must have explicit practical bounds or timeout guards.
2. Worker retries must use explicit `max_attempts` and backoff policy.
3. All external calls must use bounded context deadlines/timeouts.
4. Runtime list and summary endpoints must keep bounded defaults. Expanded report/export reads require explicit report-scoped metadata and a finite service-side cap; sentinel values such as `-1` must not mean unbounded in dashboard, bootstrap, or generic API contexts.

Enforcement:

- Review of worker/job options and timeout configuration.
- Integration tests for retry/failure termination behavior.

### CP-03: Control function size and decision complexity

Level: `Mandatory`

Requirements:

1. Keep function size within maintainable limits (target <= 80 logical lines; hard cap <= 120 unless justified).
2. Keep cyclomatic complexity reasonable (target <= 15; hard cap <= 20 unless justified).
3. Split orchestration from transformation logic when limits are exceeded.

Enforcement:

- Complexity tools in CI where available.
- Reviewer gate on large/complex functions.

### CP-04: Check return values and propagate errors intentionally

Level: `Mandatory`

Requirements:

1. Do not ignore return values from non-void/non-nil-producing calls unless explicitly intentional.
2. If ignored intentionally, annotate rationale in code or PR.
3. Error handling must preserve operational context (correlation/user/org where relevant).
4. Runtime parsing and extraction failures must return controlled errors rather than panic.
5. Error handling in multi-stage parsers and extractors must preserve stage/context so failures can be diagnosed without blind reproduction.
6. Error handling in batch processing and multi-stage pipelines must preserve record identifiers and processing stages. Diagnostic logs must pinpoint exactly which record in a batch failed and at which stage, such as `upload`, `decode`, `validate`, or `DB insert`.

Enforcement:

- Lint/static checks for unchecked errors.
- Reviewer gate for ignored returns and wrapped error context.
- Reviewer gate on panic-prone parser paths and missing stage/context preservation.

### CP-05: Use assertions/invariants at boundaries

Level: `Recommended`

Requirements:

1. Validate preconditions/postconditions on domain boundaries.
2. Assertions must be side-effect free.
3. Assertion failures in runtime paths must fail safely and return controlled errors/events.

Enforcement:

- Unit and integration tests for boundary invariants.
- Reviewer checks for side-effect-free assertion behavior.

### CP-06: Minimize mutable shared state and scope data tightly

Level: `Mandatory`

Requirements:

1. Declare data at the smallest practical scope.
2. Avoid unencapsulated global mutable state.
3. Shared state access must be synchronized or isolated by design.

Enforcement:

- Reviewer gate on package-level mutable variables.
- Concurrency-focused tests where shared state exists.

### CP-07: Apply allocation discipline in hot paths

Level: `Contextual`

Requirements:

1. Avoid unnecessary allocations in per-event/per-message hot paths.
2. Do not introduce allocation patterns that create unpredictable latency under load.
3. Prefer deterministic resource use in workers and realtime ingress paths.
4. Reusable parsing artifacts used in hot paths must be initialized once and reused.
5. Do not compile regexes inside per-record, per-line, or per-page loops when the pattern is static.
6. Prefer `strings.Builder`, `bytes.Buffer`, or pre-sized slices for repeated text assembly in loops; avoid repeated string concatenation in accumulation paths.
7. For large structured inputs, prefer bounded preview plus streaming iteration over full in-memory materialization when the downstream contract supports streaming.
8. Precompute static lookup structures such as normalized header maps, token sets, compiled boundary patterns, and semantic lookup tables when they are reused across many records.
9. Repeated normalization/parsing logic must be centralized; when identical raw values recur at scale, bounded caches may be used to avoid repeated work.
10. Optimize proven hot paths first; precompute static work and stream variable work rather than expanding the same discipline indiscriminately across the whole codebase.
11. Do not use `map[string]any` JSON envelopes on hot communication paths. Compatibility adapters may decode dynamic JSON at boundaries, but internal lanes must prefer typed protobuf, Cap'n Proto, raw byte frames, or shared-memory descriptors.
12. Performance-sensitive gRPC calls must use generated proto messages or `grpcsvc.Frame`; `grpcsvc.Envelope` is compatibility-only and must not become the default for new service-to-service traffic.
13. Foundation defaults assume every project can become performance-demanding. New communication APIs must provide a binary/typed path first and make JSON an explicit fallback.
14. Typed registry, WebSocket, HTTP, and gRPC paths must preserve payload bytes until the owning handler validates/decodes them; avoid intermediate map materialization for observability, routing, or convenience.
15. Same-process hot communication must not use gRPC, HTTP, Redis, or JSON. Use direct typed calls, direct frame dispatch, worker channels, or shared-memory descriptors so the hot path can remain zero-copy or near-zero allocation.
16. Serialization boundaries should expose both owned and borrowed decode APIs where safe. Borrowed views are preferred inside synchronous hot paths; owned decoded values are required when data escapes the frame lifetime.
17. Prefer batch database primitives such as `pgx.Batch`, `CopyFrom`, and equivalent driver-native bulk APIs for high-volume inserts.
18. Parallelize independent I/O-bound operations, such as object-storage uploads during batch ingestion, with bounded goroutines or the project chain helper. Preserve per-record diagnostics and cancellation semantics.
19. Initial dashboard and bootstrap summaries must request the smallest useful projection: explicit compact/light metadata, bounded recent items, and expensive sections disabled unless the first viewport actually renders them.
20. Frontend cache keys for summary/list hot paths must be semantic and stable. Include filters that change the response; exclude volatile metadata such as correlation IDs, timestamps, trace IDs, and retry markers.
21. Do not log full summary/list payloads in store setters, reducers, or render-adjacent code. Hot UI paths may log compact counters/keys only behind a development guard.

Enforcement:

- Reviewer gate on regex compilation inside hot loops, repeated parsing of identical values, and unnecessary full-buffer reads.
- Benchmark/profile or representative fixture evidence for parser, ingestion, worker, and realtime hot path changes.
- Load test regression gate for queue and WS critical paths.
- Dev-only performance guard: `foundation/tooling/scripts/performance_check.sh`.

### CP-08: Zero-warning mindset and static analysis in CI

Level: `Mandatory`

Requirements:

1. Compile with strict warning settings and keep warnings at zero for supported toolchains.
2. Run static analysis/lint checks in CI on every PR.
3. Treat analyzer confusion as code clarity debt; simplify code when needed.

Enforcement:

- CI gates for `go test`, `golangci-lint`, Rust `fmt`/`clippy`, frontend ESLint, TypeScript checks, and scaffolded `scripts/checks/*`.
- Foundation runtime, transport, server-kit, and SDK lanes use the strictest CP automation because resource leaks, compatibility envelopes, or dynamic JSON materialization become platform-wide costs.
- Project and frontend lanes inherit the same boundary checks, but React complexity and app-composition rules may start as ESLint warnings when strictness would create migration noise rather than better resource behavior.
- Managed `.foundation` projects must pass `server_kit_usage_check.sh`, which verifies that generated backend startup/server/worker paths actually bind server-kit runtime surfaces instead of merely carrying vendored packages.

### CP-09: Restrict unsafe and reflection-heavy patterns

Level: `Mandatory`

Requirements:

1. `unsafe` usage is prohibited unless absolutely required and ADR-approved.
2. Reflection-heavy logic in core domain paths requires justification and tests.
3. Dynamic behavior must not obscure call/control flow in critical logic.

Enforcement:

- Search-based CI checks for `unsafe`.
- Reviewer + ADR gate for exceptional use.

### CP-10: Keep event contracts deterministic and idempotent

Level: `Mandatory`

Requirements:

1. Mutating command flows must preserve stable request identity (`correlation_id` and idempotency keys where required).
2. Emitted events must keep envelope contract fields complete and versioned when semantics break.
3. Worker side effects must be idempotent under retries/duplicates.

Enforcement:

- Contract tests (integration/e2e).
- Schema/event validation checks in CI.

### CP-11: Code for testability-first behavior

Level: `Mandatory`

Requirements:

1. New behavior must include unit tests for logic and failure paths.
2. Critical flows must include integration coverage.
3. User journey and guard behavior must be covered in e2e where applicable.
4. Performance optimizations in correctness-sensitive code must keep or add regression tests for known edge cases and large representative fixtures.
5. Centralizing parsers, adding caches, or changing fallback logic requires tests for false positives, stale reuse, and behavioral drift.
6. Treat correctness regressions from cleanup refactors as a normal risk and test for them explicitly.
7. Hot-path optimizations are not complete until both correctness and performance-sensitive regression suites pass.
8. When mocking complex interfaces such as `pgx.Batch`, use a wrapper pattern like `BatchableMock` in tests. Simulate batch results without adding `isMock` flags, type switches, or test-only branching to production code.

Enforcement:

- PR test evidence requirements.
- CI execution of required test slices by change class.

### CP-11A: Use cleanup and unlock patterns deliberately

Level: `Mandatory`

Requirements:

1. In Go, use `defer` immediately after acquiring resources that must be released on every exit path: `cancel`, `Close`, `Unlock`, `RUnlock`, `wg.Done`, `span.End`, timer/ticker stop, and temporary file cleanup.
2. In hot loops, replace repeated `defer` with explicit cleanup only when profiling or allocation/latency evidence shows the deferred calls matter. Keep the explicit cleanup mechanically simple and covered by tests.
3. Do not hold locks, DB transactions, or file descriptors across network calls, unbounded waits, or callbacks into user-controlled logic.
4. In Rust, prefer RAII guards for symmetric cleanup such as counters, locks, temporary state, and FFI handles. Avoid duplicated decrement/release branches that can drift during later edits.

Enforcement:

- Reviewer gate for missing `defer`/RAII cleanup after resource acquisition.
- Reviewer gate for explicit cleanup in loops without a hot-path rationale.

### CP-12: Keep documentation and traceability current

Level: `Recommended`

Requirements:

1. When contracts/routes/guards change, update test traceability docs (for example e2e matrix).
2. Keep architecture and testing docs aligned with actual repo structure.
3. Capture notable rule exceptions in ADRs.

Enforcement:

- Reviewer gate for doc updates on contract-sensitive changes.
- Architecture review checklist.

### CP-13: Prefer styled-component architecture and shared UI primitives

Level: `Mandatory`

Requirements:

1. UI styling should be componentized through shared primitives, not repeated page-local inline styles.
2. Reusable component surfaces (buttons, alerts, segmented controls, modal layouts, form rows) should wrap `foundation/ui-minimal` `Minimal*` primitives from app-local `components/ui`; app-local components own brand defaults, not structural reimplementation.
3. Theme and motion tokens must be consumed via shared primitives before introducing per-page style overrides.
4. New styled-component modules should group declarations in a single object: `const Style = { Container: styled.div... }`. This is the preferred Ovasabi review format for application and feature code.
5. Do not carry forward large inline style objects from legacy components into new shared primitives or product surfaces. Inline style usage is reserved for runtime coordinates, CSS custom-property injection, or motion-library transform values that are impractical to express in styled components.
6. Separate styling, motion, and async-state concerns. Theme tokens belong in theme modules, loading boundaries belong in dedicated loader/skeleton components or route wrappers, and business components should compose them rather than owning every concern directly.
7. New animation work must follow `styling_design_practices.md` and the animation reference notes under `docs/references/`.
8. `ui-minimal` must be consumed as `@ovasabi/ui-minimal` through the local file package dependency. Do not alias raw source under `foundation/ui-minimal/ts/src`; keep `preserveSymlinks` enabled in frontend Vite, Vitest, and TypeScript config to avoid duplicate peer graphs.
9. IndexedDB persistence, metadata normalization, store reset handles, and runtime/WASM external stores should use `@ovasabi/frontend-kit` before introducing app-local infrastructure.

Enforcement:

- Reviewer gate on high-volume inline style additions.
- Frontend lint/review checklist for shared primitive reuse, grouped `Style` declarations, and loader/skeleton separation.

### CP-14: Form state should default to a single object model

Level: `Recommended`

Requirements:

1. Forms with multiple fields should use a single object state plus one named update function (for example `updateFormState`).
2. Prefer shallow spread updates for flat form models.
3. For nested form structures, use path-based update helpers to avoid repetitive state setters.
4. Keep visual busy state explicit. `isSubmitting`, keyed loading flags, and validation state should not be hidden inside unrelated field setters or derived from ad-hoc DOM inspection.

Enforcement:

- Reviewer check on new form implementations.
- Unit/UI behavior tests for form updates and validation paths.

### CP-15: Use lodash intentionally to reduce code bloat (not hide logic)

Level: `Recommended`

Requirements:

1. Use lodash helpers (`set`, `get`, `pick`, `omit`, `debounce`, `throttle`, `groupBy`, `keyBy`) when they reduce repeated boilerplate.
2. Avoid chaining patterns that make business intent unclear.
3. Keep lodash usage centralized in utilities for repeated patterns (state updates, normalization, grouping).

Enforcement:

- Reviewer check on readability and maintainability.
- Bundle-size and performance review for frontend utility additions.

### CP-16: Prefer adaptive concurrency over fixed internal request pacing

Level: `Mandatory`

Requirements:

1. Internal service dispatch must use bounded concurrency and timeout-based backpressure, not hardcoded tiny per-second caps.
2. All concurrency and queue worker limits must be environment-driven configuration.
3. Rate limiting is allowed at ingress or abuse-prone edges, with explicit `rate + period + burst`.
4. Saturation behavior must emit measurable signals (timeouts, queue depth, retries, rejects).
5. Acquire-timeout saturation must surface an explicit concurrency-limit error instead of silently collapsing into a generic deadline path.
6. Listener and dispatch loops must prefer blocking fan-in worker pools over sleep-based busy polling.

Enforcement:

- Review gate on hardcoded throttle values in runtime code.
- Benchmark/load evidence for hot path changes.
- Config and runbook updates in the same PR when limits change.

### CP-17: Frontend realtime architecture must stay contract-first and minimal

Level: `Mandatory`

Requirements:

1. Frontend route/auth behavior must preserve guest-to-user upgrade flow on the same websocket connection where applicable.
2. Shared `Minimal*` UI primitives (including header/table/calendar baselines) must be used before page-local UI reinvention.
3. Generated contracts (`proto-ts` and route metadata/docgen output) must be the source of truth for command routing and RBAC UI gating.
4. Frontend utility additions (`lodash`, motion helpers, style primitives) must reduce repeated code and include typecheck/build evidence.
5. New or refactored frontend domain types must import from `frontend/src/types/protos` when a matching protobuf exists. Hand-written files under `frontend/src/types` are limited to UI-only helper types and adapters around generated contracts.

Enforcement:

- Frontend architecture review against `/Users/okhai/Desktop/OVASABI STUDIOS/blueprint/frontend_optimization_practices.md`.
- CI typecheck/build and contract-drift checks when route/proto surfaces change.
- Reviewer gate on handwritten API contract types when `api/protos` already contains the schema.

### CP-18: Ingress Edge Security, Abuse Resistance, and Origin Controls

Level: `Mandatory`

Requirements:

1. **Rate Limiting**: All ingress API routes must use explicit rate controls (`rate + period + burst`) to prevent spam/abuse vectors and unbound hosting bill inflations. Auth, password-reset, OTP, upload, search, and webhook endpoints require tighter per-actor and per-source budgets than generic read APIs.
2. **CORS Policy**: Explicit whitelisting is required. `Access-Control-Allow-Origin: *` is disallowed for authenticated pathways, and credentialed routes must use exact origin matching rather than broad regex or suffix shortcuts.
3. **Origin Checks**: Cookie-authenticated mutation routes and websocket upgrades must validate `Origin` (and forwarded host where relevant) to reduce CSRF and cross-site socket abuse.
4. **Webhook Verification**: Incoming webhooks (for example, Stripe, Twilio) must verify signature and freshness, enforce body-size limits, dedupe provider event IDs, and hand off slow work asynchronously.
5. **Debug Surface Control**: Debug, profiling, and admin-only endpoints must be disabled or separately gated outside local development.

Enforcement:

- Gateway/Middleware configuration audit.
- Integration tests for origin rejection, abuse budgets, and webhook verification failure paths.
- PR reviewer checks on new integration handlers.

### CP-19: Frontend Token & Secret Lifecycle Safety

Level: `Mandatory`

Requirements:

1. **Frontend secrets**: Do not expose or hardcode private API keys in the frontend bundle. Use backend proxies for secure external call coordination.
2. **Token Storage**: Auth tokens must prioritize Secure, `HttpOnly`, and `SameSite` cookie delivery paths over `localStorage`/`sessionStorage`. Storage exceptions require explicit rationale because they widen XSS impact.
3. **Leak Prevention**: Never place bearer tokens, session IDs, signed URLs, password-reset tokens, invite tokens, or API secrets in query strings, analytics payloads, referer-bearing links, or client logs/crash reports.
4. **Expiry policy**: Sensitive one-time tokens (for example, password reset, invites, email verification) must use short-lived TTLs (15–60 mins), enforce single-use consumption, and be stored server-side as digests or encrypted values rather than raw bearer material where lookup by digest suffices.
5. **Rotation and Revocation**: Rotate session identifiers on login, privilege elevation, password change, and suspected compromise, and support server-side revocation/logout across devices where product risk warrants it.

Enforcement:

- Reviewer checks on cookie config, Storage usage, and client-side leak paths.
- Tests for token expiry, single-use, rotation, and revocation behavior.

### CP-20: Defence in Depth Validation, Authorization, and State Safety

Level: `Mandatory`

Requirements:

1. **Input Validation**: All untrusted input must be validated and normalized server-side with explicit allowlists for shape, length, enum values, and character class where applicable. Reject ambiguous or overlong values early.
2. **Safe Rendering**: Render untrusted content through safe templating/escaping APIs. `dangerouslySetInnerHTML` or equivalent raw HTML sinks require approved sanitization allowlists and tests.
3. **Server-side Authorization**: Sensitive routes must enforce authorization on both the action and the target object (BOLA/IDOR protection). Never trust client-supplied org, user, or resource identifiers without re-deriving scope from the authenticated principal.
4. **Mass-assignment Protection**: Mutation handlers must allowlist writable fields and reject or ignore ownership, role, billing, or system-managed attributes from clients.
5. **State-transition Safety**: High-risk transitions (payments, refunds, approvals, invitations, role changes, publish/unpublish, file promotion) must re-check current state and actor authority inside the same transaction/lock boundary to resist race conditions and double-submit paths.
6. **Interpreter and Egress Safety**: Outbound fetchers, template engines, shell/CLI calls, and dynamic interpreters must use allowlists, sandboxing, or disabled-by-default posture to prevent SSRF, command injection, template injection, and arbitrary file access.

Enforcement:

- Backend test validation for route access controls, object-level authorization, and mass-assignment rejection.
- Reviewer checks on form render boundaries, outbound call sites, and state-transition guards.

### CP-21: Frontend Resilience and Error Isolation

Level: `Mandatory`

Requirements:

1. Use React Error Boundaries at route, page, and feature container depths to isolate rendering failures and prevent full white-screen lockouts.
2. Always present helpful fallback views allowing the user to retry or return home.

Enforcement:

- Review gate on route/page component wrappers.

### CP-22: Operational Monitoring & Startup Safety

Level: `Recommended`

Requirements:

1. **Startup Validation**: System startup must validate all required environment variables. Fail fast (panic/exit code 1) with descriptive audit trails for missing configs.
2. **Health endpoint**: Every API service must expose a `/health` or `/status` endpoint returning system vitality for load balancers and upstream orchestrators.
3. **Structured Logging**: Use structured JSON logging in production. Use correct severity levels; capture stack traces and correlation IDs for error tracking.
4. Fallback selection, extraction failure, and degraded parsing paths must emit structured logs with explicit reason codes.
5. Security-significant events (for example, login-failure bursts, privilege changes, token resets, webhook signature failures, and rate-limit trips) must emit structured logs or audit records without raw secrets.

Enforcement:

- CI validation on config binding setup.
- Container/Cluster manifest verification for health check integration.
- Reviewer gate on missing reason-coded logs for degraded runtime/parser paths.

### CP-23: Safe Asset Management and Storage

Level: `Mandatory`

Requirements:

1. Do not store uploaded images or assets directly on the API server local file system.
2. Use remote object-storage buckets (for example, GCS, AWS S3) that are private by default, with public access only through CDN policy or signed URLs with short TTLs and scoped permissions.
3. File uploads must enforce allowed MIME/extension combinations, size/count limits, filename randomization, content sniffing, and quarantine/scanning for high-risk types before serving or downstream processing.
4. Never trust user-supplied file names, path fragments, content types, or image metadata for storage keys, authorization, or processing decisions.

Enforcement:

- Review on file handler implementation.
- Upload tests for type, size, and path-traversal rejection.

### CP-24: Offload Slow Context Operations to Background Workers

Level: `Mandatory`

Requirements:

1. Critical request path handlers must not wait on slow external services (for example, SMTP servers, external lookups).
2. Offload slower I/O bounded side-effects to asynchronous queue workers (for example, River workers) to prevent handler thread starvation and app hangs.

Enforcement:

- Reviewer assessment of synchronous handler side-effects.

### CP-25: Frontend request replay, dedupe, and loading state must be scoped

Level: `Mandatory`

Requirements:

1. Replay/cache defaults must apply only to replay-safe read requests; auth and mutation flows require explicit opt-in if they want reuse or inflight dedupe.
2. Replay and inflight keys must include current runtime identity context (for example session/user/org) so responses never bleed across auth transitions or organization switches.
3. Concurrent UI actions must use reference-counted loading keys or equivalent scoped loading state, not a single boolean that can be cleared by the wrong request.
4. Mutation flows that intentionally coalesce in flight must declare that intent explicitly in the command helper/store surface.

Enforcement:

- Unit tests for context separation, replay safety, and mutation opt-in behavior.
- Reviewer gate on broad replay/dedupe applied to auth or mutating commands.

### CP-26: Frontend boot, runtime singleton, and stale-build recovery must be deliberate

Level: `Mandatory`

Requirements:

1. Landing/public routes must lazy-load heavy authenticated shells, runtime bridges, and dashboard-only stores unless there is a measured reason not to.
2. Asset warmup and prefetch logic must be route-family aware and must respect offline and save-data signals.
3. Runtime bootstrap state that must survive HMR, code splitting, or shell remounts must live in a stable process-level singleton (`window`, `globalThis`, or equivalent module singleton).
4. Dynamic-import and chunk-load failures must trigger a guarded stale-build recovery path (cache/service-worker refresh plus reload) instead of leaving the app stuck.

Enforcement:

- Bundle/build review for new public-route dependencies.
- Smoke or integration coverage for stale-build recovery and runtime bootstrap behavior.

### CP-27: Browser boundary, headers, and cache control must be explicit

Level: `Mandatory`

Requirements:

1. Set CSP, `frame-ancestors`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Permissions-Policy`, and `Strict-Transport-Security` in TLS-backed environments; any exception must be documented.
2. `SharedArrayBuffer` deployments must intentionally combine `Cross-Origin-Opener-Policy`, `Cross-Origin-Embedder-Policy`, and compatible asset policies (`CORP`/CORS) so cross-origin isolation does not silently fail or widen exposure.
3. Authenticated or sensitive responses must default to `Cache-Control: no-store` or tightly scoped private caching. Shared CDN caches must never key only on path for user/org-specific data.
4. Websocket connections must validate origin, authenticate explicitly, bind to current session context, and re-authorize privileged subscriptions/actions after connect.
5. Disable compression on responses that reflect attacker-controlled input alongside secrets or one-time tokens when compression side channels become plausible.

Enforcement:

- Deployment/security-header review.
- Integration tests for cache headers, cross-origin isolation, and websocket origin/auth behavior.

### CP-28: Dependency, third-party integration, and secret supply chain hygiene

Level: `Mandatory`

Requirements:

1. Commit lockfiles and use reproducible installs for production builds. New dependencies require review of maintenance posture, transitive risk, and install/build scripts.
2. CI must run dependency vulnerability scanning and flag critical/high findings before merge or release unless a documented exception exists.
3. Third-party integrations must use least-privilege credentials, explicit outbound timeouts, retry limits, and signature or response validation where supported.
4. Secrets must come from environment or secret managers, never source control, demo seeds, screenshots, or client bundles. Suspected leaks require rotation, not just deletion.
5. Non-local environments must disable sample credentials, debug panels, verbose stack-trace pages, and unsafe developer toggles by default.

Enforcement:

- Dependency and lockfile diff review.
- CI vulnerability scan / secret scan gates where available.
- Config review for new external integrations.

### CP-29: Adversarial threat modeling and attack-chain review are required for exposed features

Level: `Mandatory`

Requirements:

1. New externally reachable or privilege-sensitive capabilities must document attacker profiles, entry points, trust boundaries, and sensitive assets in the PR, design note, or ADR.
2. Reviews must consider chained exploits combining lower-severity issues (for example, XSS -> token theft -> websocket reuse -> BOLA, or weak upload validation -> CDN cache poisoning -> stored execution).
3. High-risk changes must include negative tests for replay, race/double submit, privilege escalation, tenant bleed, stale cache reuse, and impossible-state transitions where applicable.
4. If context is incomplete, record the assumption and compensating control instead of assuming safety.

Enforcement:

- PR template/security checklist completion for exposed features.
- Reviewer gate on missing adversarial tests or undocumented trust-boundary changes.

### CP-30: Use coverage plus complexity to prioritize change risk

Level: `Mandatory`

Requirements:

1. Coverage alone is not enough; changed methods/functions with meaningful branching must be reviewed as hotspot candidates using complexity plus coverage together.
2. New code should target line coverage >= 80%, branch coverage >= 60%, and CRAP-style hotspot scores < 30 where the stack/tooling can calculate them.
3. Changed methods with projected hotspot scores > 30, or with high complexity and near-zero coverage, must gain tests or lose complexity before feature work continues unless an exception is documented.
4. Coverage collection must exclude test projects, benchmarks, migrations, generated files, and similarly non-production artifacts so hotspot signals remain useful.
5. Legacy code can phase in lower thresholds, but touching high-risk methods must improve either coverage or complexity; do not leave both risk factors untouched.
6. CI should publish machine-readable coverage output and a human-readable hotspot summary for changed modules where the app stack supports it.

Enforcement:

- CI coverage and hotspot reports where tooling exists.
- Reviewer gate on changed hotspots above threshold without mitigation or documented exception.

### CP-31: MutationObserver is exception-only architecture

Level: `Mandatory`

Requirements:

1. Do not use `MutationObserver` as a general app-state, auth-state, routing, or data-synchronization mechanism.
2. Prefer explicit React/store/event flows first. Use `ResizeObserver` for size measurement and `IntersectionObserver` for visibility before considering `MutationObserver`.
3. `MutationObserver` is allowed only for narrow UI adapters such as third-party widgets, `contenteditable` islands, or Shadow DOM integrations where declarative APIs are insufficient.
4. Approved observers must target the smallest practical subtree, use the narrowest observe options, disconnect on cleanup, and batch expensive follow-up work with `requestAnimationFrame`, microtasks, or debounce/throttle as appropriate.
5. Observer callbacks must not trigger unbounded dispatch loops, websocket emissions, or permission/session decisions from raw DOM mutations.

Enforcement:

- ESLint restriction on direct `MutationObserver` construction with explicit local waiver requirement.
- `scripts/checks/coding_practices_check.sh` blocks direct observer construction in generated project gates so the exception policy is not review-only.
- Reviewer gate on observer scope, cleanup, and feedback-loop risk.

### CP-32: Runtime communication must use foundation transport contracts

Level: `Mandatory`

Requirements:

1. App code must dispatch browser/backend/runtime messages through `runtime-transport` or `runtime-sdk` host APIs, not ad hoc websocket globals or raw JSON bridges.
2. The 4KB runtime buffer is the control plane only. Large payloads must use transferable buffers, binary envelopes, or the optional `RuntimeSharedArena`.
3. Main-thread code must not call blocking `Atomics.wait`; workers own blocking waits and main-thread code uses `Atomics.waitAsync` or message fallback when needed.
4. `SharedArrayBuffer` execution requires COOP, COEP, and compatible CORP/CORS headers in local, preview, and production serving.
5. Compression settings must be negotiated by transport capability and must retain identity fallback.

Enforcement:

- Scaffold checks for runtime arena schema, COOP/COEP headers, Vite header config, and generated transport/package boundaries.
- CP automation blocks blocking `Atomics.wait`, oversized runtime-control-buffer changes, raw foundation source imports, raw transport globals, and foundation hot-path dynamic JSON envelopes.
- Unit tests for shared-memory fallback, large payload arena movement, and binary frame compression.

### CP-33: Post-quantum readiness must be crypto-agile and hot-path safe

Level: `Mandatory`

Requirements:

1. New cryptography decisions must document algorithm, key lifetime, migration path, and whether the data is long-lived enough to require post-quantum planning.
2. Prefer platform TLS hybrid KEM support and edge termination policy over app-level per-request post-quantum operations.
3. Use standardized post-quantum algorithms only; do not add experimental PQC packages without an ADR and benchmarks.
4. Keep post-quantum signing for release artifacts, durable records, or compliance-driven workflows unless a threat model proves request-path need.
5. Public config must not expose secrets or private key material when advertising runtime security capabilities.

Enforcement:

- Config-contract validation for `security.postQuantum`.
- Reviewer gate on new crypto code without inventory, migration, and performance notes.

### CP-34: Observability, SLOs, and fault tests are foundation requirements

Level: `Mandatory`

Requirements:

1. New externally reachable handlers and workers must record low-cardinality counters, latency histograms, and queue/depth gauges where meaningful.
2. Services with production traffic must define SLO thresholds for dispatch p99 latency, worker success rate, and event delivery lag.
3. New queue, Redis, database, or runtime integration paths must include at least one negative-path or chaos/fault-injection test.
4. Runtime payload movement changes must include unit tests and benchmark coverage for control, arena, and streaming lanes.
5. Profiling endpoints must be disabled by default and protected by admin capability or equivalent authorization when enabled.
6. gRPC service-to-service lanes must enforce auth metadata, message-size limits, deadline propagation, and bufconn contract tests.
7. Parallel chains must distinguish critical and non-critical failures; critical failures must cancel dependent work through context cancellation.
8. gRPC hot lanes must include allocation budget tests under the `perf` build tag when they introduce or alter serialization codecs.
9. gRPC allocation budgets are boundary budgets, not hot-path budgets. Same-process frame dispatch must have a zero-allocation or explicitly justified near-zero-allocation benchmark.
10. Frame codec changes must benchmark owned decode, append-buffer decode, borrowed view decode, generated protobuf `MarshalAppend`, and RPC boundary cost separately.

Enforcement:

- `server-kit/go/metrics`, `slo`, `chaos`, `contracttest`, and `profiling` unit coverage.
- Scaffold checks for runtime streaming/arena APIs, performance guard tooling, and config-contract SLO support.

### CP-35: River / Background Job Reliability and Scaling

Level: `Mandatory`

Requirements:

1. **Idempotency Deduplication**: In-memory idempotency maps must use TTL-based expiry or a bounded LRU to avoid unbounded memory growth. Default success retention should be 24 hours unless otherwise specified.
2. **Retry Context and Shutdown**: Background retries and re-enqueuing must respect the parent context cancellation/shutdown signals. Do not use `context.Background()` in retry loops that fire during process draining.
3. **Backoff Jitter**: All retry backoff calculations must include ±25% jitter to prevent thundering herd effects on downstream services and databases.
4. **Metadata Sidecar Architecture**: Large binary payloads or extensive tracking metadata should be stored in a dedicated metadata sidecar table (e.g., `river_job_metadata` with a `bytea` column) rather than being stuffed into River's `args` JSONB column. Use FK cascades for automatic cleanup.
5. **Postgres Pool Integration**: Metadata stores and job persistence logic must use `*pgxpool.Pool` directly for performance and connection lifecycle management, rather than generic/wrapped database interfaces that may obscure driver-specific optimizations.
6. **Idempotent Migrations**: SQL setup scripts for queue infrastructure must be idempotent. Avoid destructive `DROP TABLE` statements at the top of migrations that might fire against non-empty production environments; use `CREATE TABLE IF NOT EXISTS` and separate reset scripts.
7. **Production-Representative Benchmarks**: Performance-critical workers must include benchmarks that hit the River/Postgres path (using `testcontainers-go`) to capture serialization, indexing, and fsync costs, not just the in-memory fallback path.

Enforcement:

- Reviewer gate on worker retry logic, backoff jitter, and metadata storage patterns.
- Automated migration checks for destructive drops.
- CI benchmark evidence for hot-path workers.

## Enforcement matrix

| Rule ID | Primary enforcement | Automation | Merge gate |
| --- | --- | --- | --- |
| `CP-01` | Review + lint | Partial | Yes |
| `CP-02` | Review + integration tests | Partial | Yes |
| `CP-03` | Complexity tools + review | Partial | Yes |
| `CP-04` | Lint/static checks | Strong | Yes |
| `CP-05` | Unit/integration tests | Partial | No (unless safety-critical path) |
| `CP-06` | Review + concurrency tests | Partial | Yes |
| `CP-07` | Bench/load checks | Contextual | Contextual |
| `CP-08` | CI static/lint/compile checks | Strong | Yes |
| `CP-09` | Search + ADR + review | Partial | Yes |
| `CP-10` | Contract/integration/e2e tests | Strong | Yes |
| `CP-11` | CI tests + review evidence | Strong | Yes |
| `CP-12` | Review checklist | Partial | No |
| `CP-13` | Frontend review + component reuse check | Partial | Yes |
| `CP-14` | Review + UI tests | Partial | No |
| `CP-15` | Review + bundle/perf checks | Partial | No |
| `CP-16` | Review + benchmark/load evidence | Partial | Yes |
| `CP-17` | Frontend architecture + contract drift review | Partial | Yes |
| `CP-18` | Gateway/Middleware review | Partial | Yes |
| `CP-19` | Review + Static analysis | Partial | Yes |
| `CP-20` | Backend Auth Tests + Review | Partial | Yes |
| `CP-21` | Review checklist | Partial | Yes |
| `CP-22` | CI validation + container tests | Partial | No |
| `CP-23` | Review on handlers | Partial | Yes |
| `CP-24` | Review of handlers | Partial | Yes |
| `CP-25` | Unit tests + review | Strong | Yes |
| `CP-26` | Bundle review + smoke tests | Partial | Yes |
| `CP-27` | Security-header/deploy review + integration tests | Partial | Yes |
| `CP-28` | Dependency/config review + CI scans | Partial | Yes |
| `CP-29` | PR security checklist + adversarial tests | Partial | Yes |
| `CP-30` | CI coverage/hotspot reports + review | Partial | Yes |
| `CP-31` | ESLint restriction + architecture review | Partial | Yes |
| `CP-32` | Scaffold checks + runtime tests | Partial | Yes |
| `CP-33` | Config validation + crypto review | Partial | Yes |
| `CP-34` | Metrics/SLO/chaos/runtime benchmark tests | Partial | Yes |
| `CP-35` | Review + automated migration check | Partial | Yes |

## Exception process and ADR linkage

Exceptions are allowed only when both reliability and delivery impact are evaluated.

Required process:

1. Record exception intent in PR using the relevant `CP-*` rule IDs.
2. Describe risk, compensating controls, and rollback strategy.
3. Create/update ADR using `/Users/okhai/Desktop/OVASABI STUDIOS/blueprint/architecture_decision_log_template.md` for any persistent exception.
4. Add expiry/review date for temporary exceptions.

Approval:

1. Mandatory-rule exceptions require architecture owner approval.
2. Contextual-rule exceptions require service owner approval.

## Traceability: Source rule to Ovasabi adaptation

| Source rule | Ovasabi adaptation | Enforcement method |
| --- | --- | --- |
| Power of Ten Rule 1 (simple control flow, no goto/recursion) | `CP-01` control flow simplicity with narrow cleanup exception | Review + lint |
| Power of Ten Rule 2 (bounded loops) | `CP-02` bounded loops/retries/timeouts | Review + integration tests |
| Power of Ten Rule 3 (no dynamic allocation after init) + JSF AV 206 | `CP-07` allocation discipline for hot paths in Go/TS runtime context | Bench/load + review |
| Power of Ten Rule 4 (small functions) + JSF AV 1 | `CP-03` function size budgets | Complexity/report + review |
| Power of Ten Rule 5 (assertion use) | `CP-05` boundary assertions/invariants | Unit/integration tests |
| Power of Ten Rule 6 (small scope) + JSF coupling/cohesion guidance | `CP-06` minimal mutable shared state and tight scope | Review + tests |
| Power of Ten Rule 7 (check returns) + JSF AV 115 | `CP-04` explicit return/error handling | Lint/static checks |
| Power of Ten Rule 8 (restrict preprocessor complexity) | `CP-09` avoid dynamic/obscure flow patterns (`unsafe`/reflection overuse) | Review + search checks |
| Power of Ten Rule 9 (pointer restriction) + JSF AV 215 pointer arithmetic caution | `CP-09` unsafe/pointer discipline and ADR-gated exceptions | Review + search checks |
| Power of Ten Rule 10 (warnings + static analysis) + JSF AV 218 | `CP-08` zero-warning static-analysis CI baseline | CI merge gates |
| JSF AV 3 (cyclomatic complexity <= 20) | `CP-03` target <= 15, hard cap <= 20 with justification | Complexity tools + review |
| JSF testing guidance (base/derived invariants, structural coverage) | `CP-11` testability-first coverage policy per change class | CI + reviewer evidence |

## Operating note

Rules in this document are normative for new standalone app development and for major refactors in existing services. Teams should prefer verifiable rules over style-only preferences to keep standards enforceable.
