# Columnar Null Algebra

Status: baseline
Date: 2026-08-11
Owner: Platform Architecture

## Purpose

This document fixes how Foundation represents and reduces over *absent* values
in columnar data, and why that representation is the same in every execution
lane: `cpu-scalar`, `cpu-simd`, `rust-ffi`, `webgpu`, and `native-gpu`.

Null handling looks like a correctness detail and is usually treated as one. It
is actually a layout decision, and layout is what decides whether a batch can
cross a lane boundary without a decoder. Getting it wrong once means every
kernel downstream inherits a branch, a special case, or a conversion pass.

The short version: **nulls live out-of-band in a bitmap, and reductions
substitute the identity element of their monoid.** That is simultaneously the
mathematically correct handling and the fastest one, and the rest of this
document is why.

## 1. Representation: out-of-band, never a sentinel

A null is not a value. It is the absence of one. So it must not be encoded as a
value.

The integer case settles the argument on its own. `int64` has no NaN and no
spare bit pattern — every one of the 2^64 encodings is a legitimate integer. Any
sentinel you choose is a number some row might legitimately hold, and choosing
`INT64_MIN` specifically destroys `min()`. **There is no safe in-band null for
integers.** This is not a preference between two workable options; it is the
absence of a second option.

Floats appear to offer an escape via NaN, and it is a trap:

- `NaN != NaN`, which breaks equality, total ordering, sorting, hashing, and
  grouping.
- NaN poisons rather than absorbs: one null turns an entire reduction into NaN.
- It conflates "no value here" with "a computation produced an undefined
  result", which are different facts a query may need to distinguish.

Therefore: **validity is a separate bit-packed mask**, Arrow-style. Bit *i* is 1
when row *i* holds a value.

A practical consequence for sequencing work: build the `int64` lane first.
Floats let you cheat with NaN and bake in a mistake that is expensive to undo;
integers force the correct structure, and floats then inherit a design that
already works.

## 2. Reduction: substitute the identity

Every reduction the engine performs is a **monoid** — a set, an associative
operator `⊕`, and an identity element `e` where `x ⊕ e = x`.

| operation | operator | identity `e` | int64 | float64 |
| --- | --- | --- | --- | --- |
| sum | `+` | 0 | `0` | `+0.0` |
| product | `×` | 1 | `1` | `1.0` |
| min | `min` | `+∞` | `MaxInt64` | `+Inf` |
| max | `max` | `−∞` | `MinInt64` | `-Inf` |
| count | `+` | 0 | `0` | `0` |
| all / any | `∧` / `∨` | `true` / `false` | — | — |

**Rule: a reduction over a nullable column substitutes `e` for every null.**
Because `x ⊕ e = x`, nulls provably cannot perturb the result. This is a proof,
not a convention — it does not need a test to be trusted, though we test it
anyway (§7).

The rule also explains an existing piece of the engine. `Float64Vector.Sum`
sums null slots as zero, which is correct — 0 is the additive identity. But it
is correct *for sum only*. The same shortcut applied to product, min, or max is
wrong, because their identities are 1 and ±∞. Naming the rule turns a
coincidence into a guarantee and shows exactly where it stops.

### Why the correct form is also the fast form

Identity substitution replaces a *control* dependency with a *data* dependency.
The fill is a value, not a branch:

```go
m := int64(maskBit(word, bit)) // 0 or -1, all bits
sum += values[i] & m           // value or 0, no branch
```

There is nothing for a CPU branch predictor to get wrong, and — the reason this
matters for the GPU lanes — nothing for a warp to diverge on. A branch inside a
GPU kernel serializes the lanes that took different paths; a mask does not. The
same kernel shape is therefore viable on all five lanes.

This is the general point worth carrying: an identity element is precisely the
thing that lets you compute unconditionally over data you must ignore.

## 3. Min and max need a companion value

The identities for min and max (`MaxInt64`, `MinInt64`, `±Inf`) are themselves
legal values. So a bare result cannot distinguish:

- a column of 4 rows whose real maximum is `MinInt64`, from
- a column of 4 rows that are all null.

Both return `MinInt64`.

