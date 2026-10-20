# Evidence Ledger: Princeton Companion Mathematics & Minato-Morreale ISOP Adoption

Date: 2026-10-20
Scope: `server-kit/go/money`, `server-kit/go/retry`, `server-kit/go/circuitbreaker`, `server-kit/go/hermes`, `server-kit/go/policy`, `runtime-transport/ts`, `runtime-sdk/rust/crates/ovrt-dispatch`, `tooling/math_lab`, `docs/mathematical_practices.md`, `docs/database_practices.md`, `docs/mesh_dispatch_practices.md`.

## Definition of Done Summary

1. **Public contract changes**: Additive only (`Money.AllocateWide`, `Money.AllocateMonotone`, `retry.JitterMode`, `retry.Budget`, `circuitbreaker.Config` rolling-window and Wilson fields, `Float64Vector.SumCompensated`, `Float64Vector.SumValidCompensated`, `policy.MinatoMorrealeISOP`, `policy.Engine.Analyze`, `ovrt_dispatch::decide_power_of_two`, `WebSocketTransportOptions.reconnect.jitterFactor`). All pre-existing signatures and test expectations are preserved.
2. **Invariants preserved**:
   - Money conservation (`sum(shares) == total`) and Hamilton quota property (`|share - ideal| < 1` minor unit), plus deterministic lowest-index tie-breaking.
   - Retry attempt bounds, context cancellation, and non-retryable error short-circuiting.
   - Circuit breaker fail-closed open state and half-open probe limits.
   - Dispatch fail-closed jurisdiction, capability mask, freshness, and deadline filters.
   - Columnar null-algebra identity substitution (`null -> 0`).
3. **Fallback path**: All existing callers continue to work without configuration changes; `Allocate` preserves 64-bit overflow errors expected by legacy tests while `AllocateWide` provides 128-bit overflow-free intermediate math.
4. **Regression guards**: Added unit, property/fuzz, allocation-budget (`testing.AllocsPerRun`), and benchmark tests across Go, Rust, and TypeScript.

---

## 1. Tier A Production Fixes & Benchmarks

### A1. Money Allocation (`server-kit/go/money`)

- **Fix**: Replaced the non-stable `O(n^2)` swap sort and heap-allocated remainder struct slice in `Money.Allocate` with stack-buffered direct selection (`<= 16` parties on stack) and deterministic lowest-index tie-breaking. Added `AllocateWide` (`math/bits.Mul64`/`Div64` 128-bit intermediate arithmetic) and `AllocateMonotone` (Webster / Sainte-Laguë divisor method, eliminating the Alabama paradox).
- **Benchmark (`BenchmarkMoneyAllocate`, Apple M1 Pro)**:
  - `85.39 ns/op`, `192 B/op`, `1 allocs/op` (pinned by `TestMoneyAllocateAllocsPerRun <= 1` alloc/op; down from 2 allocs/op).
  - Statement coverage: `97.9%` (baseline `96.3%`).

### A2 & A3. Retry Jitter, Lock-Free RNG & Token-Ratio Retry Budget (`server-kit/go/retry`, `runtime-transport/ts`)

- **Fix (Go)**: Replaced mutex-locked `*rand.Rand` in `Policy` with lock-free per-P `math/rand/v2`, added `JitterFull`, `JitterEqual`, and `JitterDecorrelated` modes, and added `retry.Budget` (`RecordRequest`, `AllowRetry`, `BudgetExhaustedError`).
- **Fix (TypeScript)**: Added reconnect backoff jitter (`jitterFactor`, default `0.5` equal jitter in `[0.5*exp, exp]`) to `runtime-transport/ts/src/websocket.ts`.
- **Benchmarks & Simulations (`server-kit/go/retry`)**:
  - `BenchmarkPolicy_CalculateDelay`: `16.73 ns/op`, `0 B/op`, `0 allocs/op`.
  - `BenchmarkPolicy_CalculateDelay_Parallel`: `2.551 ns/op`, `0 B/op`, `0 allocs/op` (no mutex contention across cores).
  - Thundering-herd simulation (`TestJitterModesAndThunderingHerdSpread`, 1000 clients): `JitterFull` reduces peak arrivals per 10ms bucket by `>4x` vs narrow symmetric jitter.
  - Outage amplification simulation (`TestRetryBudgetCapsAmplification`, 200 requests, 4 max attempts, 100% outage): unbudgeted retries issue `800` calls (`4.0x` amplification); `NewBudget(0.1, 0, 5)` caps total calls at `225` (`1.125x` amplification).
  - Statement coverage: `94.3%` (baseline `86.6%`).

