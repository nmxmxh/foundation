# Hermes Numeric Validity Experiment

Date: 2026-10-14
Status: implemented and locally validated
Scope: Foundation Core numeric column construction.

## Evidence and hypothesis

The [movement record](../info/movement_accounting_20261013.md) measures a complete 10,000-row construction and selection operation.
Construction performs 18,000 validity updates for two numeric columns.
Those updates account for 144,000 bitmap read bytes and 144,000 bitmap write bytes before selection.
Each update changes one bit in a word that holds 64 rows.

Hypothesis: accumulate each validity word locally, then store it once per numeric column.
This should remove construction reads of existing validity words and reduce explicit bitmap writes.
The change must preserve nulls, inferred types, numeric parsing, order, tenant scope, fences, and returned vector ownership.

The current evidence covers one Go FFI adapter and two Hermes workloads.
It cannot calibrate general planner thresholds or justify a new persistent cache.
This experiment targets the observed construction work directly.

## Acceptance gates

1. Preserve public results and controlled errors across numeric kinds, missing fields, mixed kinds, and malformed values.
2. Preserve zero tail bits across empty, partial, and complete bitmap words.
3. Exercise the public construction, predicate, merge, and selected-reduction path.
4. Demonstrate lower observed construction movement with unchanged resident-query counts.
5. Compare warm ordinary builds with alternating, equal benchmark samples.
6. Preserve allocation ceilings and reject a material full-path latency regression.
7. Exercise tenant, fence, cancellation, ordering, and race behavior through the existing package suites.
8. Add regression evidence and refresh source coverage only after reviewing the changed operations.

For this experiment, a full-path median regression above 5% at 10,000 rows requires investigation before promotion.
Retain smaller scale results and sample ranges without treating short-run timing noise as a performance claim.
Skip stores for all-null word groups, since newly allocated masks already contain zero.

The initial baseline includes earlier uncommitted accounting changes.
An independent source snapshot preserves those exact module files before this experiment.
Evidence resides in `benchmark-results/hermes_validity_20261014/`.

## Result

Numeric constructors now accumulate validity in a local `uint64` for each bounded group of 64 rows.
Each constructor stores a nonempty word once in its new vector.
This removes repeated bitmap reads and stores without retaining shared state.

### Measured movement

The diagnostic build reports these exact logical byte counts per complete 10,000-row operation.

| Metric | Before | After |
| --- | ---: | ---: |
| Bitmap reads | 152,792 | 8,792 |
| Bitmap writes | 150,280 | 8,792 |
| Numeric reads | 175,968 | 175,968 |
| Numeric writes | 144,000 | 144,000 |
| Parse input | 55,822 | 55,822 |
| Explicit copies | 0 | 0 |

Construction bitmap reads fall from 144,000 bytes to zero.
Construction bitmap writes fall from 144,000 bytes to 2,512.
Resident selection retains 8,792 bitmap read bytes and 6,280 bitmap write bytes.
Candidate counts, field probes, comparisons, materialized rows, and unobserved kernel calls remain unchanged.
These counters describe audited source operations, not physical DRAM traffic, compiler instructions, or energy consumption.

### Ordinary timing

Fresh ordinary binaries ran one discarded warm pair, then eight equal pairs with alternating order.
Each sample used 300 iterations on Go 1.26.6, darwin/arm64, Apple M1 Pro.
CGO was enabled. `GOWORK`, `GOFLAGS`, and `GOEXPERIMENT` were disabled or empty.
The baseline preserves earlier uncommitted movement instrumentation; it does not compare against an unrelated clean commit.

| Public full-path rows | Before median | After median |
| --- | ---: | ---: |
| 1 | 897.25 ns | 873.8 ns |
| 64 | 14.934 µs | 14.753 µs |
| 65 | 14.882 µs | 14.560 µs |
| 1,024 | 323.460 µs | 320.256 µs |
| 10,000 | 4.479 ms | 4.499 ms |

The 10,000-row median increases 0.46%, within the predefined 5% investigation threshold.
Its sample ranges overlap: 4.185–4.685 ms before and 4.023–4.710 ms after.
The existing bitmap-filter benchmark changes from 4.667 ms to 4.711 ms, also within noise.
All five full-path scales retain 11 allocations per operation and unchanged allocation bytes within measurement precision.
Assembly benchmarks cover unlimited reads, limit 50, and predicates with limit 50 at 128 and 10,000 rows.
Their largest median increase is 0.48%.

Benchstat finds no significant full-path latency change.
The 128-row unlimited assembly sample improves 1.15%; this limited result does not establish a general speedup.
The justification is lower observed construction movement with preserved behavior and no demonstrated material runtime regression.

### Contract and regression checks

