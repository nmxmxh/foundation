# Standalone Apps Redis Practices

Status: baseline
Date: 2026-04-20
Updated: 2026-10-31 (bounded ranked windows, Redis budget manifest, tier ownership)

## Purpose

Redis is for ephemeral speed and coordination, not durable business truth.

Redis should remove repeated network/DB work from side paths; it should not decide durable state transitions alone, and it is not a tier between Hermes and Postgres.

Read order by lane:

1. Data served from a Hermes projection: Hermes, then the durable store. No Redis tier between them. Hermes is already the in-process hot plane; a Redis tier there adds a network round trip to every miss and a second copy that must be written, trimmed, and invalidated (see "Tier ownership").
2. Side reads with no projection (summaries, idempotency fences, counters): process memory, then Redis through the `cache` package, then the durable store.

## Non-negotiable rules

1. Do not store source-of-truth business records in Redis.
2. All Redis keys must have deterministic naming and TTL policy.
3. Code paths must degrade gracefully when Redis is unavailable.
4. Redis usage must be injectable and testable.
5. Auth/session or authorization-sensitive Redis data must fail closed. Redis must not become the only durable source for permission decisions that would fail open if keys disappear or are evicted.

## Absent-key semantics

All drivers share one absent-key contract: a key that was never written, expired by TTL, or already deleted reads as `(nil, nil)` from `Get`. `Del` on absent keys returns no error. This is a deliberate divergence from raw go-redis, which surfaces `redis.Nil` from `GET`. The live driver normalizes `redis.Nil` at the boundary (`server-kit/go/redis/client.go`, `redisClient.Get`) so both drivers stay identical.

Rules for consumers:

1. Never infer existence from a `Get` error. An absent key produces no error.
2. Prove existence with `Exists` (`KeyAdminClient`) or decide from value length.
3. Tests must assert absence through zero-length values, not error values.

Regression guard: `TestAbsentKeySemanticsContract` pins the full contract for the memory driver; cross-driver parity is covered by the service-backed suites.

## Allowed use cases

1. Short-lived caches.
2. Rate limiting counters.
3. Idempotency fences/locks.
4. **Reliable Event Streaming**: Use Redis Streams (`XADD`, `XREADGROUP`) for business-critical events requiring "at-least-once" delivery.
5. **Distributed Coordination**: Use Fenced Distributed Locks for cross-process resource protection.
6. Lightweight pub/sub notifications (transient only).
7. Bounded ranked sets (`SortedSetClient`): recent-signal feeds, leaderboards, trending counters — grown only through the bounded-window helpers in "Bounded ranked windows" below.
   - Driver parity is mandatory for ranked sets: ZRevRange mirrors the ascending order exactly, so equal scores return reverse-lexicographic — verified identical on the memory and live-server drivers by cross-driver tests (`redis/sortedset_test.go`, `servicebacked/sortedset_test.go`). A memory-client implementation that "passes alone" hides real-Redis divergence.
   - Validate mirror/ingest stats at the LISTENER choke point, not inside individual sinks: a guard behind one adapter passes every in-memory test and silently disappears for every other consumer (caught service-backed, 2026-08-23).
8. Transient session management.

## Lane separation: bus, coordination, side cache

One Redis deployment serves three lanes. Keep them separate in key design and
in review:

| Lane | Owner | Examples | Forbidden |
| --- | --- | --- | --- |
| Transport bus | `events.RedisBus`, hermes tailers | Streams, envelope fanout, pub/sub invalidation wakeups | Durable truth, per-read lookups |
| Coordination | Hermes, workers | Watermarks, rebuild leases, heartbeats | Business records |
| Side cache | `cache` package (`RedisBackend`) | Summary/result caches, idempotency fences, counters | Authorization decisions, hot-path reads |

Rules that keep the lanes honest:

1. Hermes projections are the read-model lane. Do not put `cache.GetOrSet` in
   front of Hermes reads; `docs/hermes_hotplane.md` forbids per-read Redis
   dependencies and broad wildcard scans on product hot paths.
2. The `cache` package serves side reads only. Entries stay non-authoritative
   per the package contract; balances, permissions, and uniqueness checks read
   durable state.
3. Cross-process invalidation uses shared tag markers
   (`Invalidator.Tag`/`InvalidateTag` over `NewRedisBackend`). Markers are
   durable intent: a subscriber that was down reconciles from markers or TTLs,
   so a missed wakeup degrades convergence speed, never correctness.
