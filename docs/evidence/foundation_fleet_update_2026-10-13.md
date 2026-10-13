# Foundation Fleet Update: October 13, 2026

NOTE: A subsequent repair resolved the validation failures recorded below.
All three requested fleet checks now pass in twelve projects.
See [the repair evidence](foundation_fleet_repair_2026-10-13.md).

## Result

All twelve local Foundation projects received the reviewed Core changes.
The original eleven projects passed scaffold validation and the updater repeat check.
Metered received the update and passed the repeat check, but its application contracts remain incomplete.
Metered now appears in `../scaffolded-projects.tsv` for future fleet updates.

No production deployment, database migration, or live index removal occurred.
The updater used `--skip-deps` and did not force replacement of customized files.
Selected file parity passed across all twelve projects.
Configuration tests passed across all twelve projects.
Startup and worker compilation passed in eight projects.
River policy checks passed in eleven projects.

## Changes adopted

1. Distribute the profiler through the scaffold manifest for every profile.
2. Add retention settings to supported application configuration loaders.
3. Apply tier defaults to River clients while preserving explicit retention periods.
4. Preserve Pronto's existing three-hour, six-hour, and twelve-hour client periods.
5. Add documented retention variables to environment examples.
6. Register profiler, retention migration, and queue index tests in Core checks.
7. Replace unconditional diagnostic thresholds with workload-based review requirements.
8. Preserve `--skip-deps` during the updater repeat pass.

The queue index gate supports multiline and schema-qualified SQL definitions.
It preserves exact legacy definitions in recognized baselines and rollback migrations with visible warnings.
New forward migrations still fail when they introduce queue GIN indexes.
This compatibility path preserves migration history without asserting that live indexes are necessary.

Inspection has explicit file, byte, and client-time limits.
Oversized or inaccessible SQL inventories fail explicitly.
The fleet includes a fourteen-megabyte geography seed, which fits the configured inspection budget.

## Validation by project

| Project | Scaffold | Repeat check | Config tests | Startup and worker compilation | River policy |
| --- | --- | --- | --- | --- | --- |
| Chowdash | Passed | Passed | Passed | Passed | Passed |
| Civic | Passed | Passed | Passed | Failed | Failed |
| Docuos | Passed | Passed | Passed | Passed | Passed |
| Forest | Passed | Passed | Passed | Passed | Passed |
| Global | Passed | Passed | Passed | Failed | Passed |
| Marketer | Passed | Passed | Passed | Failed | Passed |
| Ovasabi | Passed | Passed | Passed | Passed | Passed |
| Pronto | Passed | Passed | Passed | Passed | Passed |
| Reframe | Passed | Passed | Passed | Passed | Passed |
| Trader | Passed | Passed | Passed | Failed | Passed |
| Trotters | Passed | Passed | Passed | Passed | Passed |
| Metered | Failed | Passed | Passed | Passed | Passed |

Selected parity checks covered retention, WhatsApp inbox, Hermes store, shared arena, and the profiler.
The migration regression tests cover idempotence, custom period preservation, refusal, and configuration compilation.
Core checks include ten profiler tests, five retention tests, and four queue index tests.
Fresh scaffold idempotence, updater regression tests, manifest checks, and enforcement integrity passed.
The earlier review contains Go, race, TypeScript, and render test evidence.

## Application gaps

- Civic: Duplicate bootstrap declarations and missing `Geo` fields prevent compilation.
  Two recurring job definitions omit explicit `ByState`, which fails the existing policy gate.
- Global: Bootstrap handlers reference missing `Wallet`, `Compliance`, and `Exchange` fields.
- Marketer: Startup references missing `bootstrap.NewServices` and `bootstrap.RegisterTypedPlanes` functions.
- Trader: Bootstrap handlers reference a missing `Market` field.
- Metered: Typed protobuf handlers are empty, and the ledger service lacks its domain protobuf contract.

These application definitions were preserved.
The updater did not invent service behavior or replace custom bootstrap implementations.
Historical queue indexes remain migration debt pending actual workload and query-plan review.
Customized PostgreSQL settings remain application-owned and require host-specific measurements.

## Evidence paths

Core retains detailed evidence under `test-results/foundation-update-20261013/`.

- `final/fleet.json`: Results for the eleven originally indexed projects.
- `metered/metered_v1.json`: Separate Metered synchronization and validation result.
- `adoption-final.json`: Configuration, compilation, River policy, and selected parity results.
- `policy-final.json`: Final checks through each project's installed River tooling.
- `*.compile-final.log`: Application compilation diagnostics.
- `*.river-final.log`: Queue policy results and legacy migration warnings.
- `*.before-status.txt`: Existing changes recorded before synchronization.
- `fleet-index.before.tsv`: Registry content before Metered registration.
- `core-integrity-final.log`: Protected tooling integrity result.

Compilation checks used `go test ./internal/startup ./cmd/worker -run '^$' -count=1`.
This command verifies compilation without executing application integration tests.
Configuration checks executed `go test ./internal/config -count=1`.

## Definition of done

- Contracts: Add retention configuration, managed diagnostics, and migration-aware policy checks.
  No application API or event schema changed.
- Invariants: Preserve explicit retention, customized files, migration history, and bounded diagnostic work.
- Evidence: Retain the regression tests, fleet reports, parity results, and compilation diagnostics.
- Fallback: Unsupported composition shapes fail before migration writes.
  Existing explicit periods remain authoritative unless an environment override is provided.
- Boundary: Core owns tooling and templates. Applications own their domain and bootstrap definitions.
- Guards: Add tests for migration idempotence, custom settings, unsupported clients, and legacy SQL compatibility.
- Documentation: Update diagnostic guidance and retain this fleet handoff.

Live database, provider, device, and production behavior remain unverified.
