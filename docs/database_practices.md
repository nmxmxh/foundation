# Standalone Apps Database Practices

Status: baseline
Date: 2026-04-20

## Purpose

This is the cross-app database standard for Ovasabi standalone apps. It is optimized for modular-monolith systems with strong domain boundaries, deterministic migrations, and low operational overhead.

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

## Query and transaction rules

1. Use parameterized SQL only.
2. Keep transactions short and scoped.
3. Use explicit ordering + strict pagination (explicit bounding via `LIMIT`/`OFFSET` or Cursor pagination) for all list queries. Unbounded fetches loading tables into memory are prohibited.
4. Prefer deterministic upserts (`INSERT ... ON CONFLICT ... DO UPDATE`) for seed and idempotent command handlers.
5. Never perform full-table updates/deletes without explicit scoped predicates.
6. Do not wrap indexed columns in runtime casts/functions on hot paths. Use range predicates like `timestamp >= day_start AND timestamp < day_end` instead of `timestamp::date = ...`.
7. N+1 reads inside write loops are performance bugs. Preload reusable reference data once per command or document.
8. Dynamic sort fields, projection lists, and filter operators must come from allowlists. Do not concatenate user input into SQL identifiers or query fragments.
9. Authorization predicates must be part of the read/write query itself or enforced by an equivalent DB policy. Do not fetch by ID first and rely on a later in-memory scope check for sensitive rows.
10. High-value or uniqueness-sensitive mutations must use unique constraints, row/advisory locks, or `SERIALIZABLE` transactions to prevent race-driven double execution, quota bypass, or state desynchronization.
11. Audit tables or append-only logs must capture privilege changes, exports, payout/billing actions, and destructive operations with actor and correlation data.

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

## Performance and operations

1. Measure p50/p95/p99 for write and read critical paths.
2. Track lock wait time and slow query volume.
3. Add indexes based on observed query plans, not guesswork.
4. Batch background writes where possible.
5. Prefer batched writes for child-row inserts. Use `COPY`/`CopyFrom` or batched statements for child rows, association rows, and other repeated inserts.
6. Document-ingestion flows should distinguish re-upload dedupe from legitimate recurring records. Repeated records from valid recurring processes must not be collapsed just because key fields or timestamps are similar.

## Ingestion and idempotency

1. Every ingestion pipeline must produce a stable fingerprint for dedupe.
2. Re-upload dedupe must be scoped by source and domain, not just amount/date similarity.
3. Similarity matching may assist recovery of legacy rows, but deterministic keys remain authoritative.
4. Repeated child-row inserts must use batch primitives where supported.

## Load-test observations (2026-02-15)

1. Recurring-path throughput improved materially after reducing hot-path query round-trips (auth/scope checks and summary aggregates): 400 concurrency moved from ~281 RPS with ~1364ms average latency to ~1991 RPS with ~186ms average latency at 0.00% errors.
2. Organization summary should follow the same compact-mode pattern as individual summary:
   `summary_mode=compact` disables heavy expansions (large related collections, detailed breakdowns, expensive derived sections).
3. For org-scoped reads, collapse membership + role + permission resolution into one query where possible; avoid sequential role and permission lookups in the request path.
4. Prefer combined aggregate queries (`SUM(CASE ...)`) for related metrics instead of separate per-metric queries.
5. Keep expensive derived sections optional and metadata-driven so high-frequency dashboards can run in compact mode by default.
6. Any runtime summary query must have bounded result sets (`LIMIT`, explicit period window, deterministic order).

## Connection and concurrency budgets

1. Pool sizing must be environment-driven, never hardcoded in service code.
2. Keep per-instance `max_conns` conservative; scale app/worker replicas before pushing very high per-process pools.
3. Reserve headroom in Postgres for migrations, admin sessions, and failover events.
4. Introduce PgBouncer transaction pooling before large horizontal fanout.
5. Monitor pool acquire latency and timeout rate as saturation signals.

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

## Ingestion observations (2026-03-26)

1. The biggest gain was not more SQL tricks; it was removing repeated lookups inside document-processing loops.
2. Batch creation improves most when both sides are addressed together: fewer round trips before insert, then batched inserts for the final write.
3. Similar-entry logic must be source-aware. A document-import duplicate rule should not accidentally catch manual entries or legitimate recurring events.
4. App-level duplicate checks are good UX, but important recurring commands still need DB-backed uniqueness.
5. Query shape matters as much as indexing. A good index can still be bypassed by a bad predicate.