### A4. Rate-Based Circuit Breaker with Wilson Score Lower Bound (`server-kit/go/circuitbreaker`)

- **Fix**: Added a 10-bucket fixed-array rolling window (`FailureRateThreshold`, `MinObservations`, `RollingWindow`) and optional 95% Wilson score lower confidence bound (`UseWilsonLowerBound`) to `CircuitBreaker`. Zero extra heap allocations.
- **Verification & Benchmarks**:
  - Under 33% alternating failures (every 3rd request fails), consecutive-5 never trips, whereas `FailureRateThreshold: 0.25` trips deterministically once `MinObservations` is reached.
  - With `UseWilsonLowerBound: true` and threshold `0.20`, a noisy 3/10 sample (30% empirical, Wilson lower bound `0.108`) does not false-trip, while a sustained 40/100 failure rate (Wilson lower bound `0.309`) trips.
  - `BenchmarkCircuitBreaker_Execute_Closed`: `97.62 ns/op`, `0 B/op`, `0 allocs/op`.
  - Statement coverage: `96.8%` (baseline `95.8%`).

### A5. Rust Dispatch Sub-Capacity Load Scoring & Power-of-Two Choices (`runtime-sdk/rust/crates/ovrt-dispatch`)

- **Fix**:
  1. Fixed `DispatchRequest::expected_latency_ns` in `decide.rs`, which previously used integer division `1 + inflight / concurrency` and truncated any sub-capacity load (`inflight < concurrency`) to zero penalty. Replaced with exact 128-bit rational scaling `ewma_ns * (concurrency + inflight) / concurrency`.
  2. Added `decide_power_of_two` (`d = 2` randomized choice seeded deterministically via SplitMix64 from `(tick_now, affinity_key)`) to eliminate Mitzenmacher stale-snapshot herd oscillations.
- **Simulation (`power_of_two_prevents_stale_snapshot_herding`)**:
  - Across 64 arrivals over 8 eligible lanes before snapshot refresh, deterministic `decide` routes all `64/64` requests to lane 0, whereas `decide_power_of_two` keeps peak lane load `<= 28/64` with zero heap allocations.

### A6. Minato-Morreale ISOP Policy Analyzer & Total Object Field Resolution (`server-kit/go/policy`)

- **Fix**:
  1. Implemented `MinatoMorrealeISOP(lower, upper TruthTable)` and `Engine.Analyze()` in `server-kit/go/policy/isop.go`, extracting atomic predicates and mutual-exclusion don't-care sets (`principal.type`, `resource.type`, `action`, `OpEquals`/`OpNotEquals` on identical fields) to compute an irredundant sum-of-products cover and detect redundant and shadowed allow rules.
  2. Replaced recursive `resolveObjectField` in `server-kit/go/policy/policy.go` with an iterative loop bounded at `maxFieldDepth = 32` (CP-01, CP-02, DEC-1).
- **Verification**: `TestMinatoMorrealeISOPAndPolicyAnalyze` verifies irredundancy, don't-care exploitation, shadowed-allow detection, and redundant-rule detection. Statement coverage increased from `78.2%` to `83.8%`.

---

## 2. Numerics & Tier B Practice Additions (`server-kit/go/hermes`, `docs/`)

- **Neumaier Compensated Summation**: Added `Float64Vector.SumCompensated` and `Float64Vector.SumValidCompensated` (`server-kit/go/hermes/columnar_sum.go`, `columnar_reduce.go`).
  - Exact rational oracle results (`TestFloat64VectorSumMatchesExactOracle`):
    - `big-plus-small` (`1e17 + 4096 * 1.0`): 4-way scalar / 8-way SIMD errs by `64 ULP`; Neumaier achieves **`0 ULP`**.
    - `catastrophic-cancellation` (`333 * (1e16, 1.0, -1e16)`): 4-way scalar / 8-way SIMD returns `0` (`4.64e18 ULP` error); Neumaier returns the exact **`333` (`0 ULP`)**.
    - `mixed-magnitude-cancellation`: 4-way scalar / 8-way SIMD errs by `1.23e14 ULP`; Neumaier achieves **`0 ULP`**.
