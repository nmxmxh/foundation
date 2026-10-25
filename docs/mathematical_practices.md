# Mathematical Practices

Status: baseline
Date: 2026-06-16
Owner: Platform Architecture

## Purpose

This document is the "mathematician in the room" for Foundation. It
translates the relevant numerical-analysis, probability, statistics, and
abstract-algebra results into engineering constraints that bind real code. It
is not a math survey: every rule below maps to an implementation we ship
(`wsmetrics` percentiles, `hermes` SIMD reductions, Redis HyperLogLog, the
security token-bucket limiter, money/minor-unit arithmetic, and CRDT-shaped
metadata merges).

The discipline mirrors `tla_architecture_practices.md`: it does not optimize
code directly. It prevents expensive *wrong* math — silent float drift in
money, under-sampled p99 numbers reported as fact, probabilistic structures
sized by guesswork, and "merge" functions that do not actually converge.

Related docs:

- `foundation/docs/tla_architecture_practices.md`
- `foundation/docs/performance_practices.md`
- `foundation/docs/foundation_benchmarks.md`
- `foundation/docs/coding_practices.md`
- `foundation/docs/gpu_practices.md`
- `foundation/docs/security_practices.md`
- `foundation/docs/testing_practices.md`

## How to read this document

Each domain states (1) the mathematical result, (2) the citation, (3) the
Foundation code it governs, and (4) the enforceable rule. The cross-cutting
control `MATH-01` (see `practice_controls.md`) requires the
`mathematical-practices-checklist` evidence on changes to financial arithmetic,
probabilistic structures, statistical metrics, floating-point reductions, or
CRDT-shaped merges.

Layer separation (inherited from `tla_architecture_practices.md`):

1. **Exact arithmetic** is a correctness property — money, counters, ranks.
   Errors here are bugs, not noise.
2. **Bounded approximation** is a contract — HLL/Bloom error, ULP tolerance,
   percentile confidence. The *bound* is asserted and tested; the exact value
   is allowed to vary within it.
3. **Statistical evidence** is reporting — means, distributions, deltas. Lives
   in `foundation_benchmarks.md`, never in a correctness assertion.

---

## 1. Minor-unit financial arithmetic (exact)

### Result

IEEE 754 binary floating point cannot represent most decimal fractions
exactly: `0.1 + 0.2 != 0.3`, and `$2.78` stored as `float64` is
`2.7799999713897705…`. Binary radix cannot finitely represent `1/10`. The
standard mitigation is the *Money pattern*: store and compute in the smallest
integer minor unit (cents, satang, pence) and format only at the boundary
(Fowler, *Patterns of Enterprise Application Architecture*).

### Rules

- **MU-1 — Integer minor units only.** Monetary amounts are stored and
  transported as integers in the currency's minor unit, paired with an ISO 4217
  currency code and its decimal exponent (most = 2, JPY = 0, KWD/BHD = 3, plus
  the few 3-decimal currencies). Never `float`/`double` for an amount that is
  added, compared, or persisted.
- **MU-2 — Checked arithmetic at boundaries.** Sums and scalings must use
  overflow-checked integer ops (Go: `math/bits` or explicit overflow tests;
  Rust: `checked_add`/`checked_mul`, never wrapping in release). An overflowing
  money operation is a hard error, not a wrap.
