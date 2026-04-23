# Standalone Apps Redis Practices

Status: baseline
Date: 2026-04-20

## Purpose

Redis is for ephemeral speed and coordination, not durable business truth.

## Non-negotiable rules

1. Do not store source-of-truth business records in Redis.
2. All Redis keys must have deterministic naming and TTL policy.
3. Code paths must degrade gracefully when Redis is unavailable.
4. Redis usage must be injectable and testable.
5. Auth/session or authorization-sensitive Redis data must fail closed. Redis must not become the only durable source for permission decisions that would fail open if keys disappear or are evicted.

## Allowed use cases

1. Short-lived caches.
2. Rate limiting counters.
3. Idempotency fences/locks.
4. Queue coordination and transient worker state.
5. Lightweight pub/sub notifications.
6. Transient session management (with explicit TTL and sliding window activity tracking where required).

## Key design

Format:

1. `<app>:<env>:<domain>:<entity>:<purpose>[:<id>]`

Examples:

1. `<app>:dev:media:asset:lock:asset_123`
2. `<app>:prod:billing:subscription:cache:sub_999`
3. `<app>:prod:publish:idempotency:cmd:abc123`

Rules:

1. Lowercase, colon-separated keys.
2. Include app and environment prefixes.
3. Include organization scope in value or suffix where relevant.
4. Authenticated cache keys must include actor/tenant/session or permission version where response shape depends on identity.

## TTL policy baseline

1. Rate limits: 60-300 seconds.
2. Idempotency fences: at least command retry horizon.
3. Caches: explicit TTL by data volatility (never indefinite by default).
4. Locks: short expiry with safe release semantics.
5. Sessions and auth-adjacent keys must rotate on login, privilege change, logout, and password-reset events where they are used.

## Runtime rules

1. Use pipelining for batch operations.
2. Avoid `KEYS` in runtime paths; use targeted keys or cursor scans for controlled maintenance only.
3. Bound retries and timeouts for Redis calls.
4. Record hit/miss/error metrics per key family.
5. Distributed locks must use unique lock tokens or fencing semantics so one actor cannot release another actor's lock after timeout or retry drift.
6. Do not cache authorization answers or user-scoped responses without an explicit invalidation/versioning strategy.

## Concurrency and scale controls

1. Keep Redis-backed rate limits windowed (`rate + period + burst`) rather than tiny per-second caps.
2. Separate key families for ingress rate limiting, idempotency, cache, and pub/sub.
3. Use distinct prefixes per app and environment to prevent cross-tenant blast radius.
4. For high throughput, pipeline multi-key operations and keep payloads compact.
5. Plan migration path to managed Redis HA/cluster before memory or CPU saturation.
6. Abuse-prone key families (login, OTP, password reset, uploads, invites, search) should combine actor and source signals such as IP/device/session where appropriate to make brute-force rotation harder.

## Safety and security

1. Redis must not be publicly exposed.
2. Use auth/ACLs and TLS where available.
3. Do not store sensitive PII unless encrypted and justified.
4. Never log raw secret-bearing values.
5. Do not place raw access tokens, password-reset tokens, invite tokens, signed URLs, or API keys in Redis keys or values. Use opaque digests or references.
6. Restrict dangerous commands and maintenance capabilities (`FLUSH*`, `CONFIG`, `KEYS`) through ACLs for runtime identities.

## Cost control

1. Cap key cardinality per feature.
2. Add feature flags for high-cardinality Redis features.
3. Track memory per key family and prune aggressively.

## Delivery checklist

1. Key naming documented.
2. TTL documented and tested.
3. Fallback behavior tested for Redis downtime.
4. Metrics and alerts in place for error rate and memory pressure.
5. Key families reviewed for tenant bleed, cache poisoning, and stale-session invalidation behavior.
