# Standalone Apps Database Practices

Status: baseline
Date: 2026-05-01

## Purpose

This is the cross-app database standard for Ovasabi standalone apps. It is optimized for modular-monolith systems with strong domain boundaries, deterministic migrations, and low operational overhead.

## Architecture posture

Postgres gives this architecture the ACID side of the system: durable state, constraints, transactional idempotency, outbox records, auditability, and analytical read models. The BASE side should be built around append-only events, materialized/read-model tables, bounded caches, River durable workers, Redis ephemeral coordination, and idempotent retry semantics. Do not turn Redis, queues, or app memory into hidden sources of truth.

Default lane budgets:

1. `hot_read`: target single-row/materialized reads, 50ms query timeout, tenant predicate required.
2. `hot_write`: command validation plus one short transaction, 150ms query timeout, idempotency key required where retryable.
3. `default`: normal product API query budget, 250ms query timeout.
4. `background`: bounded batch/worker operations, 2s query timeout, batch diagnostics preserved.
5. `analytics`: report/materialized-view refresh work, 5s query timeout, never called from hot ingress paths.

Use `server-kit/go/database.DefaultPoolOptionsFor` and `QueryBudgetContext` as the default app wiring. Pool budgets are CPU-aware and intentionally conservative so app replicas scale before Postgres connection count becomes the bottleneck.

## PostgreSQL 18 baseline

Foundation scaffolds now default local/test Postgres to version 18 because the release adds primitives that match our performance model:

1. Async I/O improves eligible sequential scans, bitmap heap scans, vacuums, and related operations. The scaffold config enables the portable `io_method = worker` baseline with explicit combine/concurrency settings; production Linux hosts may benchmark `io_uring` if the Postgres build supports it.
2. Multicolumn B-tree skip scans make some composite indexes useful even when the leading column is not fully constrained. This is useful, but it does not remove our rule that hot tenant/campaign paths must filter by tenant/campaign first.
3. Virtual generated columns are now the generated-column default. Use them for derived searchable/display fields when recomputation on read is cheaper than write amplification; use `STORED` only when the read path proves it needs precomputed storage.
4. Parallel GIN index creation and improved hash join/GROUP BY behavior help search and analytics lanes, not unbounded runtime queries.
5. `pg_stat_io`, `pg_aios`, vacuum timing, WAL I/O timing, and richer `EXPLAIN` output are part of the operational contract. Production observability should include read/write bytes, WAL pressure, vacuum/analyze timing, pool acquire latency, and top query families.

Production Postgres must still be sized from workload evidence: memory, WAL, autovacuum, partition strategy, and connection pool limits should be derived from p95/p99 latency, write rate, table growth, and EXPLAIN plans.

## Query-engine optimization posture

Foundation repository code should be written so PostgreSQL's optimizer can do
the same work a standalone analytical query engine would do explicitly:
projection pushdown, predicate pushdown, limit pushdown, partition pruning,
late materialization, and bounded batch/stream execution.

Design rules:

1. Projection pushdown: every repository method must list only the columns it
   needs. This keeps heap/TOAST reads out of paths that only need identifiers,
   states, timestamps, counters, or display summaries.
2. Predicate pushdown: tenant scope, authorization scope, time windows, state,
   cursor bounds, and report mode must be inside the SQL predicate, not applied
   after fetching rows.
3. Limit pushdown: first-page and compact dashboard reads must include finite
   `LIMIT` values at the database boundary. Do not fetch a broad candidate set
   and slice it in Go.
4. Partition pruning: partition keys must appear as simple predicates that the
   planner can recognize. Avoid wrapping partition/index columns in functions
   or casts in hot queries.
5. Late materialization: filter and order on compact indexed columns before
   reading wide JSONB, text, binary, or joined detail payloads.
6. Batch execution: use `QueryEach` for bounded streaming reads and `QueryAll`
   only for small, deliberately retained result sets. Report/export paths must
   process in batches instead of constructing one process-wide row slice.
7. Physical-plan review: important query changes must include `EXPLAIN
   (ANALYZE, BUFFERS, WAL, VERBOSE)` evidence and call out whether the plan
   uses index scan, index-only scan, bitmap heap scan, seq scan, partition
   pruning, sort, hash aggregate, nested loop, hash join, or materialization.
