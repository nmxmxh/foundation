# Sweeper Wiring and Fleet Refresh

Date: 2026-10-14
Status: fixed and locally validated across the registered fleet.
Scope: Application-owned sweeper wiring in Chowdash and Trotters, followed by the registered Foundation fleet update.

## Correction

`MirrorSweeper.Run` returns an error whenever it stops.
It rejects an empty source registry and otherwise returns when its context ends.
The caller's `err != nil` condition is therefore always true, which Staticcheck reports as `SA4023`.
Both applications now assign the returned error before checking the context.
The context condition remains unchanged, so normal shutdown remains silent.
Unexpected termination still uses each application's existing log level.

The two files are project-owned under the scaffold's `create` mode.
The Core template contains neither affected sweeper invocation.
The fix does not change the shared sweeper, its public contract, source polling, retries, cursors, or tenant boundaries.
Existing Staticcheck checks provide the regression guard for redundant nil comparisons.
The existing sweeper lifecycle tests exercise source polling and cancellation.

## Update and validation

Use the official updater across all twelve registered projects.
Preserve customizations and disable forced replacement and automatic reseeding.
Capture existing file hashes and private backups before changes.
Validate affected application startup suites, shared sweeper lifecycle tests, vet, and Staticcheck.
Then verify scaffold validation, movement guards, source parity, and updater idempotence across the fleet.

Raw results reside in `benchmark-results/sweeper_wiring_fleet_20261014/` under Core.
Private backups reside in `test-results/sweeper-wiring-fleet-20261014/` under Core.
Local validation does not establish production deployment or live persistence behavior.

## Result

Staticcheck and Go vet pass for the affected application packages in both Chowdash and Trotters.
Both applications pass their complete startup, server, and worker race checks.
The shared Hermes sweeper lifecycle tests pass with race detection.
The original conditions, restored only in scratch modules, reproduce `SA4023` in both projects.
The working source hashes remain unchanged during those probes.

All twelve projects pass the official updater, scaffold validation, and built-in repeat checks.
Each copied reference passes the movement guard and complete Hermes/gateway race suites.
Each application passes startup and worker race tests with `GOWORK=off` and read-only manifests.
Every project resolves Hermes from its own Foundation reference and passes installed practice controls.
Chowdash's earlier audience-policy failure remains fixed.

| Project | Official update and scaffold | Repeat check | Movement | Hermes/gateway race | Startup/worker race | Controls |
| --- | --- | --- | --- | --- | --- | --- |
| Chowdash | Passed | Passed | Passed | Passed | Passed | Passed |
| Civic | Passed | Passed | Passed | Passed | Passed | Passed |
| Docuos | Passed | Passed | Passed | Passed | Passed | Passed |
| Forest | Passed | Passed | Passed | Passed | Passed | Passed |
| Global | Passed | Passed | Passed | Passed | Passed | Passed |
| Marketer | Passed | Passed | Passed | Passed | Passed | Passed |
| Ovasabi | Passed | Passed | Passed | Passed | Passed | Passed |
| Pronto | Passed | Passed | Passed | Passed | Passed | Passed |
| Reframe | Passed | Passed | Passed | Passed | Passed | Passed |
| Trader | Passed | Passed | Passed | Passed | Passed | Passed |
| Trotters | Passed | Passed | Passed | Passed | Passed | Passed |
| Metered | Passed | Passed | Passed | Passed | Passed | Passed |

### Preservation and final documentation

Private snapshots record 8,281 existing files outside the copied reference, documentation, and checker directories.
The intentional application changes are the two sweeper call sites and Chowdash's follow-up evidence text.
All other existing application files retain their hashes and modes, apart from synchronization metadata.
The updater uses `--skip-deps`, `--validate`, `--verify-idempotence`, and `--no-auto-reseed`.
No forced replacement, dependency upgrade, seed acknowledgement, migration, reset, commit, or deployment occurs.
Source hashes verify that the fixed application call sites survive synchronization exactly.
Final documentation receives an official docs-only synchronization and a repeated content comparison.

### Definition of done

- Contract: Public interfaces, events, schemas, and shared runtime behavior remain unchanged.
- Invariant: Keep shutdown silent and retain each application's existing error log level for unexpected termination.
- Evidence: Retain Staticcheck, vet, race suites, lifecycle checks, source hashes, and official updater reports.
- Fallback: Preserve existing source retries, cursors, bounded polling, and controlled cancellation.
- Boundary: Application-owned startup files change. Core modules remain unchanged by this correction.
- Regression: Staticcheck rejects the original conditions in isolated scratch modules.
- Documentation: Reconcile the earlier warning record and distribute this completed fleet handoff.

### Records

Durable records reside in `benchmark-results/sweeper_wiring_fleet_20261014/` under Core.

- `fix_validation.json`: Commands and results for both affected applications.
- `chowdash.staticcheck.txt` and `trotters.staticcheck.txt`: Clean Staticcheck runs.
- `*.race.txt` and `*.vet.txt`: Affected application checks.
- `sweeper_lifecycle.txt`: Shared sweeper lifecycle results.
- `regression_probe.json` and `*.staticcheck-mutation.txt`: Rejected original conditions and unchanged source hashes.
- `fleet.json` and `updater_reports.json`: Official synchronization, validation, and repeat results.
- `adoption.json`: Per-project checks, source parity, dependency paths, and preservation results.
- `final_parity.json` and `final_document_idempotence.json`: Final copied sources and documentation comparisons.
- `metadata.json`: Toolchain, revisions, application source hashes, and evidence scope.

### Reproduction

From either affected application:

```sh
GOWORK=off staticcheck ./internal/startup
GOWORK=off go vet ./internal/startup ./cmd/server ./cmd/worker
GOWORK=off go test -mod=readonly -race -p=2 ./internal/startup ./cmd/server ./cmd/worker -count=1 -timeout=180s
```

From Core:

```sh
GOWORK=off go -C server-kit/go test -race ./hermes -run 'TestNotifyCoalescesSignalsIntoOneSweep|TestMirrorSweeper|TestScopeSignal' -count=1 -timeout=120s
bash scripts/update-all.sh --skip-deps --validate --verify-idempotence --no-auto-reseed --report-dir /tmp/sweeper-fleet-refresh
```

Validation uses explicit process deadlines and at most two projects concurrently.
Concurrent benchmark timings are diagnostic and do not support latency claims.

