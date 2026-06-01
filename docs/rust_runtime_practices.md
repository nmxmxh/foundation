# Rust Runtime Practices

Status: baseline
Date: 2026-05-31
Owner: Platform Runtime

## Purpose

This document turns the local Rust review set into Foundation rules for
`runtime-sdk/rust`, `runtime-native/rust`, app Rust/WASM units, and generated
project checks.

The review used three passes over these local documents:

1. `23.emse.rust-bug-patterns.pdf`: common Rust bug-fix patterns and
   borrow-checker-related fixes.
2. `async-book.pdf`: async Rust execution, blocking hazards, cancellation,
   spawning, streams, `Send`, `Pin`, and executor boundaries.
3. `lars_quentin_report.pdf`: Rust performance engineering, benchmarking,
   profiling, bounds checks, cache locality, SIMD, and Rayon.

The follow-up pass also targeted the Foundation architecture documents:
`runtime_foundation.md`, `runtime_native.md`, `gpu_practices.md`,
`performance_practices.md`, and the repository `README.md`. The additional
search lens was: runtime lanes, native bridge, GPU descriptor registry, scaffold
propagation, app-owned Rust units, bounded frames, FFI/shared-memory safety,
worker response deadlines, capability discovery, and scalar/native/GPU parity.

The resulting Foundation posture is conservative: make contracts explicit,
measure before optimization, keep unsafe code isolated, and automate checks for
runtime-specific Rust risks.

## Runtime code rules

1. Production runtime Rust must not use `unwrap`, `expect`, `panic!`, `todo!`,
   or `dbg!`. Return `Result` with a stable error class or message instead.
2. Tests may use `expect` when it makes the oracle clearer, but runtime tests
   must still assert status codes, diagnostics, epochs, or error classes.
3. Long waits must have explicit bounds. Worker response waits use timeouts;
   stream/session loops must reject oversized frames before allocation.
4. Runtime buffer declared lengths must reject negative, oversized, and
   just-over-limit values. Do not silently coerce invalid headers to zero.
5. Avoid `clone()` in hot paths unless ownership must outlive the source. Prefer
   borrowed slices/views for same-call inspection and owned values only when
   crossing a thread, queue, FFI, or storage boundary.
6. Keep FFI and browser interop `unsafe` code in narrow modules with documented
   safety preconditions. Safe Rust modules should use `#![forbid(unsafe_code)]`.
7. Crates that allow unsafe code must deny `unsafe_op_in_unsafe_fn`; every raw
   pointer dereference, imported host call, or unchecked memory operation must
   sit inside a local `unsafe {}` block with a nearby `SAFETY:` explanation.
8. Public unsafe functions must include a `# Safety` section. Unsafe blocks must
   explain the caller-owned invariant, not merely restate the operation.
9. Preserve parity across direct, FFI, stdio, shared-memory, native, and
   browser/WASM lanes. Faster lanes are refinements of the same visible
   command/result contract.

## Async and blocking rules

1. Do not perform blocking I/O or long CPU work inside async tasks. Move CPU
   batches to bounded worker pools, native/WASM units, or runtime lanes.
2. Do not hold locks across `.await`, blocking receives, FFI callbacks, or
   user-controlled runtime-unit execution.
3. Spawned tasks must have an observation path: joined result, channel response,
   cancellation token, diagnostics update, or bounded worker lifecycle.
4. Values held across `.await` must be `Send` when the executor can move tasks
   across threads. Keep non-`Send` temporaries inside a smaller scope before the
   await point.
5. `select`/stream loops must define completion, cancellation, and no-progress
   behavior. Never rely on an infinite pending future as a control plane.

## Performance rules

1. Start with a behavior boundary and baseline measurement. Use `cargo test`,
   `cargo bench`/Criterion, `hyperfine`, `cargo flamegraph`, `iai`/cachegrind,
   or the Foundation native flow simulation according to the question.
2. Prefer call-by-reference and borrowed views where ownership transfer is not
   part of the contract.