8. CPU locality review: when a repository path feeds Go/Rust/TS scan logic,
   prefer compact columns, ordered cursors, and late materialization so the app
   loop touches predictable cache lines instead of chasing JSON maps, wide row
   objects, or pointer-heavy joined structures.

Index rules from this posture:

1. Hot first-page reads should have one index whose leading columns match scope
   and equality predicates, followed by range/order columns.
2. Covering indexes with `INCLUDE` are allowed only for measured hot reads where
   index-only scans are realistic and the extra index bytes do not hurt write
   lanes more than they help reads.
3. Partial indexes must match the query predicate text closely enough for the
   planner to prove implication. If the predicate is not stable in query code,
   do not rely on the partial index.
4. Query plans that sort after scanning many rows are suspect for runtime
   endpoints. Prefer order-aware indexes or materialized read models.
5. If an optimizer cannot push a predicate through a CTE, view, function,
   window, or JSON expression, rewrite the repository query or add a read model
   rather than assuming the planner will recover the intended shape.

## Non-negotiable rules

1. PostgreSQL is the system of record for durable business state.
2. Every org-scoped table must include `organization_id` and an index path including `organization_id`.
3. Every write path must be idempotent at the command boundary and enforce uniqueness at the data boundary.
4. Duplicate prevention must use deterministic business keys or source fingerprints at the data boundary; fuzzy similarity is only a fallback, never the primary uniqueness rule.
5. Recurring domain events must have explicit uniqueness rules. Example: scheduled runs should be unique on (`organization_id`, `run_type`, `period_start`, `period_end`, `effective_date`).
6. No external network calls inside open DB transactions.
7. No unbounded queries in runtime paths.
8. Queries on filter predicates without supporting indexes are performance bugs. Index queried fields aggressively.
9. Externally referenced rows must expose opaque public identifiers or tightly scoped lookup keys. Authorization must not depend on guess-resistance of sequential IDs.
10. Security-critical uniqueness, balance, and state-transition rules must be enforced by constraints or in-transaction locking, not app-side prechecks alone.
11. Schema changes must be synchronized with service code and integration test mocks in the same commit. A migration is incomplete until all dependent queries and mocks are reconciled.

## Schema design rules

1. Use `BIGSERIAL`/`BIGINT` primary keys and stable external refs (`*_ref`, `public_id` UUID where needed).
2. Use `TIMESTAMPTZ` for all temporal columns.
3. Use `JSONB` only for flexible metadata, not core filter keys.
4. If a query uses JSONB tags or metadata paths in a recurring path, add a matching GIN or expression index immediately. JSONB is acceptable for cross-cutting metadata only when index-backed.
5. Expression indexes must mirror the actual predicate shape. Example: if the query is `LOWER(name)` with `is_active = TRUE`, the index should be `ON (..., LOWER(name)) WHERE is_active = TRUE`.
6. Index all foreign keys and high-selectivity predicates.
7. Keep domain table prefixes explicit (`identity_*`, `operations_*`, `billing_*`, etc.).
8. Store password-reset tokens, invite tokens, API tokens, and other bearer secrets as digests or encrypted blobs when raw-value lookup is unnecessary.
9. Separate especially sensitive fields (PII, tax identifiers, recovery data) from broad read paths and duplicate them as little as possible.
10. App runtime roles must default to least privilege on tables, sequences, functions, and views.

## Foundation state store

The scaffolded Postgres schema must include `governance_state_records` because `server-kit/go/database.PostgresDB` uses it as the durable implementation of the `StateStore` contract. Generated apps that default `STATE_STORE_DRIVER=postgres` must not require application teams to rediscover this table manually.

Required base shape:

1. Columns: `domain`, `collection_name`, `organization_id`, `record_id`, `data jsonb`, `created_at`, and `updated_at`.
2. Unique identity: `(domain, collection_name, organization_id, record_id)`.
3. Tenant/list index: `(domain, collection_name, organization_id, updated_at DESC, record_id ASC)`.
4. Organization cleanup index: `(organization_id)`.
5. JSONB index: `USING GIN (data jsonb_path_ops)` for app-specific containment/search lanes.
6. Foundation state filter index: `(domain, collection_name, organization_id, btrim(data ->> 'state'), updated_at DESC, record_id ASC) WHERE data ? 'state'` for the common `state` predicate plus first-page ordering.