4. Pair shared invalidation with `cache.InvalidationBus` wakeups on the
   transport channel so process-local memory caches drop entries immediately.
   The broadcast is a hint; markers are truth.
5. Tag marker keys live under `<prefix>:__tag__:<tag>:<hex(key)>`. Never read
   them as data, never hand-format sibling namespaces, and keep pattern scans
   out of request paths — they belong to mutation and maintenance lanes only.

## Bounded ranked windows

Sorted-set members have no TTL of their own. A key-level `EXPIRE` only fires
once writes stop, and every `ZADD` keeps it alive, so a sorted set that is
added to but never trimmed grows until `maxmemory`. Then `allkeys-lfu` evicts
whatever it samples, including the index itself. Pronto hit this three times
on 2026-08-23/24, ending in a host OOM. The 2026-10-31 review found the same
pattern still live in pronto's facet indexes and in the chowdash and trotters
demand buffers.

Use `server-kit/go/redis`:

| Shape | Helper | What it guarantees |
| --- | --- | --- |
| Ranked counter (trending, demand, leaderboard) | `BumpRanked(ctx, c, key, member, delta, ttl, keep)` | One `ZINCRBY`, TTL renewed, set capped at `keep`. Refuses a zero TTL or cap. |
| Window index with payload rows | `TrimRankedWindow(ctx, c, key, WindowTrim{Keep, PayloadKey, BatchSize, MaxPasses})` | Evicted payloads unlinked before their members leave the index; bounded passes |
| Index without payloads (facet, collection) | `TrimRankedWindow(ctx, c, key, WindowTrim{Keep})` | One rank trim |

Rules:

1. Every file that adds to a sorted set (`ZAdd`, `ZAddMany`, `ZIncrBy`) also
   trims it through one of the helpers, or `ZRemRangeByRank`. Enforced by
   `redis_practices_check`.
2. Ranked counters live in the ranked set. Do not keep a per-member `INCR`
   counter and copy it into the set. That needs one key per member, each with
   a TTL that is easy to forget, plus one round trip per unit of quantity.
   Both demand buffers had counters that never expired.
3. Payload behind an index: write payloads before the index, so a reader
   never sees a member without its row. If either write fails, unlink the
   batch's payloads. When payload existence doubles as the duplicate test, a
   payload stranded by a failed index write rejects every re-delivery for the
   payload TTL while the row stays invisible.
4. Evict payload rows with `TrimRankedWindow` and a `PayloadKey`. Never rank
   trim first and `DEL` second: a `DEL` that fails or times out strands the
   payloads for their whole TTL. That order caused the 2026-08-24 spiral.
5. Every secondary index gets its own cap, at most
   `min(window capacity, deepest read)`. A facet index over a master feed holds
   members the master already evicted, and a read never looks past its
   deepest range, so those members are pure growth. "Trimmed like the master"
   is not a cap unless code trims it.
6. Track which secondary indexes grew and trim them on the amortized cadence.
   An index touched only by batches that skip the trim must still be capped
   at the next pass.

## Redis budget manifest

A window's size and the Redis memory cap are set in different files. Pronto
had four values for one window: `docker-compose.yml` defaulted to 200,000,
`config.go` to 50,000, the code constant to 200,000, and `redis.conf` was
sized for 100,000. The deployed default needed about 1.8 GB against a 1.5 GB
cap.

Any app with a ranked window keeps `config/redis_budget.tsv`, one row per
key family:

```text
# family	max_entries	bytes_per_entry	bound	keys
signal_window	100000	9216	env:SIGNAL_HOT_BUFFER_LIMIT,const:defaultHotBufferLimit
dedup_markers	120000	350	-
trending_dishes	500	96	const:trendingKeep	200
```

- Columns are tab-separated. `max_entries` is the cap per key. `keys`
  (optional, default 1) is how many keys share the cap, such as one trending
  set per tenant at the tenant ceiling you plan for.
- `max_entries x bytes_per_entry x keys`, summed over all rows, must stay
  within 80% of `maxmemory` in `config/redis.conf`.
