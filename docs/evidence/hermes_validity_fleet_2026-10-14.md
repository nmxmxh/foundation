# Hermes Validity Fleet Adoption

Date: 2026-10-14
Status: locally adopted and validated, with one confirmed application baseline failure.
Scope: Twelve projects in the local Foundation registry.

Follow-up: Chowdash rectified its audience declarations locally after this fleet snapshot on 2026-10-14.
Its full startup race suite now passes. The original failure below remains historical evidence.
The application record is `chowdash_rider_v1/docs/evidence/audience_policy_2026-10-14.md`, relative to the workspace parent.

## Change and significance

Numeric constructors accumulate validity in bounded 64-row words, then store each nonempty word once.
The [Core experiment](hermes_validity_words_2026-10-14.md) records the complete construction, selection, merge, and reduction path.
At 10,000 rows, bitmap reads decrease from 152,792 to 8,792 logical bytes per operation.
Bitmap writes decrease from 150,280 to 8,792 logical bytes per operation.
Together, these counters decrease about 94%, or 285,488 logical bytes per operation.

This is a substantial reduction in the measured bitmap work.
It does not establish a comparable application speedup, physical bandwidth reduction, or energy saving.
Ordinary timing shows no significant full-path latency change, and allocations remain unchanged.
Parsing, sorting, candidate selection, and other numeric accesses still contribute to the request cost.

## Method and acceptance

Use the official updater with `--skip-deps`, `--validate`, `--verify-idempotence`, and `--no-auto-reseed`.
Do not force replacement or acknowledge seed drift.
Preserve existing customizations with private snapshots and compare their hashes after synchronization.
Validate each adopted reference through ordinary and tagged Hermes tests, race checks, and the movement guard.
Compile each application startup and worker with read-only module manifests and `GOWORK=off`.
Verify source and documentation parity against Core.
Record actual second-pass content stability as well as the updater report.

Private snapshots reside in `test-results/hermes-validity-fleet-20261014/` under Core.
Durable reports reside in `benchmark-results/hermes_validity_fleet_20261014/` under Core.
These results concern local source adoption. Production deployment and live persistence require separate evidence.

## Documentation cleanup

The runtime capture directory exists. The old evidence checker incorrectly required it to be a regular file.
The checker now accepts existing files and directories, and checks complete nested paths.
Eight regression cases cover valid and missing evidence, including nested files and prefix references.
The browser replay command now writes a temporary report instead of implying a missing historical artifact.
The dated browser evidence remains unchanged.

## Result

All twelve registered projects received the reviewed constructors, tests, stricter movement ceilings, checker fix, and documentation.
Every updater report records successful scaffold validation and idempotence.
Every adopted movement guard audits 90 functions, requires 13 movement tests, and enforces all 38 exact ceilings.
Each reference reports 8,792 full-path bitmap read bytes and 8,792 full-path bitmap write bytes.
The resident query counts remain unchanged.
All twelve Hermes and projection gateway suites pass with race detection.
Every application resolves Hermes from its own updated Foundation reference with `GOWORK=off`.

| Project | Update and scaffold | Repeat and parity | Hermes/gateway race | Startup/worker race |
| --- | --- | --- | --- | --- |
| Chowdash | Passed | Passed | Passed | Existing audience failure; worker passed |
| Civic | Passed | Passed | Passed | Passed |
| Docuos | Passed | Passed | Passed | Passed |
| Forest | Passed | Passed | Passed | Passed |
| Global | Passed | Passed | Passed | Passed |
| Marketer | Passed | Passed | Passed | Passed |
| Ovasabi | Passed | Passed | Passed | Passed |
| Pronto | Passed | Passed | Passed | Passed |
| Reframe | Passed | Passed | Passed | Passed |
| Trader | Passed | Passed | Passed | Passed |
| Trotters | Passed | Passed | Passed | Passed |
| Metered | Passed | Passed | Passed | Passed |

All application startup and worker packages compile with read-only manifests.
Eleven complete startup/worker race suites pass.
Chowdash's worker suite passes; its startup suite has the existing audience failure described below.
Installed practice controls pass in all twelve projects.

### Preservation and idempotence

Before synchronization, private archives preserved existing application changes and Git patches.
Hash and mode checks cover 8,244 existing files outside the managed reference, documentation, and checker directories.
In ten projects, only `.foundation` synchronization metadata changed within that set.
Pronto has five application edits from the concurrent chat, `Implement SEO nginx changes`.
That chat is implementing its separate request to use Hermes for translation caching.
Those edits remain intact. They are not updater transformations.
Metered received the existing managed patch that adds its local runtime-SDK requirement to `go.mod`.
The patch uses version `v0.0.0` and the existing local replacement. Its patch ledger records the change.
No other existing application file changed in the preservation comparison.
No dependency resolution, forced replacement, seed acknowledgement, or automatic reseeding occurred.