Adapter semantics:

1. `UpsertRecord` normalizes the durable identity fields and mirrors `organization_id` into `data`.
2. `GetRecord`, `ListRecords`, `CountRecords`, and delete operations must run under `QueryBudgetContext`.
3. Scalar filters must be applied before `LIMIT` in SQL, then rechecked in Go so MemoryDB and Postgres preserve the same visible filter behavior.
4. Recurring JSONB filter fields need app-owned expression indexes that match the exact predicate. Example: if a hot read uses `state`, add a scoped expression index for `btrim(data ->> 'state')`.
5. Bounded list reads should match predicate and order in the same index where possible. A JSONB GIN index can narrow containment searches, but first-page runtime reads also need B-tree order support for `ORDER BY updated_at DESC, record_id ASC LIMIT n`.

## Query and transaction rules

Use `server-kit/go/database` executor helpers for repository operations before
reaching for raw driver calls:

1. `ExecCommand` for command statements that do not need returned rows.
2. `ExecRowsAffected` for strict update/delete paths that must distinguish
   "not found" from "updated" without exposing driver-specific command tags.
3. `QueryOne` for single-row reads/writes with `RETURNING`.
4. `QueryEach` for bounded streaming reads where retaining all rows is not appropriate.
5. `QueryAll` for typed, bounded list reads.
6. `AtomicLane` for short transactions with a lane-specific timeout budget.
7. `PostgresDB.SendBatch` for independent statements that should amortize one
   client/server round trip.
8. `PostgresDB.CopyFromSource` / `CopyFromRows` for bulk ingest.

Performance order for write-heavy paths, fastest to slowest when the workload
matches the lane:

1. `COPY`/`CopyFrom` for bulk append/import.
2. Batched statements or prepared repeated statements inside one short transaction.
3. Single `INSERT ... RETURNING` / `UPDATE ... RETURNING` through `QueryOne`.
4. Repeated one-row writes with independent commits.

Service-backed benchmarks must show all relevant lanes, not only the slowest
single-row path. For Foundation state-store writes, compare:

1. single `UpsertRecord` for semantic cost;
2. `RawStateStore.UpsertRecordJSON` for byte-preserving handlers that already own canonical JSON and do not need immediate map mutation;
3. parallel `UpsertRecord` for pool/concurrency behavior;
4. `SendBatch` for independent per-row diagnostics with fewer round trips;
5. `CopyFromRows` for append/import workloads that do not need per-row upsert semantics.

`DBTX` intentionally remains small so command-only fakes, transactional helpers,
and state stores are easy to test. Repositories that need streamed rows should
opt into `RowQueryer` instead of widening every fake and store. This keeps the
contract testable while still exposing the fast row path for hot repositories.
Repositories that need row counts should opt into `ResultExecutor`; this keeps
strict conflict/not-found behavior portable across Postgres and tests.

Foundation Postgres connections must keep pgx statement caching enabled for
stable repeated SQL. `PoolOptions.StatementCacheCapacity` defaults to a non-zero
capacity, `PostgresDB` forces `QueryExecModeCacheStatement`, and existing
`pgxpool.Pool` instances should be projected through `WrapPostgresPool` while
legacy services migrate toward the Foundation repository interfaces.

1. Use parameterized SQL only.
2. Keep transactions short and scoped.
3. Use explicit ordering + keyset/cursor pagination for all list queries. **Prohibit `OFFSET` for large datasets**; as offset increases, Postgres must still scan and discard the preceding rows, leading to linear performance degradation. Use `WHERE id > last_seen_id` instead.
4. Prefer deterministic upserts (`INSERT ... ON CONFLICT ... DO UPDATE`) for seed and idempotent command handlers.
5. Never perform full-table updates/deletes without explicit scoped predicates.
6. Do not wrap indexed columns in runtime casts/functions on hot paths. Use range predicates like `timestamp >= day_start AND timestamp < day_end` instead of `timestamp::date = ...`.
7. N+1 reads inside write loops are performance bugs. Preload reusable reference data once per command or document.
8. Dynamic sort fields, projection lists, and filter operators must come from allowlists. Do not concatenate user input into SQL identifiers or query fragments.
9. **No `SELECT *`**: Explicitly list required columns. This reduces network I/O, prevents "wide-row" performance penalties, and ensures schema evolution (e.g., adding a large JSONB column) doesn't accidentally degrade unrelated read paths.
10. Authorization predicates must be part of the read/write query itself or enforced by an equivalent DB policy. Do not fetch by ID first and rely on a later in-memory scope check for sensitive rows.
11. High-value or uniqueness-sensitive mutations must use unique constraints, row/advisory locks, or `SERIALIZABLE` transactions to prevent race-driven double execution, quota bypass, or state desynchronization.
12. Audit tables or append-only logs must capture privilege changes, exports, payout/billing actions, and destructive operations with actor and correlation data.

