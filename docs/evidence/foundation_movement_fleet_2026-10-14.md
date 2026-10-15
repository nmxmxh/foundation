# Foundation Movement Fleet Update

Date: 2026-10-14
Scope: Twelve registered local Foundation projects.
Core base: `b06f99fafd99e32bae973ec90b7fc056e1ccb324`, with the current working changes.

## Result

All twelve projects received the movement diagnostics, regression tooling, and updated runtime and gaming practices.
The official updater completed scaffold validation and its repeat check in every project.
The copied movement guard passed in every vendored Foundation reference.
Each project also passed standalone startup and worker compilation and the installed practice-control check.

These results establish local source adoption.
They do not establish production deployment, live database behavior, a playable game journey, or physical GPU performance.
The Core-only guard does not become automatic application telemetry or an application benchmark gate.

The [movement evidence](../info/movement_accounting_20261013.md) records the original measurements and runtime confirmation.
The extra input copy was a deliberate mutation in an isolated checkout.
It was never a duplicate in the working runtime.

The [Hermes validity follow-up](hermes_validity_fleet_2026-10-14.md) records the later numeric construction improvement.
The 86-function audit and original movement budgets below describe this earlier rollout.

## Fleet results

| Project | Update and scaffold | Repeat check | Movement guard | Standalone compilation | Practice controls |
| --- | --- | --- | --- | --- | --- |
| Chowdash | Passed | Passed | Passed | Passed | Passed |
| Civic | Passed | Passed | Passed | Passed | Passed |
| Docuos | Passed | Passed | Passed | Passed | Passed |
| Forest | Passed | Passed | Passed | Passed | Passed |
| Global | Passed | Passed | Passed | Passed after reconciliation | Passed |
| Marketer | Passed | Passed | Passed | Passed | Passed |
| Ovasabi | Passed | Passed | Passed | Passed | Passed |
| Pronto | Passed | Passed | Passed | Passed | Passed |
| Reframe | Passed | Passed | Passed | Passed after reconciliation | Passed |
| Trader | Passed | Passed | Passed | Passed | Passed |
| Trotters | Passed | Passed | Passed | Passed | Passed |
| Metered | Passed | Passed | Passed | Passed after reconciliation | Passed |

The registry is `../scaffolded-projects.tsv` relative to Core.
All twelve discovered Foundation projects appear in that registry.
Metered has no Git commit yet. Its existing files received the same preservation snapshot.

## Update method and preservation

The update used the official `scripts/update-all.sh` entrypoint.
Arguments included `--skip-deps`, `--validate`, and `--verify-idempotence`.
No forced replacement or seed-drift acknowledgement occurred.
Customized scaffold files and migration history remained under their existing ownership rules.
Dependency installation and resolution were skipped during synchronization.

Each project had its status, existing changed files, and staged and unstaged patches captured before the update.
Local archives preserve previous file contents and modes.
Managed references were then refreshed from Core through the updater.
Preservation checks distinguish reference replacement, metadata updates, and the application changes described below.

Private snapshots and detailed reports reside in `test-results/foundation-movement-fleet-20261014/` under Core.
The archives contain existing local work and remain excluded from version control.
No stash, reset, checkout, commit, database migration, or production operation occurred.

## Dependency reconciliation

The initial standalone compile passed in nine projects.
Global, Reframe, and Metered requested module updates when tested with `GOWORK=off` and `-mod=readonly`.
Their existing workspace builds could resolve dependencies through the other Foundation modules.
Their root manifests still declared older versions.

Bounded `go mod tidy -diff` commands identified the required changes before writes.
Bounded `GOWORK=off go mod tidy` commands then reconciled those three application manifests.
The selected versions match requirements already present in Core.
Examples include pgx 5.10.0 and River 0.44.1.
This process did not use an unrestricted dependency upgrade.
Metered also received its missing standalone checksum file and a normalized Go version declaration.

All three projects subsequently passed:

