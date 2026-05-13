# Standalone Apps Redis Practices

Status: baseline
Date: 2026-04-20

## Purpose

Redis is for ephemeral speed and coordination, not durable business truth.

In this architecture the default read order is local memory cache first, Redis second, Postgres/read model third. Redis should remove repeated network/DB work from hot paths; it should not decide durable state transitions alone.

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
4. **Reliable Event Streaming**: Use Redis Streams (`XADD`, `XREADGROUP`) for business-critical events requiring "at-least-once" delivery.
5. **Distributed Coordination**: Use Fenced Distributed Locks for cross-process resource protection.
6. Lightweight pub/sub notifications (transient only).
7. Transient session management.

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

1. Use pipelining for batch operations to reduce network roundtrips.
2. Avoid `KEYS` in runtime paths; use targeted keys or cursor scans.
3. Bound retries and timeouts for Redis calls.
4. Record hit/miss/error metrics per key family.
5. **Fencing Tokens**: Distributed locks must use unique lock tokens (fencing tokens) or CAS semantics (`SET NX`). Locks must be released using a script that verifies the token to prevent accidental release of a lock re-acquired by another process.
6. **Consumer Groups**: Use Redis Stream consumer groups to distribute event load across multiple worker instances while ensuring every message is acknowledged (`XACK`).
7. **Big Key Prevention**: Do not store more than 10,000 elements in a single Hash, Set, or List. Large keys cause high latency during access and deletion (blocking the main thread) and lead to memory fragmentation.
8. **Scalable Counting**: Use **HyperLogLog** (`PFADD`, `PFCOUNT`) for estimating the cardinality of unique items (e.g., daily unique users) when a ~1% error margin is acceptable. This maintains a constant 12KB memory footprint regardless of scale.
9. **No O(N) Commands**: Strictly avoid `KEYS *`, `SMEMBERS` on large sets, or `HGETALL` on large hashes. Use `SCAN`, `SSCAN`, and `HSCAN` instead to prevent blocking the Redis event loop.
10. Group repeated cache writes/invalidations into bounded pipeline batches. Large pipelines must be chunked so queued replies do not become the memory problem.
11. Use `allkeys-lfu` for generic cache nodes unless the workload demands TTL-only eviction. LFU adapts better to mixed hot/cold cache traffic than pure recency.
12. Keep Redis timeouts shorter than the app lane budget. A failed cache read should fall through quickly; a Redis stall must not consume a full API request budget.
13. The Foundation memory Redis driver is a contract test double, not a production store. It should preserve Redis-like semantics for copied values, TTLs, locks, streams, and pub/sub patterns so tests catch drift, but production throughput claims still require service-backed Redis benchmarks.
14. Redis clients should be initialized through `ConnectWithOptions` so pool size, min-idle, retry, shard URL, dial, read, and write budgets are inherited from config instead of being silently discarded.
15. Use `redis.BatchClient` for multi-key cache hydration and write-through paths. `SetMany`, `GetMany`, and `SetGetMany` keep app code on Foundation boundaries while still using Redis pipelining underneath.
16. Do not benchmark a busy loop of sequential single-key calls as the target shape. Measure it as a baseline, then compare parallel, pipelined, and batch-per-key costs.

## Concurrency and scale controls

1. Keep Redis-backed rate limits windowed (`rate + period + burst`) rather than tiny per-second caps.
2. Separate key families for ingress rate limiting, idempotency, cache, and pub/sub.
3. Use distinct prefixes per app and environment to prevent cross-tenant blast radius.
4. For high throughput, pipeline multi-key operations and keep payloads compact.
5. Plan migration path to managed Redis HA/cluster before memory or CPU saturation.
6. Abuse-prone key families (login, OTP, password reset, uploads, invites, search) should combine actor and source signals such as IP/device/session where appropriate to make brute-force rotation harder.
7. Use `REDIS_POOL_SIZE`, `REDIS_MIN_IDLE`, and bounded retries from the scaffold defaults instead of app-local hardcoded clients.
8. Use `REDIS_SHARD_URLS` for coarse application-level sharding when one Redis node becomes a CPU/memory hotspot. Stable key hashing keeps single-key operations local to one shard; cross-key analytics should move to Postgres/read models instead of Redis-wide fanout.
9. Use Redis Streams only for ephemeral or coordination-heavy event lanes. Durable business workflows still need Postgres/River/outbox as the replayable source.
10. The scaffold Redis config disables RDB/AOF persistence by default. Re-enable persistence only if an app explicitly makes Redis part of its recovery contract and has tests for restart/replay semantics.

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