## Migration policy (active development)

During active v1 development, use the three-group structure:

1. `0001_schema` (all schema + indexes)
2. `0002_seed_system_data` (deterministic baseline seeds)
3. `0003_seed_demo_data` (deterministic demo/test seeds)

Rules:

1. Edit these migration groups directly during active model evolution.
2. Do not add `0004+` migration groups without ADR and rollout justification.
3. Provide paired `up` and `down` scripts for each active migration group.
4. Seed data must carry stable markers (example: `seed_version`) for safe rollback.
5. When databases are resettable, fold new indexes and constraints back into `0001_schema` before release. Temporary incremental migrations are acceptable only as rollout aids and should be squashed on the next reset.
6. Maintain the strict three-group migration structure during active development. Folding changes back into `0001_schema`, `0002_seed_system_data`, and `0003_seed_demo_data` keeps the bootstrap path deterministic.

## Performance and operations

1. Measure p50/p95/p99 for write and read critical paths.
2. Track lock wait time and slow query volume.
3. Add indexes based on observed query plans, not guesswork.
4. Batch background writes where possible.
5. Prefer batched writes for child-row inserts. Use `COPY`/`CopyFrom` or batched statements for child rows, association rows, and other repeated inserts.
6. Document-ingestion flows should distinguish re-upload dedupe from legitimate recurring records. Repeated records from valid recurring processes must not be collapsed just because key fields or timestamps are similar.

## SSD and write-amplification posture

The SSD paper's storage-engine recommendation is clear: database engines that
own page placement can reduce total write amplification with out-of-place
writes, page packing, hot/cold grouping by expected deathtime, ZNS/FDP
placement, and GC-unit alignment. Foundation does not own PostgreSQL's storage
engine, so we cannot retrofit those mechanics into heap pages. We can still
reduce the write pattern that Postgres and the SSD observe.

Foundation write-amplification rules:

1. Treat total write amplification as a product metric: logical domain writes
   produce heap writes, index writes, WAL writes, checkpoint writes, vacuum
   work, and finally SSD-internal writes.
2. Minimize indexes on write-heavy tables. Each index improves some reads but
   adds write, WAL, vacuum, cache, and SSD pressure to every relevant mutation.
3. Prefer append-only fact tables and partition drop/attach retention for event,
   audit, telemetry, outbox history, and report facts. Deleting old ranges row
   by row is a write-amplification bug.
4. Separate hot mutable columns from cold/wide detail payloads. Do not update a
   large JSONB or TOAST-heavy row when the command only changes compact state,
   counters, timestamps, or lifecycle metadata.
5. For update-heavy tables, design for HOT updates: avoid indexing frequently
   updated columns unless the read path proves it, and set table `fillfactor`
   below 100 only after measuring the update rate, row width, and bloat trend.
6. Use virtual generated columns when recomputation on read is cheaper than
   stored write amplification. Use `STORED` generated columns only for proven
   hot predicates/projections.
7. Tune checkpoints from evidence. Frequent checkpoints increase full-page WAL
   images after each checkpoint; oversized checkpoint windows can increase
   recovery time and dirty-buffer pressure. Track `pg_stat_wal`, `pg_stat_io`,
   checkpoint logs, and WAL bytes per business operation.
8. Keep `full_page_writes` on unless storage guarantees and recovery posture are
   formally reviewed. It protects against torn pages; disabling it is not a
   normal Foundation optimization.
9. Consider WAL compression for write-heavy workloads with high full-page image
   volume, but benchmark CPU cost and replica/archive behavior first.
10. For SSD/NVMe hosts, random-page-cost tuning should be evidence-based and
    paired with plan review. Lower random I/O cost can make index access more
    attractive, but a bad index still creates write amplification.