After the updater's built-in repeat checks, a separate full update ran again with scaffold validation.
All updater-owned output content remains identical across that pass, excluding volatile `.foundation` metadata.
Eleven projects also retain identical complete source trees.
Pronto's concurrent translation work changes `internal/translation/hermes.go` and `internal/translation/hermes_test.go` during the comparison.
Those application edits are recorded separately; no updater-owned content changes.
This comparison covers tracked files and visible untracked source files, including copied modules, documentation, and checks.
Final documentation receives an additional official synchronization and content comparison.
Private archives remain excluded from version control.

### Existing Chowdash failure

`TestEveryFrontendBoundScopeDeclaresAnAudience` rejects four frontend scopes without declared audience policies:

- `logistics_runtime/observations`
- `logistics_runtime/stops`
- `logistics_runtime/unserved_stops`
- `message/ops_conversations`

A temporary module file redirects only the Foundation dependency to the preserved pre-improvement source.
The same test produces the same four failures against that baseline.
The application's relevant files retain their original hashes.
This run preserves the application's authorization policy and records the failure instead of suppressing its test.
The broader startup suite therefore remains red for this separate application issue.

### Validation harness correction

The initial harness invoked the practice script with Bash instead of its declared Zsh interpreter.
That invocation rejected associative-array syntax before checking any project controls.
The corrected harness executes the installed script directly.
All twelve corrected control checks pass. Initial errors remain available beside the corrected evidence.

### Definition of done

- Contract: Public APIs, event schemas, vector layouts, and planner decisions remain unchanged.
- Invariants: Preserve nulls, parsing errors, order, tenant scope, bounded work, fallback behavior, and owned vectors.
- Evidence: Retain updater reports, race results, movement counts, dependency paths, preservation hashes, and repeat comparisons.
- Fallback: Keep ordinary builds, controlled parse errors, and existing query and projection fallback paths.
- Boundary: Core owns the copied modules and checks. Applications retain their services, policies, manifests, and customizations.
- Regression: Adopt the lower ceilings, numeric boundary tests, mutation guard, source audit, and artifact-check fixtures.
- Documentation: Reconcile current Hermes results, historical movement records, benchmark references, and this adoption report.

These checks establish local library and application integration.
They do not establish deployment, live database behavior, browser journeys, device performance, or physical traffic savings.
Concurrent fleet benchmark timings are diagnostic and cannot support latency claims.

### Durable records

Records reside in `benchmark-results/hermes_validity_fleet_20261014/` under Core.

- `fleet.json`: Initial official updater results.
- `fleet_repeat.json`: Independent complete update results.
- `adoption.json`: Final per-project checks, preservation summaries, and baseline failure classification.
- `content_idempotence.json`: Content comparisons across the independent complete update.
- `final_parity.json`: Final source, checker, and documentation hashes against Core.
- `final_document_idempotence.json`: Content comparison across the final documentation repeat.
- `*.movement.txt`: Actual counters, tests, and source audits from each copied reference.
- `*.hermes-race.txt`: Complete Hermes and gateway race suites.
- `*.application-race.txt`: Application startup and worker race suites.
- `chowdash.baseline-audience.txt`: The reproduced application failure against the previous Foundation source.
- `core_checks.txt`: Core controls and documentation checks, including the artifact-check fixtures.
- `metadata.json`: Starting revisions, toolchain, methods, artifact hashes, and scope.

### Reproduction

From Core, use the official fleet entrypoint:

```sh
bash scripts/update-all.sh --skip-deps --validate --verify-idempotence --no-auto-reseed --report-dir /tmp/hermes-validity-fleet
```

From each project, execute the adopted full-path and integration checks:

```sh
bash foundation/tooling/scripts/benchmark_movement_check.sh "$PWD/foundation"
GOWORK=off go -C foundation/server-kit/go test -mod=readonly -race -p=2 ./hermes ./projectiongw -count=1 -timeout=180s
GOWORK=off go test -mod=readonly -race -p=2 ./internal/startup ./cmd/worker -count=1 -timeout=120s
GOWORK=off go list -mod=readonly -f='{{.Dir}}' github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes
scripts/checks/practice_controls_check.sh "$PWD"
```

Outer process limits supplement package deadlines.
Validation uses at most two projects concurrently.