The fix is one line of algebra: reduce over the **product monoid**
`(M, ⊕, e) × (ℕ, +, 0)`, carrying the count of contributing rows alongside the
value. `Reduction[T]{Value, Count}` is that pair, and `Count == 0` means the
value is the bare identity and must not be read.

The count component is **free**. It is the POPCNT of the validity bitmap the
column already holds — no pass over the values at all (measured at 400ns vs
15µs for a value scan over the same rows, §6).

Mean falls out of the same pair. Mean is not itself a monoid, which is why it is
derived rather than reduced: `(sum, count)` is the monoid and mean is its
projection. That is also why it needs no second pass.

## 4. Two-valued logic, and the complement trap

Foundation predicates use **two-valued logic**: a null never matches. SQL uses
three-valued (Kleene) logic where `NULL = 5` evaluates to `NULL` rather than
`FALSE`, which requires a second bit per row and a slower merge.

Two-valued is the right choice for a projection hot plane, but it has a
consequence that must be stated because it is a live bug class:

> `Eq` and `Ne` no longer partition the rows. Nulls are excluded from both.

And the sharp edge that follows:

> **Complementing a validity-masked selection re-selects the nulls.** Under
> two-valued logic, "did not match" and "had no value to match" are the same
> bit, so `Not()` cannot tell them apart.

**Invariant: any complement over a nullable column must be re-intersected with
validity.** `SelectionBitmap.Not` documents this at the call site, and
`TestComplementResurrectsNulls` pins both the trap and the remedy so the
behaviour cannot drift silently.

## 5. One bitmap