11. For high-volume ingest, use `COPY`, batched statements, staging tables, and
    set-based merge/upsert. Per-row command loops multiply parse, round-trip,
    WAL, and index maintenance cost.
12. For append-only and insert-mostly tables, configure insert-triggered vacuum
    and analyze thresholds when index-only scans, visibility maps, or planner
    estimates matter.

SSD evidence to collect on service-backed load tests:

1. WAL bytes and full-page image ratio per operation family.
2. `pg_stat_io` read/write/extend/fsync counts and timings by backend type.
3. Checkpoint frequency, checkpoint write/sync time, and checkpoint-trigger
   reason.
4. Autovacuum/analyze duration, dead tuples, index bloat, table bloat, and HOT
   update ratio for mutable tables.
5. Host-level disk write bytes and utilization; on managed/cloud SSDs, track
   volume write IOPS/throughput, latency, burst-credit exhaustion, and device
   wear/endurance metrics where exposed.

## Database state-machine invariants

Use `foundation/docs/tla_architecture_practices.md` for high-risk DB workflows where concurrency, retries, or performance optimizations can change behavior.

1. `DBUniquenessAuthoritative`: security-critical and idempotency-critical uniqueness must be enforced by constraints, indexes, locks, or serializable transactions, not only app prechecks.
2. `TransactionScopeBounded`: DB transactions must have finite scope, finite timeout, and no external network call inside the open transaction.
3. `OutboxRefinement`: publishing an event is a lower-level implementation of the durable state transition; the durable outbox write must exist before publication can be observed as successful.
4. `QueryBounded`: runtime queries must have tenant predicates, explicit order, finite limits, and no unbounded offset scans.
5. `RetryIdempotent`: retrying a DB-backed command must converge on the same visible state or controlled duplicate result.
6. `BatchDiagnosticsPreserved`: batching may change internal execution, but visible per-record success/failure identity and stage diagnostics must remain available.
7. `LockProgressBounded`: lock waits and pool acquire waits must have hard timeouts and visible failure classes.

State-machine candidates that deserve table-driven/property-style tests:

1. outbox insert -> publish -> mark published/failed
2. idempotent command insert/update under duplicate submissions
3. worker lease acquire -> renew -> complete/fail/expire
4. batch ingestion with partial failure and retry
5. report/export expanded reads vs compact dashboard reads

## Ingestion and idempotency

1. Every ingestion pipeline must produce a stable fingerprint for dedupe.
2. Re-upload dedupe must be scoped by source and domain, not just amount/date similarity.
3. Similarity matching may assist recovery of legacy rows, but deterministic keys remain authoritative.
4. Repeated child-row inserts must use batch primitives where supported.
5. Batch handlers must resolve internal organization and user IDs efficiently, such as via one fetch, scoped preload, or short-lived command cache, to avoid N+1 queries during batch-write preparation.
6. Batch ingestion errors must include the record key/index and stage name so retry, quarantine, and operator diagnostics can target the failed record without replaying the whole batch blindly.

## Load-test observations (2026-02-15)

1. Recurring-path throughput improved materially after reducing hot-path query round-trips (auth/scope checks and summary aggregates): 400 concurrency moved from ~281 RPS with ~1364ms average latency to ~1991 RPS with ~186ms average latency at 0.00% errors.
2. Organization summary should follow the same compact-mode pattern as individual summary:
   `summary_mode=compact` disables heavy expansions (large related collections, detailed breakdowns, expensive derived sections).
3. For org-scoped reads, collapse membership + role + permission resolution into one query where possible; avoid sequential role and permission lookups in the request path.
4. Prefer combined aggregate queries (`SUM(CASE ...)`) for related metrics instead of separate per-metric queries.
5. Keep expensive derived sections optional and metadata-driven so high-frequency dashboards can run in compact mode by default.
6. Any runtime summary query must have bounded result sets (`LIMIT`, explicit period window, deterministic order).
7. Report/export views may request expanded transaction detail only with explicit report context metadata (for example `request_context=report`) and must still map to a high but finite backend cap. Dashboard and bootstrap views must use compact or small-limit summary metadata so normal navigation does not inherit report-sized reads.
8. Summary endpoints that combine aggregates with recent activity need matching compound indexes for both the filter and order shape, such as `(tenant/profile_id, transaction_date DESC, created_at DESC)`. A single foreign-key index is not enough for first-page latency.
9. Metadata-driven query options must be parsed consistently from top-level metadata and sanctioned nested metadata containers (`extras`, attributes/fields maps). If a client must place options in metadata, tests must prove the backend honors the exact nesting shape.
10. Treat frontend persistence as part of read-path performance. Persisted report-sized transaction snapshots can slow app hydration even when the backend query is fast; keep offline snapshots intentionally small and refetch expanded reports on demand.