- `bound` ties a row to the settings that size it, comma-separated. With
  `env:VAR`, every default for `VAR` must equal `max_entries`. That covers
  `${VAR:-N}` in compose files, `VAR=N` in `.env.example`, and
  `getEnvInt("VAR", N)` in Go. With `const:NAME`, the Go constant must equal
  it. Use `-` for families bounded only by a TTL.
- `redis_practices_check` enforces all three: the file exists when the app
  uses ranked sets, the sum fits, and every bound default agrees. To change a
  window, change the manifest, the env defaults, and `maxmemory` together.

## Tier ownership

A cache tier needs a production writer. Without one, every read pays a round
trip to miss.

1. Each tier you add names its writer, and that writer has a production
   caller. In chowdash and trotters, `logistics.RedisBuffer` and the Hermes
   edge projection were read on every estimate, but no code wrote to either.
   A plan made one lookup per candidate pair, each with a 500ms Redis
   timeout, inside a 15 second deadline.
2. Durable first, hot second. Expose one write path that commits to the
   durable store and publishes to the hot tier only after that commit
   succeeds (`logistics.Runtime.CommitEdge`). Scattered write calls let tiers
   disagree.
3. A process-local decoded-row memo keyed by a Redis generation counter is
   wiped by every accepted write. Under steady ingest its hit rate falls to
   almost zero. If the memo carries the read path, the data belongs in a
   Hermes projection.

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
8. **Scalable Counting**: Use **HyperLogLog** (`PFADD`, `PFCOUNT`) for estimating the cardinality of unique items (for example, daily unique users) when a ~1% error margin is acceptable. This maintains a constant 12KB memory footprint regardless of scale.
9. **No O(N) Commands**: Strictly avoid `KEYS *`, `SMEMBERS` on large sets, or `HGETALL` on large hashes. Use `SCAN`, `SSCAN`, and `HSCAN` instead to prevent blocking the Redis event loop.
10. Group repeated cache writes/invalidations into bounded pipeline batches. Large pipelines must be chunked so queued replies do not become the memory problem.
11. Use `allkeys-lfu` for generic cache nodes unless the workload demands TTL-only eviction. LFU adapts better to mixed hot/cold cache traffic than pure recency.
12. Keep Redis timeouts shorter than the app lane budget. A failed cache read should fall through quickly; a Redis stall must not consume a full API request budget.
13. The Foundation memory Redis driver is a contract test double, not a production store. It should preserve Redis-like semantics for copied values, TTLs, locks, streams, and pub/sub patterns so tests catch drift, but production throughput claims still require service-backed Redis benchmarks.
14. Redis clients should be initialized through `ConnectWithOptions` so pool size, min-idle, retry, shard URL, dial, read, and write budgets are inherited from config instead of being silently discarded.
15. Use `redis.BatchClient` for multi-key cache hydration and write-through paths. `SetMany`, `GetMany`, and `SetGetMany` keep app code on Foundation boundaries while still using Redis pipelining underneath.
16. Use `redis.StreamBatchClient.XAddMany` for durable event relay bursts. Eventlog/outbox drains must claim pending Postgres rows with a bounded lease before Redis publication, pipeline Redis Stream appends, then batch the token-checked durable published-state update in Postgres while preserving per-entry success/failure diagnostics.
17. Do not benchmark a busy loop of sequential single-key calls as the target shape. Measure it as a baseline, then compare parallel, pipelined, and batch-per-key costs.

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
11. The shard-routing function (key → shard index) sits on every routed operation, so keep it allocation-free — compute the hash inline over the key, do not build a hasher or `[]byte(key)` per call — and treat its hash as a data-placement contract. Changing the routing hash silently remaps existing keys onto different shards, so any change must ship with a parity oracle proving identical placement (see `server-kit/go/redis` `TestShardIndexMatchesStdlibFNVOracle`) and a service-backed physical-placement check against a real multi-shard cluster (`TestShardedClientPlacesKeysServiceBacked`).

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
2. TTL documented and tested. Ranked sets use the bounded-window helpers.
3. Key family listed in `config/redis_budget.tsv` with its bound, if it can grow with traffic.
4. Every cache tier has a production writer, and a test that fails if the durable write is refused but the hot tier still publishes.
5. Fallback behavior tested for Redis downtime.
6. Metrics and alerts in place for error rate and memory pressure.
7. Key families reviewed for tenant bleed, cache poisoning, and stale-session invalidation behavior.
