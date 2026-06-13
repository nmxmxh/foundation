# Standalone Apps Migration Structure

Status: v1.1
Date: 2026-06-08
Owner: Platform Architecture

## Purpose

Define the full migration lifecycle for Foundation-generated apps — from initial
greenfield development through live-user production operation. This document has
two phases with a hard gate between them.

---

## Phase 1: Active Development (Before Schema Freeze)

### Objective

Keep early-stage architecture deterministic and low-friction by using a fixed
migration group model until schema freeze.

### Required active structure

1. `0001_schema` — full current schema, indexes, constraints, and generated columns
2. `0002_seed_system_data` — deterministic baseline/system seed data
3. `0003_seed_demo_data` — deterministic demo/test seed data

Each group must have `up` and `down` files.

### Rules

1. During active development, update existing migration groups directly.
2. Do not create `0004+` migration groups without a written ADR. The
   `migration_structure_check.sh` enforces this.
3. Keep demo seeds isolated from system seeds.
4. Make seeds idempotent with `ON CONFLICT DO UPDATE` or `ON CONFLICT DO NOTHING`.
5. Ensure down scripts remove only records introduced by matching up script.
6. In higher environments (staging/prod), verified snapshot backups MUST be
   confirmed or taken immediately BEFORE executing structural migrations.
7. Production application identities must not own schema objects or execute
   migrations. Use a separate migrator role or pipeline identity.
8. System/demo seed files must never contain reusable real secrets, long-lived
   tokens, or default production credentials.
9. Data backfills affecting auth, billing, permissions, or destructive state must
   be restartable/idempotent and emit audit-friendly logs.
10. `0003_seed_demo_data` must stay synthetic and must not run in
    staging/production unless an explicit environment policy says otherwise.

### Phase 1 → Phase 2 gate

Move from fixed active structure to incremental migration stream only when ALL
of the following are true:

1. **Schema freeze is declared.** A product owner or architect writes the ADR
   marking v1 schema as frozen. No schema-breaking changes are expected without
   a coordinated version cut.
2. **Backward-compatible rollout is required.** The app is live with real user
   data, and zero-downtime deploys are needed.
3. **ADR documents the transition policy.** The ADR names the exact migration
   number that closes Phase 1, the expand/contract plan for any in-flight schema
   changes, and the team's rollback runbook.
4. **Backups are verified.** A full backup of the production database has been
   taken and a restore test has passed within the same deployment window.

---

## Phase 2: Production Incremental Migration Stream

### Overview

Once Phase 1 is closed, migrations follow an **expand/contract** pattern. No
migration may drop a column, remove an index required by active queries, or
rename a field without a multi-step coordinated rollout.

### Expand/Contract pattern

Every breaking schema change requires at minimum two sequential migrations:

```text
Step 1 — Expand: add the new column/table/index alongside the old shape.
Step 2 — Migrate: backfill data to the new column. App code reads new + old.
Step 3 — Contract: remove the old column after all reads/writes are on the new shape.
```

Do not combine expand and contract in one migration. Each step must be
deployable and rollbackable independently.

### Numbering in Phase 2

After Phase 1 closes at `0003`, new migrations use `0004`, `0005`, etc.
Each migration must be:

1. Numbered strictly increasing from the previous.
2. Named for the domain operation it performs:
   `0004_users_add_verified_at.up.sql`
3. Paired with a reversible down migration unless reversal is documented as
   explicitly not required (e.g., data deletion is itself the rollback).

### Migration rules in production

1. **No network calls inside migrations.** SQL migrations run in a transaction
   context. Never call external services (Stripe, S3, Redis) from a migration.
2. **No application logic.** Migrations must contain only DDL and DML. Business
   logic belongs in workers or startup jobs.
3. **Lock estimation required for large tables.** Before adding a column, index,
   or constraint to a table with >100K rows, run `EXPLAIN` on the equivalent
   operation in a staging environment and document the estimated lock time.
4. **Use `CONCURRENTLY` for index creation on live tables.** `CREATE INDEX
   CONCURRENTLY` avoids full-table locks. Note: it cannot run inside a
   transaction block; use a separate migration file and mark down as
   `DROP INDEX CONCURRENTLY`.
5. **Tenant predicate on every backfill.** Any DML backfill must include
   `WHERE organization_id = ...` or process all rows in bounded batches.
6. **Idempotent seeds in production.** If `0002_seed_system_data` or any
   production-seed migration runs again (e.g., on a fresh replica), it must
   produce the same final state with `ON CONFLICT DO UPDATE`.
7. **Restartable data movement.** Backfills affecting >10K rows must use keyset
   pagination and checkpoint progress so they can be restarted after a failure.
8. **WAL impact.** Large DML migrations emit significant WAL. Coordinate with
   infrastructure to avoid running large backfills during peak traffic.

### Rollback runbook requirements

Every Phase 2 migration that drops, renames, or backfills data must be
accompanied by a rollback runbook entry in `docs/operations/migration_log.md`
(created at project init time) that states:

- Migration number and name
- Estimated runtime (from staging)
- Lock impact
- Rollback steps (run down migration or manual reversal)
- Data recovery path if down migration cannot restore data
- Verified backup timestamp before the migration ran

### ADR requirement

Any migration that:

- Removes a column or table
- Changes a type in a way that truncates data
- Drops an index used by known query paths
- Adds a non-nullable column without a default

…requires an ADR in `docs/decisions/` before the migration is merged.

---

## Enforcement

The `migration_structure_check.sh` script enforces Phase 1 rules automatically:

- Numeric prefixes, strict ordering, paired up/down files
- First migration named `_init` or `_schema`
- No gaps in prefix sequence
- `0004+` migrations are rejected unless a Phase 2 ADR exists under
  `docs/decisions/`, `docs/adr/`, or `docs/architecture/decisions/`
- Phase 2 migrations require `docs/operations/migration_log.md`

For Phase 2, additional checks should be added as the app matures:

- `CONCURRENTLY` on index creation targeting live tables
- No raw SQL with unbound loops
- Presence of a rollback runbook entry when migrations drop or backfill data

Run via:

```bash
make check-migration-structure
```

---

## Checklist Before Merging Any Migration

- [ ] Migration is paired (up + down) unless explicitly documented
- [ ] Down migration is reversible or rollback is documented
- [ ] Seeds use `ON CONFLICT` for idempotency
- [ ] No external network calls inside the migration
- [ ] Lock impact is estimated for large tables
- [ ] `CONCURRENTLY` used for index creation on live tables
- [ ] Backfill is bounded and restartable if >10K rows
- [ ] Rollback runbook entry added for breaking changes
- [ ] ADR linked if the migration drops, renames, or type-changes a column
- [ ] Backup verified in the deployment window (Phase 2 only)