- Standalone compilation with read-only manifests.
- Config, startup, worker, and worker-entry package tests with race detection.
- Scaffold validation.
- A final `go mod tidy -diff` with no remaining changes.

Their `go.mod` and `go.sum` files are project-owned under the scaffold contract.
The preservation archives retain their previous contents where those files existed.

## Global startup wiring

The existing managed patch added Global's optional projection-envelope fallback helper and invocation.
The invocation requires the configuration flag, projected store, and Redis client.
`HERMES_ENVELOPE_FALLBACK` remains false by default.
The startup patch ledger records this generated change.

Global's startup tests and the affected application package tests passed with race detection.
Compilation verifies that the generated helper uses supported Foundation interfaces.
This run did not activate the fallback against live Redis.
The helper does not replace canonical database authority or the existing durable worker path.

Other existing application changes retained their contents, apart from normal Foundation metadata updates and the stated manifest reconciliation.

## Adoption evidence

The initial comparison verified 43 selected source, documentation, and control files in each project.
Each comparison used content hashes against the current Core working files.
The movement guard independently audited 86 function declarations and enforced 38 ceilings in every reference.
Its fixtures execute both Hermes paths and the actual Go FFI caller-buffer path.

The final documentation pass distributes this handoff through the official updater.
Final parity includes this document and all 43 original comparison files.
The Core revision alone does not identify the update because these changes are still uncommitted.
The recorded content hashes identify the adopted source.

Durable records reside in `benchmark-results/movement_fleet_20261014/` under Core:

- `fleet.json`: Official updater results.
- `adoption_initial.json`: Initial checks, including the three manifest failures.
- `adoption_final.json`: Final compilation, movement, parity, and control results.
- `final_parity.json`: Content checks after the documentation pass.
- `*.movement.txt`: Actual movement guard output from each vendored reference.
- `*.compile*.txt`: Standalone compilation results.
- `*.application-race.txt`: Affected application test results.
- `*.tidy-diff.txt`: Preview of required manifest reconciliation.
- `metadata.txt`: Commands, versions, source hashes, and evidence scope.

Local reports also include `preflight.json`, `preservation.json`, project snapshots, and the updater logs.
The initial dry run passed across the fleet. It overlapped the start of synchronization and was not the sole safety check.

## Reproduction

Run the managed update from Core:

```sh
bash scripts/update-all.sh --skip-deps --validate --verify-idempotence --report-dir /tmp/foundation-fleet-report
```

Run the adopted guard from a project root:

```sh
bash foundation/tooling/scripts/benchmark_movement_check.sh "$PWD/foundation"
GOWORK=off go test -mod=readonly -p=2 ./internal/startup ./cmd/worker -run '^$' -count=1 -timeout=120s
scripts/checks/practice_controls_check.sh "$PWD"
```

Run affected application tests and verify resolved manifests:

```sh
GOWORK=off go test -mod=readonly -race ./internal/config ./internal/startup ./internal/worker ./cmd/worker -count=1 -timeout=120s
GOWORK=off go mod tidy -diff
```

Commands used explicit process deadlines. Fleet validation limited concurrency to two projects.
Benchmark timings from this concurrent validation are diagnostic and do not support latency claims.

## Definition of done

- Contract: Distribute the reviewed Core diagnostics and practices. Public APIs, event schemas, and runtime layouts retain their contracts.
- Invariants: Preserve project ownership, output lifetime, tenant-safe clearing, bounded work, and controlled fallback.
- Evidence: Retain updater reports, content hashes, movement guards, standalone compilation, and affected package tests.
- Fallback: Preserve ordinary builds, existing transport failures, and default-disabled optional startup behavior.
- Boundary: Core owns the shared modules and checks. Applications own their manifests, custom configuration, and services.
- Regression guards: Run the adopted movement fixtures, installed practice controls, and read-only standalone compile checks.
- Documentation: Distribute this handoff and the updated runtime, gaming, GPU, SAB, Rust, and binding practices.

Provider behavior, live persistence, deployed applications, and target-device performance remain outside this validation scope.