- **Documentation updates**:
  - `docs/mathematical_practices.md`: Added MU-6 (Balinski-Young theorem and Alabama paradox), §3.4 (Benjamini-Hochberg FDR for multi-benchmark gates), §3.5 (Wald SPRT and Wilson lower bound), FP-3 (`SumCompensated` / `SumValidCompensated`), §6.2 (Kingman's heavy-traffic formula), §6.4 (retry budgets, jitter, power-of-two choices), and §6.5 (non-Turing-complete rule engines and Minato-Morreale ISOP).
  - `docs/database_practices.md`: Documented the Braess paradox in queueing networks as the formal foundation for the cache-layering rule.
  - `docs/mesh_dispatch_practices.md`: Updated expected latency formula to exact rational scaling and documented `decide_power_of_two`.

---

## 3. Tier C Experimental Lab Verdicts (`tooling/math_lab`)

Reproduce all experiments with:

```bash
cd tooling/math_lab && go test -v ./...
```

| Experiment | Key Measured Numbers | Verdict | Rationale |
| :--- | :--- | :--- | :--- |
| **C1. Kingman / Lagrange LP Worker Sizing** (`TestC1_KingmanLagrangeScalingEvaluation`) | At 4 cores: `0.000 ms` gap (`[8,4,3]` is exact optimal). At 8–32 cores: heuristic wait is within `0.43–1.05 ms` of discrete convex optimum. | **Keep current `scaling.AutoTuneForCores` heuristics** | Sub-millisecond absolute queueing wait difference does not justify runtime LP solver complexity. |
| **C2. Reed-Solomon GF(2^8) vs Replication** (`TestC2_ReedSolomonVsReplicationDurabilityAndCost`) | `RS(4,2)` cuts storage overhead from `3.00x` to `1.50x` (`-50%`) while tolerating any 2 node failures (`P(loss) = 1.96e-5` at `p = 0.01`). | **Defer until multi-store `objectstore` topology ships** | Current `hermessnapshot` writes to a single configured objectstore backend; adopt `RS(4,2)` when sharding across `>= 6` failure domains. |
| **C3. Expander Gossip vs Hub-and-Spoke** (`TestC3_ExpanderVsHubSpokeDissemination`) | At `N = 512` with 10% dead nodes: 4-regular expander reaches `100%` of live nodes in `5` rounds using `2,048` msgs/round vs `261,632` for full mesh (`128x` fewer messages). | **Defer for `N <= 32`; adopt above `N >= 64` nodes** | Hub-and-spoke is optimal (`2` hops) at current regional scale; random 4-regular expander is the proven fallback when mesh node count exceeds `64`. |
| **C4. Haar Wavelet Spike Compression vs Mean Bucketing** (`TestC4_HaarWaveletSpikePreservationVsMeanBuckets`) | At `8x` compression (`32/256` values kept) on a `310 ms` p99 spike: mean bucketing washes the spike down to `58.1 ms` (`81.2%` error); Haar wavelet top-coeff thresholding reconstructs `309.7 ms` (`0.1%` error). | **Adopt for historical telemetry downsampling** | Whenever metric/SLO series are downsampled for cold storage, retain either `(min, max, sum, count)` tuples or top Haar wavelet coefficients; never average-bucket latency series. |
| **C5. Minato-Morreale ISOP Policy Analysis** (`server-kit/go/policy/isop.go`) | Sub-millisecond exact boolean minimization and dead/redundant rule detection for `<= 16` predicates. | **Adopted in `server-kit/go/policy`** | Shipped directly as `policy.MinatoMorrealeISOP` and `Engine.Analyze()`. |
| **C6. Matroid Greedy Deadline Scheduling** (`TestC6_MatroidGreedySchedulingVsFIFO`) | On 64 weighted deadline jobs: FIFO achieves `1,806` on-time weight, EDF achieves `1,861`, and Rado-Edmonds matroid greedy achieves `2,543` (`+40.8%` over FIFO). | **Defer (no unit-time deadline SLA in River workers yet)** | Current River queues schedule heterogeneous jobs by static priority tier; adopt matroid scheduling if SLA deadline-weighted unit batches are introduced. |
