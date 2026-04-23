# Standalone Apps Migration Structure (Active Development)

Status: baseline
Date: 2026-04-20

## Objective

Keep early-stage architecture deterministic and low-friction by using a fixed migration group model until schema freeze.

## Required active structure

1. `0001_schema` - full current schema + indexes + constraints
2. `0002_seed_system_data` - deterministic baseline/system seed data
3. `0003_seed_demo_data` - deterministic demo/test seed data

Each group must have `up` and `down` files.

## Rules

1. During active development, update existing migration groups directly.
2. Do not create `0004+` migration groups without ADR.
3. Keep demo seeds isolated from system seeds.
4. Make seeds idempotent with `ON CONFLICT` behavior.
5. Ensure down scripts remove only records introduced by matching up script.
6. In higher environments (staging/prod), verified snapshot backups MUST be confirmed or taken immediately BEFORE executing structural migrations to mitigate data loss risk.
7. Production application identities must not own schema objects or execute migrations. Use a separate migrator role or pipeline identity.
8. System/demo seed files must never contain reusable real secrets, long-lived tokens, or default production credentials.
9. Data backfills affecting auth, billing, permissions, or destructive state must be restartable/idempotent and emit audit-friendly logs.
10. `0003_seed_demo_data` must stay synthetic and must not run in staging/production unless an explicit environment policy says otherwise.

## Transition rule

Move from fixed active structure to incremental migration stream only after:

1. v1 schema freeze is declared.
2. Backward-compatible production rollout sequencing is required.
3. ADR documents the transition policy.