Public tests cover dense, sparse, mixed-kind, absent, and all-null word groups.
They cover 0, 1, 63, 64, 65, 127, 128, 129, and 1,024 rows.
They verify canonical order, exact float bits, null counts, zero tail bits, selection merging, and selected reduction.
Malformed integer, unsigned overflow, and malformed float values fail after one complete word, without returning partial batches.
Ownership tests mutate one returned vector and verify independent batches remain unchanged.

The complete Hermes and projection gateway suites pass, including their ordinary race checks.
Tagged Hermes tests and movement race checks also pass.
Both new production constructors have 100% statement coverage.
Ordinary Hermes coverage improves from 85.9% to 86.0%; tagged coverage reaches 86.1%.
Projection gateway coverage is 95.7%.

Ordinary and tagged `go vet` checks pass.
Targeted Staticcheck passes for Hermes and the projection gateway.
Core validation, coding, practice controls, concurrency, and runtime performance checks pass.
Allocation and movement ratchets pass together, including unchanged Go FFI host budgets.
Documentation references, test naming, agent contracts, and enforcement integrity checks pass.

The initial benchmark evidence check reported two missing artifacts:
`benchmark-results/browser_abi_report.json` and `benchmark-results/runtime_layers_20260924/`.
Those initial references matched `HEAD` in `docs/foundation_benchmarks.md`.
The [fleet follow-up](hermes_validity_fleet_2026-10-14.md) resolves the checker error and replay output reference.
The captures were present; the old check incorrectly treated their directory as a missing file.

The guard lowers both full-path bitmap ceilings to 8,792 bytes.
It adds word-construction tests and audits both new constructors plus their fixture.
No movement ceiling increases. The guard now audits 90 functions and requires 13 movement tests.

A scratch mutation restores the original per-row construction while preserving the new functional tests.
Those output tests pass. The source audit rejects the changed constructor.
After refreshing only the scratch audit, the measured guard still rejects the mutation.
For 129 dense rows, it observes 2,064 construction read and write bytes instead of zero reads and 48 writes.
This demonstrates that unchanged outputs and allocations cannot conceal this regression.
The working implementation remains unchanged by the mutation.

### Scope and fallback

Public contracts, schemas, vector layouts, planner decisions, and Hermes query rules remain unchanged.
The invariant is identical typed values, null semantics, errors, order, tenant scope, fences, cancellation, and vector ownership.
Only Foundation Core numeric column construction and its evidence controls change.
Other column types retain their existing construction paths.
Existing parse errors and ordered-read fallback paths remain intact.
Rollback requires restoring the previous constructors and explicitly reviewing the resulting movement increase.

This evidence is local and synthetic. It does not establish deployed behavior or hardware traffic savings.
The earlier movement fleet record predates this improvement.
The [Hermes fleet follow-up](hermes_validity_fleet_2026-10-14.md) records subsequent application adoption.

### Reproduction and artifacts

Artifacts reside in `benchmark-results/hermes_validity_20261014/`.
`baseline_metadata.json` records the starting commit and source hashes.
`before_columnar.go` preserves the original construction code.
For an ordinary baseline binary, copy the candidate module tree and restore that file as `hermes/columnar.go`.
Keep the same benchmark file and compile both binaries with `GOWORK=off`.
`comparison_runs.txt`, `before_ordinary.txt`, `after_ordinary.txt`, `ordinary_summary.json`, and `benchstat.txt` retain every timing sample.
`movement_candidate.txt` retains the diagnostic counters.
`mutant_output_tests.txt`, `mutant_audit.txt`, and `mutant_movement_guard.txt` retain the deliberate regression results.

Run these commands from Core, except the stated module commands.
All package tests use explicit deadlines. Guard wrappers provide bounded execution.

```sh
go -C server-kit/go test ./hermes ./projectiongw -coverprofile=/tmp/hermes-validity-final.cover -timeout=180s
go -C server-kit/go test -tags=foundation_movement ./hermes -coverprofile=/tmp/hermes-validity-tagged.cover -timeout=180s
go -C server-kit/go test -race ./hermes ./projectiongw -timeout=180s
go -C server-kit/go test -race -tags=foundation_movement ./hermes -run '^TestMovement' -count=1 -timeout=180s
go -C server-kit/go vet ./hermes ./projectiongw
go -C server-kit/go vet -tags=foundation_movement ./hermes
make check-benchmark-ratchet
make check-core-validation-contract check-coding-practices check-practice-controls check-go-concurrency-practices check-runtime-performance-contracts
make check-doc-references
```

The ordinary comparison selects these benchmarks in both binaries:

```text
^BenchmarkHermes(ColumnarBitmapFilterSum|NumericValidityFullPath|ColumnarAssembly)$
```

Each invocation uses `-test.run=^$ -test.benchtime=300x -test.count=1 -test.benchmem -test.timeout=90s`.
Source audit hashes change only after operation review; protected manifests require explicit refreshes.