## Connection and concurrency budgets

1. Pool sizing must be environment-driven, never hardcoded in service code.
2. Keep per-instance `max_conns` conservative; scale app/worker replicas before pushing very high per-process pools.
3. Reserve headroom in Postgres for migrations, admin sessions, and failover events.
4. Introduce PgBouncer transaction pooling before large horizontal fanout.
5. Monitor pool acquire latency and timeout rate as saturation signals.
6. **Observability**: Export native `pgxpool` stats (Total, Idle, Active, Acquire Duration) to the Foundation's observability bridge. Alert on high acquire duration or connection exhaustion.
7. Pool saturation must fail under an explicit acquire budget and error class. Tests should simulate `MaxConns` exhaustion with concurrent callers and assert bounded wait, `ErrPoolAcquireTimeout`, and visible pool pressure metrics.
8. **Zero-Allocation Scanning**: In high-throughput paths, use manual `rows.Scan()` or the Foundation's optimized `QueryMaps` bridge to avoid reflection and redundant allocations.
9. **Count Optimization**: Exact `COUNT(*)` is an O(N) operation in Postgres due to MVCC.
    * **Small Sets**: Exact count with index is acceptable.
    * **Large Sets**: Use `EstimateCount` (via `EXPLAIN` plan analysis or `reltuples` catalog statistics) for UI indicators and non-critical analytics.
    * **Hot Counters**: Use a dedicated counter cache table or Redis if exact, high-frequency counting is required.
10. **Index Overhead**: While missing indexes cause slow reads, excessive indexes cause slow writes and increased VACUUM pressure. Audit indexes regularly for usage.
11. Use read models or materialized views for dashboard, feed, search, and analytics reads that would otherwise join many live transactional tables.
12. Use partitioned append tables for high-volume events, audit logs, outbox history, and time/campaign-heavy telemetry. Partition by time, tenant/campaign, or hash only when query pruning and retention policy are explicit.
13. Use `pgx.CopyFrom` or batched statements for high-volume writes. Per-row loops are acceptable only for small control-plane writes.
14. Use `EXPLAIN (ANALYZE, BUFFERS, WAL, VERBOSE)` for slow or important queries on PostgreSQL 18 so CPU, memory, buffer, and WAL costs are visible.
15. Enable `pg_stat_statements` in production and treat top total-time queries as optimization priorities, even if individual latency looks modest.

## Columnar and analytical lane

Postgres remains the authoritative row store for commands, constraints,
idempotency, outbox state, and tenant-scoped transactional reads. Columnar
storage is a separate analytical lane for scan-heavy, append-oriented, or
export-oriented data where the query usually touches a subset of columns across
many rows.

Use the analytical lane for:

1. High-volume telemetry, audit/event history, pricing ticks, media metrics,
   behavioral events, delivery metrics, and report/export facts.
2. Dashboard or cohort reads that scan time windows, group by dimensions, or
   repeatedly recompute aggregates from many transactional rows.
3. ML/signal/vector workloads where the compute path benefits from contiguous
   typed columns, SIMD, GPU buffers, or Rust/WASM/native batch execution.

Do not use the analytical lane for:

1. Hot command validation, balance/ledger mutation, idempotency enforcement, or
   authorization checks.
2. Small single-row lookups where row storage and B-tree indexes already match
   the access path.
3. Any path where eventual projection lag would change visible product
   semantics.

Recommended projection shape:

1. Write durable command state and outbox records in Postgres first.
2. Project append-only facts through a bounded worker using correlation ID,
   organization ID, source event type, schema version, and deterministic
   idempotency/fingerprint fields.
3. Keep compact Postgres read models or materialized views for hot dashboards.
4. Export cold or large analytical partitions to object storage as Parquet or
   another columnar file when report workloads no longer belong on the primary.