3. Prefer contiguous layouts (`Vec`, fixed arrays, structure-of-arrays, runtime
   arena descriptors) for scan-heavy or vector-capable loops.
4. Reduce bounds checks with iterator/slice structure and explicit length
   validation before considering `unsafe`.
5. Add `#[inline]`, loop unrolling, target CPU flags, SIMD, or Rayon only after
   a benchmark shows the bottleneck and parity tests cover scalar fallback,
   tails, and boundary sizes.
6. Benchmark raw runtime lanes and adapter lanes separately. Encoding,
   dispatch, FFI, shared-memory, and unit execution costs answer different
   questions.
7. Tune Cargo profiles only from the workspace root that owns the runtime unit.
   Record the reason for `opt-level`, `lto`, `codegen-units`, `panic`, and
   `overflow-checks` choices in the benchmark note or PR. Do not rely on
   dependency manifests for profile settings.

## Required automation

Run the combined Rust issue check before merging runtime Rust changes:

```bash
make check-rust
```

The target runs:

1. `tooling/scripts/rust_static_analysis_check.sh`: `cargo fmt` and `cargo
   clippy -D warnings -D unsafe-op-in-unsafe-fn
   -D clippy::undocumented_unsafe_blocks -D clippy::missing_safety_doc` for
   discovered Rust manifests.
2. `tooling/scripts/rust_runtime_practices_check.sh`: Foundation-specific
   checks for panic/error discipline, bounded dispatch, bounded stdio frame
   reads, native GPU private-handle coverage, and runtime benchmark hooks.
3. `cargo test --all-features` for discovered Rust manifests.

Manifest discovery covers Foundation Core and scaffolded applications:

- `Cargo.toml`
- `rust/Cargo.toml`
- `native/src-tauri/Cargo.toml`
- `runtime-sdk/rust/Cargo.toml`
- `runtime-native/rust/Cargo.toml`
- `foundation/runtime-sdk/rust/Cargo.toml`
- `foundation/runtime-native/rust/Cargo.toml`

Generated projects receive `scripts/checks/check-rust.sh`,
`scripts/checks/rust_static_analysis_check.sh`, and
`scripts/checks/rust_runtime_practices_check.sh`. Their scaffolded Makefile
exposes `make check-rust`, and `lint-foundation` includes the Rust static and
runtime-practice gates so app-owned Rust/WASM/native implementations are checked
alongside vendored Foundation Rust.

Optional deep checks for unsafe-heavy changes:

```bash
cargo +nightly miri test --manifest-path <path-to-Cargo.toml>
```

Use Miri for FFI, pointer, endian-sensitive, and aliasing-sensitive changes when
the crate can run under Miri's supported host model. Keep it opt-in because
native OS APIs, networking, and some FFI are intentionally unsupported.

## Online Rust references

- Rust Edition Guide: `unsafe_op_in_unsafe_fn` separates the caller contract of
  an unsafe function from the local permission to perform unsafe operations.
- Rust API Guidelines: validate inputs at boundaries, document `Errors`,
  `Panics`, and `Safety`, and avoid copied examples that normalize `unwrap`.
- Cargo Book profiles: release/profile settings live in the workspace-root
  manifest and affect optimization, overflow checks, LTO, panic strategy, and
  codegen units.
- Clippy lint catalog: `undocumented_unsafe_blocks` and `missing_safety_doc`
  support automated review of unsafe explanations.
- Miri: useful for undefined-behavior detection in test executions, but not a
  proof of soundness and not a replacement for invariants and review.

## Review checklist

- [ ] Changed runtime Rust has tests for success, failure, boundary, and
      just-over-limit inputs.
- [ ] Any performance claim has a before/after benchmark and states payload
      shape, iteration count, machine/runtime, and relevant feature flags.
- [ ] Any new clone, allocation, lock, channel, or worker pool is justified by
      ownership, lifetime, or backpressure.
- [ ] Any `unsafe` is isolated, documented, and covered by parity and invalid
      pointer/length tests.
- [ ] Runtime metadata, status codes, diagnostics, and epochs remain equivalent
      across optimized and fallback lanes.