Validity ("row *i* holds a value") and selection ("row *i* survived the
predicate") are different facts, but they are the same structure: a packed bit
vector over the same row count, with the same tail-word hygiene, the same
word kernels, and the same POPCNT. The engine's central step is the bitwise AND
of the two.

They are therefore one type (`bitmap`, in `columnar_bitmap.go`), with
`validityBitmap` and `SelectionBitmap` as thin vocabularies over it. This is
what lets `maskValidity` be a single bulk AND instead of a per-row test, and it
means tail-word hygiene is written once rather than maintained in two places
that can drift apart.

## 6. Crossing lanes

Word width is where the null representation stops being a hermes concern and
starts being a runtime one.

**32 bits is the natural word across every lane:**

- an NVIDIA warp is 32 lanes, and a ballot/vote instruction returns exactly a
  32-bit mask — one bitmap word *is* one warp ballot
- WGSL has no `u64`; storage buffers are `u32`
- portable SIMD's `Mask<T, 32>` is 32 bits
- Go's `bits.OnesCount32` and `OnesCount64` are both a single instruction

The engine stores `[]uint64` because that is what the CPU lanes want. On a
little-endian host this costs nothing at the boundary: a bitmap packed in 64-bit
words and the same bitmap in 32-bit words are **byte-identical**, because u64
word 0 (bytes 0–7) is exactly u32 words 0 and 1, and bit *i* lands at the same
offset either way. The bitmap does not need converting to cross into a GPU
buffer — only reinterpreting.

That is worth contrasting with the *values*, which do not travel free: hermes
carries `float64` and WGSL has no `f64` at all, so every float column needs a
real narrowing pass to reach a GPU. Integer columns and their validity bitmaps
are the part of a batch that can cross unchanged.

**Not yet proven.** No GPU lane consumes a hermes bitmap today, so the claim
above is an argument, not a verified property. Before any lane relies on it, it
needs an explicit byte-layout contract test and a big-endian guard. Treat this
section as a design constraint on the bridge, not as a capability that exists.

## 7. Measurements

Apple M1 Pro, `arm64`, 65,536 rows of `int64`, values buffer 512 KiB.
Mean of 5 runs at `-benchtime=500ms`. Reproduce with:

```bash
go test ./hermes/ -run '^$' -bench 'SumInt64|CountValid' -benchtime=500ms -count=5
```

| benchmark | ns/op | reading |
| --- | --- | --- |
| `SumInt64NullOblivious` | 14,931 | control: same loop, validity never consulted |
| `SumInt64ValidDense` | 15,259 | all-valid column: **2.2% over control** |
| `SumInt64ValidSparse` | 5,418 | mostly-null: *faster* than control |
| `SumInt64ValidHoles` | 41,741 | masked path, predictable pattern |
| `SumInt64BranchyHoles` | 58,837 | per-row branch, predictable pattern |
| `SumInt64ValidRandom` | 42,931 | masked path, unpredictable validity |
| `SumInt64BranchyRandom` | 267,436 | per-row branch, unpredictable validity |
| `CountValid` | 400 | POPCNT over the bitmap, no value scan |
| `SumInt64SingleAccumulator` | 143,269 | context: interleaving alone is 9.6× |

Four things to take from this:

1. **On dense columns the null machinery is free** (2.2%). A fully-set validity
   word skips masking entirely, so every reserved field hermes builds — which
   are dense by construction — pays nothing.
2. **Identity substitution is flat; branching is not.** The masked kernel moves
   3% between predictable and random validity (41.7µs → 42.9µs). The branchy
   version degrades 4.5× (58.8µs → 267.4µs). Branch-free cost is
   data-independent, which is the property that makes it portable to a warp.
3. **The win depends entirely on predictability.** Against a predictable
   pattern the masked kernel is 1.4× faster; against unpredictable validity it
   is 6.2× faster. Quoting only the second number would be dishonest — real
   columns sit somewhere between.
4. **Sparse columns beat the control** because an all-zero validity word is
   rejected in one compare and the corresponding values are never read at all.

Honest caveat: masking is not free where it actually runs. The masked path costs
~2.8× the dense path (41.7µs vs 15.3µs). Identity substitution does not make
null handling cheap; it makes it *predictable*, and predictable is what crosses
lanes.

## 8. Rules

1. Nulls are represented out-of-band in a validity bitmap. No sentinel values,
   no NaN-as-null, in any lane.
2. A reduction over a nullable column substitutes its monoid identity. Any
   kernel that branches per row on validity must justify it against a
   measurement.
3. Reductions whose identity is a legal value (min, max, product) must return
   the `(value, count)` pair, never a bare value.
4. Predicates use two-valued logic. Any complement over a nullable column must
   be re-intersected with validity.
5. Validity and selection are one bitmap type. New masks extend it rather than
   declaring a parallel structure.
6. Every null-aware kernel carries a naive "skip the nulls" reference and a
   parity test across the word boundary, the sub-word tail, and the all-null,
   sparse, half, dense-with-holes, and all-valid densities.
7. Test fixtures put **poison** in null slots, never zero. A kernel that reads a
   null must produce a wrong answer, not an accidentally-right one.

## 9. Documented float semantics

Two asymmetries that follow from the rules rather than from choice, both pinned
by tests:

- **NaN in a present row propagates through `sum`** (`NaN + x = NaN`) but is
  **ignored by `min`/`max`**, because both `<` and `>` are false against NaN. A
  caller needing NaN-poisoning semantics must test for it separately.
- **Masking is done on the bit pattern, not by multiplying by 0/1.** A multiply
  would turn a present `±Inf` into `NaN` (`Inf × 0 = NaN`). Clearing all bits
  yields `+0.0` — the additive identity — for any null regardless of what the
  value slot held.

## References

1. Apache Arrow columnar format, validity bitmaps:
   <https://arrow.apache.org/docs/format/Columnar.html#validity-bitmaps>
2. W3C WebGPU Shading Language (no `f64`, `u32` storage buffers):
   <https://www.w3.org/TR/WGSL/>
3. NVIDIA PTX vote/ballot instructions:
   <https://docs.nvidia.com/cuda/parallel-thread-execution/index.html#parallel-synchronization-and-communication-instructions-vote-sync>
4. Rust portable SIMD `Mask`:
   <https://doc.rust-lang.org/core/simd/struct.Mask.html>
5. Foundation lane ladder and promotion rules: `docs/performance_practices.md`
6. Foundation GPU lane posture: `docs/gpu_practices.md`
7. Columnar projection design spec: `docs/info/columnar_projection_lane.md`
8. Implementation: `server-kit/go/hermes/columnar_bitmap.go`,
   `server-kit/go/hermes/columnar_reduce.go`