- **MU-3 — Rounding is declared, not implicit.** Any division (tax, splits,
  fees, FX) must name its rounding mode. Default to round-half-to-even
  (banker's rounding) to remove the upward bias of round-half-up over many
  transactions; document any deviation at the call site.
- **MU-4 — Allocation conserves the total.** When splitting an amount across
  `n` parties, use the largest-remainder (Hamilton) method: floor each share,
  then distribute leftover minor units one at a time by descending remainder,
  breaking ties by lowest index (`Money.Allocate`, `Money.AllocateWide`).
  Post-condition `sum(shares) == total` and the quota property (`|share − ideal| < 1`
  minor unit) are invariants and must be tested.
- **MU-5 — No cross-currency arithmetic.** Two amounts may only be combined
  after an explicit FX conversion with a recorded rate and rounding step.
- **MU-6 — Apportionment trade-offs (Balinski–Young theorem).** No apportionment
  method with `n ≥ 3` parties satisfies both the quota property and house
  monotonicity. Hamilton (`Allocate`, `AllocateWide`) guarantees quota but can
  exhibit the *Alabama paradox* (increasing the total by 1 minor unit can reduce
  one party's share by 1 minor unit when remainders shift). When monotonic
  incremental streaming is required, call `Money.AllocateMonotone` (Webster /
  Sainte-Laguë divisor method) and document that quota can deviate on skewed
  ratios.

### Citations

- Fowler, "Money" pattern — minor-unit integer storage.
  <https://martinfowler.com/eaaCatalog/money.html>
- Balinski & Young, *Fair Representation: Meeting the Ideal of One Man, One
  Vote*, Yale University Press, 1982 (impossibility of simultaneous quota and
  monotonicity).
- Modern Treasury, "Floats Don't Work For Storing Cents."
  <https://www.moderntreasury.com/journal/floats-dont-work-for-storing-cents>
- IEEE 754-2019; Goldberg, "What Every Computer Scientist Should Know About
  Floating-Point Arithmetic," *ACM Computing Surveys* 23(1), 1991.

---

## 2. Probabilistic data structures (bounded approximation)

We ship Redis HyperLogLog (`server-kit/go/redis/client.go`: `PFAdd`/`PFCount`)
for cardinality. Bloom filters are governed here for any future membership
lane. These structures trade exact answers for sublinear memory; the trade is
only safe if the error bound is **chosen from the formula, not guessed.**

### 2.1 HyperLogLog cardinality

**Result.** For `m` registers, the relative standard error is asymptotically
`σ ≈ 1.04 / √m` (Flajolet, Fusy, Gandouet, Meunier, 2007). Redis uses
`m = 2^14 = 16384` registers ⇒ `σ ≈ 1.04/128 ≈ 0.81%`, in ≤ 12 KB per key.

**Rules.**

- **HLL-1** Pick `m` from the *required* error: `m ≈ (1.04 / σ_target)²`, then
  round up to a power of two. Do not raise register count "to be safe" — memory
  is `O(m)` and error only falls as `√m`.
- **HLL-2** HLL answers are estimates with a stated ±σ band. Never compare two
  HLL counts for equality or use them in exact billing/limits; use them for
  trends, dashboards, and capacity signals only (relates to the layer-2/layer-1
  split above).
- **HLL-3** Unions are free (register-wise max), but **intersections via
  inclusion–exclusion compound error**; avoid HLL set-intersection for anything
  load-bearing.

**Citations.** Flajolet et al., "HyperLogLog: the analysis of a near-optimal
cardinality estimation algorithm," AOFA 2007.
<https://algo.inria.fr/flajolet/Publications/FlFuGaMe07.pdf> · Redis HLL design
(0.81% standard error, 12 KB). <https://thoughtbot.com/blog/hyperloglogs-in-redis>

### 2.2 Bloom filters

**Result.** For `n` elements and `m` bits, the false-positive rate is minimized
at `k = (m/n) ln 2` hash functions, giving `f = (1/2)^k ≈ 0.6185^(m/n)`. To hit
a target FPR `ε`, size `m = −n ln ε / (ln 2)² ≈ −2.081 · n · ln ε` bits. Two
hash functions suffice via `h_i(x) = h1(x) + i·h2(x)` (Kirsch–Mitzenmacher
double hashing) with no asymptotic FPR penalty.

**Rules.**

- **BF-1** Derive `m` and `k` from `(n, ε)` using the formulas above and assert
  them in a test; never hand-tune `k`.
- **BF-2** State the expected `n` (capacity). A Bloom filter loaded past its
  design `n` silently exceeds `ε`; size for the high-water mark or use a
  scalable/counting variant.
- **BF-3** Prefer Kirsch–Mitzenmacher double hashing over `k` independent
  hashes for CPU cost.

**Citations.** Mitzenmacher & Upfal, *Probability and Computing*; Kirsch &
Mitzenmacher, "Less Hashing, Same Performance," ESA 2006.
<https://en.wikipedia.org/wiki/Bloom_filter>

### 2.3 Birthday-bound ID collisions

**Result.** Drawing `r` uniform random IDs from a space of size `N`, the
collision probability is `P ≈ 1 − e^(−r²/2N)`. The 50% threshold is
`r ≈ 1.177 √N`; a "safe" regime keeps `P` tiny, that is, `r ≪ √N`. A 64-bit random
ID reaches ~40% collision risk near `2^32 ≈ 4.3e9` IDs.

**Rules.**

- **ID-1** Choose ID width so that the *lifetime* maximum count `r` satisfies
  `r²/2N ≤ ε_collision` for an explicit `ε_collision` (for example, `1e-9`). For
  high-volume distributed IDs use ≥ 128 bits (UUIDv4/v7: 122 random bits ⇒ 50%
  collision near `2^61.5`).
- **ID-2** 64-bit random IDs are acceptable only for bounded, short-lived,
  per-tenant scopes where `r ≪ 2^32`; document the bound.

**Citation.** Lemire, "Are 64-bit random identifiers free from collision?"
<https://lemire.me/blog/2019/12/12/are-64-bit-random-identifiers-free-from-collision/>

### 2.4 Maplets (approximate key-value mappings over join-semilattices)

**Result.** A maplet is a space-efficient data structure for approximate key-value
mappings governed by a partial order $(V, \preceq)$. When queried for key $k$,
a maplet returns an approximation $m[k]$ of true value $M[k]$ satisfying
$m[k] = M[k]$ with probability $1 - \varepsilon$, and one-sided error
$M[k] \preceq m[k]$. Storing values with $b = \lceil \log_2 |V| \rceil$ bits,
a maplet achieves space bounds of:
$$m \ge n \cdot (\lceil \log_2(1/\varepsilon) \rceil + b) \text{ bits}$$

When values form a join-semilattice $(V, \preceq, \vee)$, internal collisions
between inserted keys resolve via the lattice join ($v_1 \vee v_2$). This
guarantees fail-closed security and prevents false-value downgrade. Unordered
values (system pointers, slot IDs) require wide collision-free fingerprints
or speculative verification against canonical records.

**Rules.**

- **MAPLET-1 (Lattice ordering)** Any micro-value stored in a narrow Maplet
  (slot width $\le 16$ bits) must form a verified join-semilattice where
  collisions evaluate to the upper bound ($v_1 \vee v_2$).
- **MAPLET-2 (One-sided fail-closed invariant)** Maplets used in security,
  jurisdiction, or retention enforcement must strictly fail closed: an
  approximate match may trigger slow-path verification, but must never grant
  elevated privileges or premature deletion.
- **MAPLET-3 (Unordered pointer boundary)** For non-ordered keys (such as Hermes
  slot identifiers or storage offsets), Maplets must either:
  (a) use wide fingerprints sized against the Birthday bound ($N^2 / (2 \cdot B \cdot 2^r) \ll 1$), or
  (b) treat returned values as unverified location hints requiring canonical
      record confirmation before use.
- **MAPLET-4 (Zero-allocation slab layout)** Maplet storage must reside in
  contiguous scalar slabs (`[]uint32`, `[]uint16`, or `[]byte`) marked `noscan`
  by the Go GC, avoiding heap pointers and pointer-scanning overhead.

**Citations.** Bender, Conway, Farach-Colton, Johnson, Pandey, "Time To Replace
Your Filter: How Maplets Simplify System Design", arXiv:2510.05518 (2026).
<https://arxiv.org/abs/2510.05518>

---

## 3. Statistical soundness of metrics

Governs `server-kit/go/wsmetrics/wsmetrics.go` (`calculatePercentiles`,
`percentileIndex`) and every p95/p99 reported in `foundation_benchmarks.md`.

### 3.1 Nearest-rank percentile (the one true definition)

**Result.** Many percentile definitions exist (the nine in Hyndman & Fan,
1996). For latency we use the **nearest-rank** method: the `p`-th percentile is
the value at rank `⌈p/100 · n⌉` (1-indexed). Our code computes the 0-indexed
form `idx = ⌈n·p/100⌉ − 1` via integer arithmetic `((n*p)+99)/100 − 1`, clamped
to `[0, n−1]` — exactly nearest-rank with ceiling. This is order-preserving,
needs no interpolation, and is reproducible across languages.

**Rules.**

- **PCT-1** All Foundation percentiles use nearest-rank (ceiling). Do not mix
  in linear-interpolation percentiles in the same ledger; the definitions
  disagree and make deltas meaningless.
- **PCT-2** The percentile function must sort (or use a correct streaming
  estimator) and clamp the index. Document the method next to the number.

### 3.2 Minimum sample size

**Result.** A percentile is only meaningful if samples exist *above* it.
Nearest-rank p99 needs `n` large enough that the top 1% is more than one or two
values; the practical floor is `n ≥ 1000` for stable p99 and higher for p999.
With `n = 10`, "p99" is just the max.

**Rules.**

- **SS-1** Reporting p95 requires `n ≥ 100`; p99 requires `n ≥ 1000`; p999
  requires `n ≥ 10000`. Below the floor, report the value but mark it
  *under-sampled*; never gate a decision on it.
- **SS-2** Benchmarks state `samples` next to every tail number (already a
  column in `foundation_benchmarks.md`).

### 3.3 Confidence / margin of error

**Result.** A sample quantile is itself an estimate. A distribution-free
confidence interval for the `p`-quantile comes from order statistics: the
interval `[X_(l), X_(u)]` where `l, u` are binomial ranks around `np` with
`l, u = np ∓ z·√(np(1−p))`. Equivalently, the rank uncertainty at p99 scales
like `√(n·0.99·0.01)`. For smooth interval estimates use Maritz–Jarrett or
Harrell–Davis (L-estimators over order statistics).

**Rules.**

- **MOE-1** When a tail metric gates a release or SLO, attach a CI (binomial
  rank interval is sufficient) or a run-to-run variance band; a single p99
  number with no spread is not evidence.
- **MOE-2** Compare distributions, not single tail points, across runs on the
  same machine/load shape (consistent with `foundation_benchmarks.md` §layer
  separation).

**Citations.** Hyndman & Fan, "Sample Quantiles in Statistical Packages," *The
American Statistician* 50(4), 1996. · Maritz–Jarrett method, Akinshin.
<https://aakinshin.net/posts/maritz-jarrett-vs-jackknife/> · Nearest-rank sample
size guidance. <https://hackmysql.com/eng/percentiles/>

### 3.4 Multiple comparisons in benchmark gating (Benjamini–Hochberg FDR)

**Result.** Testing `m` independent benchmarks at per-test significance level
`α = 0.05` yields family-wise false-alarm probability `1 − (1 − α)^m` (for
`m = 30`, `P(false alarm) ≈ 78.5%`). The Benjamini–Hochberg step-up procedure
sorts p-values `p_(1) ≤ p_(2) ≤ … ≤ p_(m)` and rejects all `i ≤ k` where
`k = max { i : p_(i) ≤ (i / m) · q }`, bounding the expected False Discovery
Rate (FDR) at `≤ q`.

**Rules.**

- **FDR-1** Multi-benchmark CI gates must apply Benjamini–Hochberg FDR control
  (or Holm–Bonferroni family-wise control) across the suite rather than raw
  unadjusted `p < 0.05` per benchmark.
- **FDR-2** Pair statistical significance with a minimum practical effect-size
  floor (for example, `≥ 3%` ns/op delta or `≥ 1` alloc/op delta) so zero-variance noise
  never blocks merges.

### 3.5 Sequential testing for canary rollouts (Wald SPRT)

**Result.** Repeatedly peeking at a fixed-horizon A/B or canary p-value inflates
Type I error toward 100%. Wald's Sequential Probability Ratio Test (SPRT) tracks
the log-likelihood ratio `Λ_n = ∑ log(f_1(x_i) / f_0(x_i))` against upper and
lower stopping boundaries `B = log((1 − β) / α)` and `A = log(β / (1 − α))`,
preserving exact error guarantees `(α, β)` regardless of check frequency.

**Rules.**

- **SPRT-1** Automated canary and `featureflags` progressive rollouts that
  evaluate health continuously must use sequential boundaries (SPRT or always-valid
  confidence sequences), never repeated fixed-sample z-tests.
- **SPRT-2** Circuit-breaker failure-rate trips on small windows must require a
  minimum sample floor (`MinObservations`) and, where noisy low-rate false trips
  matter, a 95% Wilson score lower confidence bound (`UseWilsonLowerBound` in
  `server-kit/go/circuitbreaker`).

**Citations (3.4–3.5).** Benjamini & Hochberg, "Controlling the False Discovery
Rate," *JRSS-B* 57(1), 1995. · Wald, *Sequential Analysis*, 1945. · Wilson,
"Probable Inference, the Law of Succession, and Statistical Inference," *JASA*
22, 1927.

---

## 4. SIMD & GPU floating-point tolerances (bounded approximation)

Governs `server-kit/go/hermes/columnar_sum_simd.go` (two 4-wide AVX2
accumulators with scalar tail) and its tolerance tests in
`columnar_sum_test.go`, plus GPU reductions under `gpu_practices.md`.

### Result (4)

Floating-point addition is **commutative but not associative**:
`(a+b)+c ≠ a+(b+c)` in general. Any reduction that re-orders or parallelizes
additions (SIMD lanes, GPU warps, multi-accumulator) produces a *different but
equally valid* result than the left-to-right scalar reference. Error growth:

- **Naive sequential sum:** worst-case error `O(n·u)`, RMS error `O(√n·u)`,
  where `u = ½·2^−52 ≈ 1.1e−16` is the double unit roundoff.
- **Pairwise / multi-accumulator (what SIMD does):** error `O(log n · u)` —
  this is *why* the vectorized sum is often **more** accurate than scalar, not
  less.
- **Kahan compensated sum:** error `≈ 2u + O(n·u²)`, effectively `O(u)` for
  `n < 1/u`. Use when accuracy must be independent of `n`.

A correctly-rounded **FMA** (`fma(a,b,c) = round(a·b+c)`, one rounding) reduces
dot-product error versus separate multiply-add and must not be assumed
bit-identical to the unfused path.

### Rules (4)

- **FP-1 — Tolerance, never bit-equality, across lanes.** SIMD/GPU/parallel
  reductions are validated against the scalar reference with a mixed
  absolute+relative tolerance, not `==`. Our shipped bound is
  `|got − ref| ≤ 1e−6·|ref| + 1e−9` for the public path and
  `≤ 1e−9·n` element-wise against the scalar accumulator — keep this form; tune
  constants from the analysis above, not by trial.
- **FP-2 — Scale tolerance with `n` and conditioning.** The acceptable error
  band grows with the count of operations and with cancellation (sums of mixed
  sign). State `n`-dependence explicitly; a fixed `1e−12` for arbitrary `n` is
  wrong.
- **FP-3 — Use the accuracy ladder deliberately.** Default to pairwise /
  multi-accumulator (`Float64Vector.Sum`, `SumValid` in `server-kit/go/hermes`).
  Promote to Neumaier compensated summation (`Float64Vector.SumCompensated`,
  `SumValidCompensated`) when `n` is large or inputs are ill-conditioned with
  alternating-sign cancellation. For streaming variance and standard deviation,
  use the Chan/Welford parallel moments monoid (`columnar_moments.go`). Reserve
  exact integer paths for money (§1).
- **FP-4 — Subnormal / NaN / Inf safeguards.** Reductions must define behavior
  on NaN (propagates), ±Inf (saturates), and subnormals (flush-to-zero only if
  the lane config says so, and then documented). Tests include these inputs.
- **FP-5 — Determinism is opt-in and costly.** If a result must be
  bit-reproducible (for example, cross-node consensus on a float), pin reduction order
  or use integer/decimal; do not expect SIMD/GPU to be deterministic across
  hardware.

### Citations (4)

- Higham, *Accuracy and Stability of Numerical Algorithms*, 2nd ed. (summation
  error bounds, pairwise vs naive vs compensated).
- Kahan summation analysis (`2u + O(nu²)` vs naive `O(√n·u)`).
  <https://en.wikipedia.org/wiki/Kahan_summation_algorithm>
- IEEE 754-2019: round-to-nearest-ties-to-even, FMA single rounding.

---

## 5. Algebraic convergence — CRDT-shaped merges (exact convergence)

Governs `server-kit/go/metadata/metadata.go` merges and any
"last-writer-wins" / set-union / counter state we replicate or fold over
out-of-order, at-least-once delivery (events under `server-kit/go/events`).

### Result (5)

A merge function converges under concurrent, reordered, and duplicated delivery
**iff** it forms a *join-semilattice*: the merge `⊔` is

- **Commutative:** `a ⊔ b = b ⊔ a` (order-independence),
- **Associative:** `(a ⊔ b) ⊔ c = a ⊔ (b ⊔ c)` (grouping-independence),
- **Idempotent:** `a ⊔ a = a` (duplicate-safe).

Shapiro et al. (2011) prove that a state-based replicated type whose states
form a join-semilattice and whose merge is the lattice join achieves **Strong
Eventual Consistency**: replicas that have received the same set of updates have
equal state, with no coordination. These are the same three properties
`tla_architecture_practices.md` requires for idempotent replay.

### Rules (5)

- **CRDT-1 — Merges must be ACI.** Any function that folds replicated or
  retried state must be commutative, associative, and idempotent. If a proposed
  merge lacks one, it is not a merge — it needs ordering (a sequencer) or it is
  a bug.
- **CRDT-2 — Property tests for ACI.** Each merge ships property-based tests
  asserting the three laws on random inputs, plus an explicit
  duplicate-delivery and reorder test (matches `NoPartialHang` rigor in the TLA
  doc).
- **CRDT-3 — LWW needs a total order on the tiebreak.** Last-writer-wins is a
  semilattice only if timestamps are totally ordered with a deterministic
  tiebreaker (for example, `(hlc, replica_id)`); wall-clock ties without a tiebreak
  break idempotency.
- **CRDT-4 — Counters: grow-only or PN.** Use G-Counter (per-replica maxima,
  merged by max) or PN-Counter; never a single shared integer merged by "take
  the bigger one" unless it is genuinely monotonic.

### Citations (5)

- Shapiro, Preguiça, Baquero, Zawirski, "Conflict-free Replicated Data Types,"
  SSS 2011 (Inria RR-7687).
  <https://www.csa.iisc.ac.in/~raghavan/CleanedPods2021/crdt-shapiro-2011.pdf>
- <https://crdt.tech/>

---

## 6. Complexity & resource-scaling boundaries

Governs the security token-bucket limiter (`server-kit/go/security/middleware.go`),
queue/pool sizing (`server-kit/go/scaling`, `resilience/httpclient.go`), and
page-aligned buffer math in the runtime lanes.

### 6.1 Token bucket / rate limiting

**Result.** A token bucket with rate `ρ` (tokens/sec) and capacity `σ`
(burst) admits at most `ρ·Δt + σ` events in any interval `Δt` — the (σ,ρ)
arrival-curve bound. It is equivalent to GCRA with emission interval `T = 1/ρ`
and limit `τ` related to burst by `σ = (τ+1)/T`. Steady-state admitted rate is
`ρ`; `σ` only buys a one-time burst.

**Rules.**

- **RL-1** Set `ρ` from the sustainable downstream capacity, `σ` from the
  largest legitimate burst — never the other way around. State both with units.
- **RL-2** Document the arrival-curve guarantee (`ρ·Δt + σ`) at the limiter;
  it is the contract callers depend on.

### 6.2 Queue depth, concurrency & tail latency (Little's Law & Kingman's Formula)

**Result.** Little's Law states `L = λ · W` — mean items in system `L` equals
arrival rate `λ` times mean residence time `W`. It holds for any stable system.
For queueing wait time before service, Kingman's G/G/1 heavy-traffic
approximation gives:

`E[W_q] ≈ (ρ / (1 − ρ)) · ((c_a² + c_s²) / 2) · τ`

where `ρ = λ·τ < 1` is utilization, `τ` is mean service time, and `c_a², c_s²`
are the squared coefficients of variation of inter-arrival and service times.
Wait time explodes hyperbolically as `ρ → 1` and scales linearly with arrival
and service variance.

**Rules.**

- **QD-1** Size bounded queues and worker pools from `L = λ·W`: the in-flight
  count needed for throughput `λ` at latency `W`. A queue smaller than `λ·W`
  forces backpressure; much larger hides latency and risks memory blow-up
  (consistent with "bounded resources are correctness boundaries" in the TLA
  doc).
- **QD-2** State `λ`, target `W`, and derived `L` when choosing a pool/queue
  bound; do not pick round numbers.
- **QD-3** Target steady-state pool utilization at `ρ ≤ 0.70–0.80` so the
  Kingman factor `ρ / (1 − ρ)` stays bounded (`≤ 4`). Reduce service-time
  variance `c_s²` (by splitting heavy jobs into dedicated pools as in
  `scaling.QueueWorkersHeavy` and bounding batch sizes) before adding workers.

### 6.3 Alignment & sizing arithmetic

**Result.** Aligning size `s` up to a power-of-two boundary `A` is
`(s + A − 1) & ~(A − 1)` — exact integer arithmetic, no division. Page counts
are `⌈s/P⌉ = (s + P − 1) / P`.

**Rules.**

- **AL-1** Buffer/page sizing uses exact integer alignment formulas; never
  float for byte counts or offsets.
- **AL-2** Alignment masks require `A` to be a power of two; assert it.

### 6.4 Retry budgets, jitter & stale-snapshot placement

**Result.**

1. *Metastable retry amplification:* Without a budget, `k` retry attempts
   multiply downstream load by up to `k×` during an outage, keeping utilization
   `ρ > 1` even after the root cause clears. A token-ratio budget (`retry.Budget`)
   bounds total load at `(1 + r) · λ`.
2. *Decorrelated / full jitter:* Unjittered or narrow symmetric backoff keeps
   synchronized failure waves correlated across attempts; drawing from `[0, cap]`
   (`retry.JitterFull`) or `[cap/2, cap]` (`websocket.ts` reconnect jitter)
   disperses arrival spikes in `O(1)` rounds.
3. *Power of two choices (`d = 2`) under stale load snapshots:* Deterministic
   argmin over a periodically mirrored load table routes every concurrent caller
   to the same lowest-score lane until the next refresh (Mitzenmacher, 2000).
   Sampling `d = 2` eligible lanes (`ovrt_dispatch::decide_power_of_two`) drops
   maximum queue depth from `Θ(log N / log log N)` to `Θ(log log N)`. In addition,
   expected-latency scoring must use exact rational scaling
   `ewma · (concurrency + inflight) / concurrency` rather than integer division
   `1 + inflight / concurrency` so sub-capacity load is never hidden.

**Rules.**

- **RB-1** Every retry loop on a shared dependency must pair bounded attempts
  with jitter (`JitterFull` or `JitterEqual`) and a token-ratio `retry.Budget`
  (or circuit breaker) to cap retry amplification.
- **PL-1** Load-aware placement must penalize partial sub-capacity load
  continuously and use `decide_power_of_two` when routing across stale mirrored
  lane snapshots.

### 6.5 Decidability & boolean minimization of rule engines (ISOP)

**Result.** By Rice's theorem and the unsolvability of the Halting Problem, any
Turing-complete policy or routing DSL makes termination and semantic equivalence
undecidable. Keeping authorization (`server-kit/go/policy`) and targeting rules
restricted to finite boolean predicates over bounded structures guarantees total
evaluation and permits exact boolean minimization via the Minato–Morreale
Irredundant Sum-of-Products (ISOP) recurrence over a care/don't-care interval
`[L, U]` (`policy.MinatoMorrealeISOP`, `Engine.Analyze`).

**Rules.**

- **DEC-1** Policy, feature-flag, and agent-guard DSLs must remain
  non-Turing-complete (no unbounded loops, no general recursion; see CP-01/CP-02).
- **DEC-2** Use `Engine.Analyze` in tests or CI reviews to detect shadowed allow
  rules (`Allow_i ∧ ¬Deny == 0`) and redundant policies before shipping policy
  changes.

### Citations (6)

- Little, "A Proof for the Queuing Formula L = λW," *Operations Research* 9(3),
  1961.
- Kingman, "The single server queue in heavy traffic," *Proc. Cambridge Philos.
  Soc.* 57, 1961.
- Mitzenmacher, "The Power of Two Choices in Randomized Load Balancing," *IEEE
  TPDS* 12(10), 2001, and "How Useful Is Old Information?" *IEEE TPDS* 11(1),
  2000.
- Minato, "Fast Generation of Irredundant Sum-of-Products Forms from Binary
  Decision Diagrams," SASIMI 1992; Morreale, "Recursive Operators for Prime
  Implicant and Irredundant Normal Form Determination," *IEEE Trans. Computers*
  19(6), 1970.
- GCRA ≡ token bucket equivalence (rate `1/T`, bucket `(τ+1)/T`).
  <https://brandur.org/rate-limiting> · LimoncelliNetworks/Cruz, σ-ρ arrival
  curves.

---

## 7. Mathematical-practices checklist (MATH-01 evidence)

A change touching the domains above attaches this checklist to its review:

- [ ] **Money:** integer minor units + ISO 4217 exponent; checked arithmetic;
      rounding mode named; allocation conserves the total (§1).
- [ ] **Probabilistic:** `m`/`k`/ID-width derived from the error/collision
      formula and asserted in a test; capacity `n` stated; estimates not used
      for exact decisions (§2).
- [ ] **Statistics:** nearest-rank percentile; sample-size floor met
      (p95≥100, p99≥1000, p999≥10000) or marked under-sampled; CI/variance on
      any gating tail metric (§3).
- [ ] **Floating point:** lane reductions validated by `abs+rel` tolerance
      scaled with `n`, not `==`; NaN/Inf/subnormal behavior tested;
      determinism requirements declared (§4).
- [ ] **Convergence:** every replicated/retried merge proven commutative,
      associative, idempotent with property tests; LWW has a total-order
      tiebreak (§5).
- [ ] **Scaling:** rate-limit `(ρ,σ)`, queue depth `L=λW`, and alignment masks
      derived from the formulas with units stated (§6).

This checklist is the `mathematical-practices-checklist` evidence required by
control `MATH-01` in `tooling/practice_controls.psv`.