5. Use DuckDB, ClickHouse, warehouse jobs, or app-owned Rust/Arrow readers only
   behind report/export/background boundaries unless a benchmark justifies a
   product runtime path.

Columnar-read design rules:

1. Store facts in append-friendly partitions by time and, when query pruning
   proves it, tenant/campaign/hash. Confirm pruning with `EXPLAIN`.
2. Use BRIN for naturally ordered append tables, B-tree for hot dimensions and
   first-page reads, GIN only for indexed JSONB containment/search, and
   materialized summaries for recurring dashboards.
3. Preserve per-record diagnostics when batching projection work. Batching may
   change transport cost, not visible failure identity.
4. Keep projections replayable: the same source event should converge on the
   same fact row or controlled duplicate outcome.
5. Measure warm-cache and cold-cache report runs separately. Page-cache effects
   are part of the lane contract, not noise to hide.
6. Keep runtime columnar projections as structure-of-arrays buffers where
   possible. Contiguous validity, offset, and value buffers let downstream
   filters count cache lines and use SIMD/vector lanes when benchmarks justify
   them.

## Security and compliance

1. Least-privilege DB users per environment. Runtime roles should not own schema objects or run DDL.
2. No credentials in repository.
3. Encrypt transport and implement automated backups (Daily snapshot + Point-In-Time Recovery for production) validated by periodic restore tests.
4. Audit high-risk mutations and policy decisions.
5. Lock down `search_path`, extension installation, function execution privileges, and `SECURITY DEFINER` usage for application roles.
6. For high-risk shared-database multi-tenancy, prefer DB-enforced tenancy controls (for example, RLS or audited security-barrier views) when app-only scoping is too easy to bypass.

## Delivery checklist

1. Migration reset passes from clean database.
2. Integration tests cover new schema behavior.
3. Rollback path validated for seed/data changes.
4. Bootstrap from clean DB must exercise real recurring ingest paths, including duplicate document uploads and recurring business events.
5. Schema/docs updated in same change set.
6. Integration tests cover tenant-isolation, race/duplicate prevention, and sensitive-token storage behavior where applicable.

## Field OS observation (2026-02-16)

1. Keeping only three active migration groups is fast, but it increases risk of schema/seed drift while files are edited in place.
2. Add an explicit pre-merge gate that runs a clean bootstrap (`schema -> system seed -> demo seed`) so seed references fail early if column/constraint names diverge.
3. Org-scoped table indexes should be audited continuously; adding domains without matching org predicates causes avoidable contention under recurring load.

## Merchant hub load observation (2026-05-09)

1. Product-layer slowness can come from query shape even when Foundation dispatch is nanosecond-fast. Measure DB-backed domain flows separately from pure dispatch.
2. Dashboard reads must not recompute live aggregates from transactional tables under normal navigation. Use compact read models, counter shards, or materialized refreshes.
3. A single merchant-wide counter row can become a write-contention point during shared-merchant bursts. If exact hot counters are required, shard them by deterministic business key and sum a bounded shard set on read.
4. Do not add synchronous read-model writes to command paths unless the visible state truly requires them. Invoice creation should stay minimal; terminal payment transitions may update operational counters.
5. Load harnesses must propagate pool settings into the actual startup path. `TEST_DB_MAX_CONNS` is not useful if `DB_MAX_CONNS` remains pinned to a smaller runtime default.
6. Scaffolded workers should apply `database.ApplyPoolOptions` to raw `pgxpool.Config` values so River and app RuntimeStore paths share the same connection, cache, and statement-timeout budgets.
7. Failed load steps must report a bounded sample error. A percentage without the representative SQLSTATE or timeout class is not enough to correct the architecture.
8. Deadlock, lock-wait, pool-acquire, and query-timeout failures are different signals. Preserve their error classes in tests and logs.

## Ingestion observations (2026-03-26)

1. The biggest gain was not more SQL tricks; it was removing repeated lookups inside document-processing loops.
2. Batch creation improves most when both sides are addressed together: fewer round trips before insert, then batched inserts for the final write.
3. Similar-entry logic must be source-aware. A document-import duplicate rule should not accidentally catch manual entries or legitimate recurring events.
4. App-level duplicate checks are good UX, but important recurring commands still need DB-backed uniqueness.
5. Query shape matters as much as indexing. A good index can still be bypassed by a bad predicate.
