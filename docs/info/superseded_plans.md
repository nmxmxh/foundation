# Superseded Plans

Status: archive
Owner: Platform Architecture

This file holds planning material that the work it planned has overtaken. The
plans were accurate when written. They are kept because they record the
promotion targets and the intended build order, which explain why a subsystem
has its present shape.

Do not read this file as a statement about current behavior.

---

## From `hermes_hotplane.md`, archived 2026-09-08

The `server-kit/go/hermes` package was complete when this was archived. The
build sequence below finished, and the benchmark list was superseded by the
runs recorded in `../foundation_benchmarks.md` and its archive. Several
benchmarks in the list were never built under these names, because the real
benchmark work took a different shape.

## Benchmark Plan

Phase 0 baseline uses the existing MemoryDB, websocket, worker, chain, and
event-bus benchmarks:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='Benchmark(MemoryDB|Scale_|Scale1M_|ExecCommandMemoryDB|Engine_Enqueue|Job_Normalize|RunParallel)' -benchmem ./database ./appbench ./worker ./chain
```

Phase 1 Hermes-specific benchmarks:

1. `BenchmarkHermesGetView`
2. `BenchmarkHermesGetRecordCopied`
3. `BenchmarkHermesListViewsIntoLimit50`
4. `BenchmarkHermesListRecordsCopiedLimit50`
5. `BenchmarkHermesApplyEventUpsert`
6. `BenchmarkHermesApplyEventDelete`
7. `BenchmarkHermesApplyBatch64`
8. `BenchmarkHermesApplyBatch1024`
9. `BenchmarkHermesApplyRecords64`
10. `BenchmarkHermesBulkLoad512`
11. `BenchmarkHermesApplyRecordPayloads64`
12. `BenchmarkHermesPublishEpoch`
13. `BenchmarkHermesWatchEpoch`
14. `BenchmarkHermesFallbackPostgres`
15. `BenchmarkHermesRedisStreamTail`
16. `BenchmarkHermesSnapshotRebuild100K`
17. `BenchmarkHermesSnapshotRebuild1M`
18. `BenchmarkHermesDriftCheckMerkle`

Promotion targets should be evidence-based, but the first local guardrails are:

1. Internal hot key read: below 1000 ns and zero allocation.
2. Internal indexed list into caller storage: below 10000 ns for limit 50 at
   1M records and zero allocation where payload copies are not requested.
3. Public copied list: no worse than current MemoryDB copy-safe API unless the
   projection stores wider payloads by design.
4. Apply event: below 5000 ns for compact records.
5. Batch apply: amortized event cost below Redis pipelined per-key cost.
6. Epoch publish: below 500 ns.
7. No unbounded heap growth under churn.
8. Race tests pass for projector/read concurrency.

## Implementation Sequence

1. Document projection specs and invariants.
2. Add a `server-kit/go/hermes` experimental package behind tests.
3. Implement partition metadata, epochs, memory accounting, and copied APIs.
4. Add internal borrowed view APIs and benchmarks.
5. Add idempotent apply with version/tombstone rules.
6. Add snapshot rebuild from `database.StateStore`.
7. Add Redis Stream tailer using `redis.Client` and bounded worker ownership.
8. Add binary payload tailing for Cap'n Proto/protobuf/runtime-frame decoders
   without adding JSON to the Hermes transport lane.
9. Add Foundation envelope tailing for generated
   `foundation.v1.RecordMutationBatch` contracts.
10. Add drift checks: count parity, sampled hashes, and partition hash/Merkle
    witnesses.
11. Add integration tests against service-backed Postgres and Redis.
12. Wire selected product-specific read paths only after benchmark and parity
    evidence.

The scaffolded `database.RuntimeStore` wrapper is now mandatory. Promotion of
additional domain-specific read paths still requires proof that Hermes refines
the ordinary Postgres-backed read path under tenant isolation, version ordering,
deletes, staleness, replay, rebuild, Redis loss, and memory pressure.
