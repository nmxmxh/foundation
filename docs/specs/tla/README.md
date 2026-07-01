# Foundation TLA Specs

Status: model-checked
Owner: Platform Architecture

These modules encode Foundation architecture invariants as small, **runnable**
TLA+ specs. Every spec ships a finite model configuration that TLC checks, and a
negative control that proves each invariant can actually fail. Use them with the
guidance in `../../tla_architecture_practices.md` and the mathematical grounding
in `../../mathematical_practices.md`.

They are the "tiny mathematical invariants that hold giant systems together"
from `../info/coding_magic.md`, made executable: name the visible state, name
the hidden state, then let the model checker prove the forbidden states cannot
occur.

## The specs

| Spec | Encodes | Key invariants | Math grounding |
| --- | --- | --- | --- |
| `WorkerRetryQueue` | bounded queue, retry budget, leases, terminal states | `QueueBounded`, `RetryBudgetBounded` | bounded work (§6) |
| `CacheProjectionFreshness` | read-your-write, bounded-stale reads, watermarks, repair | `ProjectionVersionMonotonic` | monotone watermark / G-Counter (§5, CRDT-4) |
| `WebSocketBackpressure` | authenticated subs, bounded write queues, slow-client close | `WriteQueueBounded`, `TopicAuthorized`, `DisconnectCleansState` | bounded work (§6) |
| `FrontendLiveProjection` | frontend store: loading buffer, live queue, tenant scope, version monotonicity | `LiveQueueBounded`, `TenantScopeStable`, `VersionMonotone` (step property) | monotone version |
| `HermesProjectionPublish` | lock-free single-writer projection publish (implementation-mapped) | `TearFreeRead`, `VersionWatermarkConsistent` | — |
| `MetadataMerge` | CRDT metadata convergence under reordered/duplicate delivery | `StrongEventualConsistency` | join-semilattice / SEC (§5) |

## Runnable model instances

Each spec that declares `CONSTANTS` has a finite model instance `MC<Name>.tla`
(constant operators + `VARIABLES` binding + plain `INSTANCE`) and an
`MC<Name>.cfg`. The monolithic `HermesProjectionPublish` uses a bare
`HermesProjectionPublish.cfg`. Run the whole set through the gate:

```bash
FORMAL_RUN_TLC=1 TLA_TOOLS_JAR=/path/to/tla2tools.jar make check-formal-methods
```

TLC runs from this directory (so an `INSTANCE` resolves its sibling base module)
with a throwaway `-metadir`, so its `states/` scratch never lands in `docs/`
(which is copied verbatim into generated apps). `-deadlock` is passed because
terminal states (all jobs done, all connections closed) are expected in these
safety models, not errors.

## Two specs worth calling out

**`HermesProjectionPublish` is implementation-mapped.** `snapshot` models the
immutable value behind `atomic.Pointer[recordEntry]` / `atomic.Pointer[indexSnapshot]`;
`epoch`/`watermark` model the partition atomics in `store.go`. Its `TearFreeRead`
invariant is the same coupled-field consistency the Go test
`TestStoreConcurrentPatchReadsNeverObserveTornArchiveState` asserts at runtime —
so the design (TLC), the runtime (`go test -race`), and the SAB ring (Rust
`loom`) triangulate on one property.

**`MetadataMerge` is the CRDT convergence proof.** It encodes the Shapiro et al.
(2011) result from `mathematical_practices.md` §5: a merge that forms a
join-semilattice (commutative, associative, idempotent) achieves Strong Eventual
Consistency. The model has two replicas receive an arbitrary, reordered,
duplicated subset of a shared update universe; `StrongEventualConsistency` proves
that once they have delivered the same set, their state is identical.

## Negative controls (proving invariants have teeth)

Every spec ships an `MC<Name>Broken.tla` (or `HermesProjectionPublishBroken.tla`)
that reuses the real spec via `INSTANCE` and injects exactly one bad transition,
so a named invariant **must** be violated:

| Negative control | Injected bug | Invariant it breaks |
| --- | --- | --- |
| `MCWorkerRetryQueueBroken` | retry ignores the attempt budget | `RetryBudgetBounded` |
| `MCCacheProjectionFreshnessBroken` | refresh stamps a version past the watermark | `ProjectionVersionMonotonic` |
| `MCWebSocketBackpressureBroken` | enqueue skips the write-queue cap | `WriteQueueBounded` |
| `MCFrontendLiveProjectionBroken` | live enqueue skips the `MaxQueued` cap | `LiveQueueBounded` |
| `HermesProjectionPublishBroken` | publish decouples `bucket` from `status` | `TearFreeRead` |
| `MCMetadataMergeBroken` | non-idempotent counting fold (counts duplicates) | `StrongEventualConsistency` |

The gate runs positives and negatives differently: a positive `*.cfg` must pass;
a `*Broken*` config must FAIL with an invariant violation. A negative control
that passes is itself reported as a failure — the invariant would be vacuous.
This is the model-checking analogue of a mutation test, and it mirrors the
runtime negative controls used under `go test -race` and Rust `loom`.

## Confirming the code (spec → implementation)

TLC proves the *design* is sound; it does **not** read your Go/Rust/TS. To tie
each invariant to the real implementation, [`conformance.tsv`](conformance.tsv)
maps every `(spec, invariant)` to the concrete anchors that confirm it — the TLC
model **and** at least one real-code anchor — and `spec_conformance_check.sh`
(`make check-spec-conformance`) verifies every anchor exists. A renamed or
deleted test breaks the build, so the spec↔code link is an enforced reference,
not a comment.

The anchors span all three verification levels:

| Level | Anchor kind | Example |
| --- | --- | --- |
| **1. Enforced mapping** | source symbol that enforces the invariant | `observeSourceWatermark` (hermes), `exceeds max_attempts` (worker `Job.Validate`) |
| **2. Conformance property test** | test asserting the invariant on real code | `TestMergeMapsACILaws` (metadata ACI), `TestConformanceWorkerRetryBudgetBounded` |
| **3. Trace / service-backed** | real execution recorded and checked against the invariant | `TestConformanceTraceWorkerRetryQueue`, `TestConformanceServiceBackedHermesProjectionMonotonic` (real Postgres/Redis) |

The strongest chains are `MetadataMerge` (TLC proves ACI ⇒ SEC; `TestMergeMapsACILaws`
proves the real `MergeMaps` **is** ACI) and `HermesProjectionPublish` (TLC
`TearFreeRead` ↔ the Go concurrent test under `-race` ↔ Rust `loom` on the ring).

Service-backed anchors carry `//go:build servicebacked` and run under
`make test-service-backed` against live Postgres/Redis; the default unit build
covers the property and trace tests.
