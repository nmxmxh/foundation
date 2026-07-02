# Adding a Rust Performance Unit

Status: 0.0.1
Date: 2026-07-02
Owner: Platform Architecture

This is the end-to-end walkthrough for adding an app-owned Rust performance
library (a **runtime unit**) to a Foundation project: when to build one, the
crate layout, the descriptor contract, every execution lane (stdio, FFI,
shared memory, browser WASM), the Go and frontend integration, and the
evidence required before the unit counts as done.

Reference implementation: `global_value_exchange_net_v1/rust/crates/gve-financial`
(checked integer money math exposed as a runtime unit over both stdio and FFI,
with a Go `runtimehost` integration test and benchmarks). The unit taxonomy is
inspired by the `inos_v1` compute lineage (math, image, audio, physics,
crypto, data, gpu units over shared-memory reactors); Foundation keeps the
same shape but runs it through the 4KB control-buffer contract and the
`RuntimeUnitDescriptor` registry.

Rules that govern this lane: `docs/runtime_foundation.md` (native host
binding, §14–19), `docs/rust_runtime_practices.md`, and CP-07/CP-09 in
`docs/coding_practices.md`.

## 1. When a unit is (and is not) justified

A crate that only has Rust functions is a library. A crate with a
**descriptor, stable input/output schema names, and capability flags** is a
runtime unit the lane planner can select.

Build a unit when the work is **deterministic, batched, and boundary-worthy**:
scoring vectors, signal/image/audio kernels, physics/simulation steps,
checked financial math, compression, hashing, columnar transforms.

Do **not** build a unit for nanosecond-scale scalar checks, request
orchestration, auth/tenant decisions, or database access. Direct Go remains
the right lane when the boundary cost exceeds the work
(`runtime_foundation.md` §16): a stdio exchange costs milliseconds cold and
tens of microseconds warm, an FFI call costs sub-microsecond dispatch — the
kernel must be worth the trip.

## 2. Crate layout

App-owned compute lives in the project's `rust/` workspace (never inside
`foundation/runtime-sdk` — that is platform space):

```text
rust/
├── Cargo.toml                    # workspace, or single crate manifest
└── crates/
    └── myapp-scoring/
        ├── Cargo.toml
        └── src/
            ├── lib.rs            # unit + descriptor + host builder + FFI export
            └── bin/
                └── runtime_stdio.rs   # portable stdio runner
```

`Cargo.toml` for a unit crate (paths relative to the vendored foundation):

```toml
[package]
name = "myapp-scoring"
version = "0.1.0"
edition = "2021"

[lib]
name = "myapp_scoring"
crate-type = ["rlib", "cdylib"]   # rlib for tests/bins, cdylib for FFI + WASM

[dependencies]
ovrt-core   = { path = "../../../foundation/runtime-sdk/rust/crates/ovrt-core" }
ovrt-unit   = { path = "../../../foundation/runtime-sdk/rust/crates/ovrt-unit" }
ovrt-native = { path = "../../../foundation/runtime-sdk/rust/crates/ovrt-native" }
ovrt-ffi    = { path = "../../../foundation/runtime-sdk/rust/crates/ovrt-ffi" }

[[bin]]
name = "myapp-scoring-runtime-stdio"
path = "src/bin/runtime_stdio.rs"
```

Keep `#![forbid(unsafe_code)]` at the top of `lib.rs`. The only sanctioned
unsafe surface is inside `ovrt-ffi`/`ovrt-native`, which the macro in step 5
re-exports for you.

## 3. Implement the unit and its descriptor

A unit is a `Send + Sync` type implementing the `ovrt_unit::RuntimeUnit`
trait — two methods, nothing else:

```rust
#![forbid(unsafe_code)]

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_unit::RuntimeUnit;

pub struct ScoringUnit;

impl RuntimeUnit for ScoringUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "myapp.scoring.v1".to_string(),
            role: RuntimeRole::Compute,           // pulse | compute | gpu | io
            input_schema: "myapp/scoring/v1/features.capnp".to_string(),
            output_schema: "myapp/scoring/v1/scores.capnp".to_string(),
            supports_wasm: true,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 2,
        }
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        // input arrives pre-bounded from the 4KB control buffer (or arena
        // descriptor); return controlled errors, never panic.
        score(input)
    }
}
```

Descriptor rules:

1. `unit_id` is versioned and stable (`<app>.<domain>.<name>.v<N>`); it is the
   dispatch key on every lane, so renaming it is a contract change.
2. `input_schema`/`output_schema` name declared schema files (Cap'n Proto or
   protobuf under `api/schemas`/`api/protos`), not prose. Text protocols are
   acceptable only for bring-up and must say so (`.../text-command`).
3. `role` feeds per-role worker limits in the native host and the browser
   lane planner. `Pulse` watches/drives epochs; `Compute` is CPU work; `Gpu`
   and `Io` gate their respective capabilities.
4. `descriptor().validate()` runs at registration; an empty id/schema or zero
   `max_concurrency` fails registration, not first dispatch.
5. Financial units must use integer minor units and checked arithmetic —
   float money is rejected at review (`runtime_foundation.md` §17,
   `mathematical_practices.md`).

## 4. Build the native host

One builder function owns registration and per-role concurrency; every native
lane (stdio, shm, FFI) reuses it:

```rust
use ovrt_native::NativeRuntimeHost;
use std::{collections::BTreeMap, sync::Arc};

pub fn build_runtime_host(workers: usize) -> Result<NativeRuntimeHost, String> {
    let mut role_limits = BTreeMap::new();
    role_limits.insert(RuntimeRole::Compute, workers.max(1));

    let host = NativeRuntimeHost::new(role_limits);
    host.register_unit(Arc::new(ScoringUnit))?;
    Ok(host)
}
```

## 5. Expose the lanes

**Stdio (portable, safest — always provide this one):**

```rust
// src/bin/runtime_stdio.rs
use myapp_scoring::build_runtime_host;
use ovrt_native::serve_stdio;

fn main() {
    if let Err(error) = run() {
        eprintln!("{error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), String> {
    let host = build_runtime_host(1)?;
    serve_stdio(&host)
}
```

**FFI (trusted in-process, fastest):** one macro line in `lib.rs` exports the
versioned C ABI (`ovrt_runtime_abi_version`, `ovrt_runtime_create`,
`ovrt_runtime_destroy`, `ovrt_runtime_process_buffer`, `ovrt_runtime_write_log`):

```rust
ovrt_ffi::export_runtime_ffi!(build_runtime_host);
```

FFI is a trusted-only lane: never load arbitrary libraries or let user input
choose the module path (`runtime_foundation.md` §10).

**Shared memory (Linux-first throughput):** no unit code changes — the Rust
host selects it with `OVRT_RUNTIME_TRANSPORT=shm` and the Go side chooses the
transport in pool options. Frame-size limits and unit allowlists apply.

**Browser WASM:** `crate-type = ["cdylib"]` plus
`supports_wasm: true` is the contract; the scaffold Makefile does the rest
(step 7).

## 6. Integrate from Go

The backend talks to every native lane through
`runtime-sdk/go/runtimehost`. Stdio uses a process pool:

```go
pool, err := runtimehost.NewProcessPool(runtimehost.ProcessPoolOptions{
    Command: []string{
        "cargo", "run", "--quiet",
        "--manifest-path", "rust/Cargo.toml",
        "--bin", "myapp-scoring-runtime-stdio",
    },
    Dir:             projectRoot,
    Workers:         1,
    Transport:       runtimehost.ProcessTransportStdio,
    ExchangeTimeout: 30 * time.Second,
})
```

FFI uses the compiled `cdylib`:

```go
pool, err := runtimehost.NewFFIPool(runtimehost.FFIPoolOptions{
    LibraryPath: libraryPath, // target/release/libmyapp_scoring.{dylib,so}
    Workers:     1,
})
if errors.Is(err, runtimehost.ErrFFITransportUnsupported) {
    // non-unix platforms: fall back to stdio
}
```

Both pools expose the same call shape, so the lane is a deployment decision,
not an API decision:

```go
resp, err := pool.Execute(ctx, runtimehost.ProcessRequest{
    UnitID:        "myapp.scoring.v1",
    Input:         payload,
    ModuleVersion: 1,
})
// resp.Output, resp.StatusCode, resp.OutputEpoch, resp.Diagnostics
```

Rules: bounded `ExchangeTimeout` always; treat non-zero `StatusCode` +
`Diagnostics` as the controlled error path (`CP-04`); production binaries are
prebuilt — `cargo run` inside a pool command is a test/dev convenience only.

## 7. Ship the WASM lane to the browser

The scaffold owns the propagation path; do not hand-copy artifacts:

1. `make build-rust-wasm` — builds `rust/Cargo.toml` for
   `wasm32-unknown-unknown` and copies emitted `.wasm` files to
   `frontend/public/modules/`.
2. `make wasm-manifest` — regenerates
   `frontend/public/runtime/wasm-manifest.json` so the frontend discovers
   artifacts by manifest, never by hard-coded path.
3. Frontend: `loadWasmManifest(...)` from `@ovasabi/frontend-kit` selects the
   artifact; `BrowserRuntimeHost.instantiate(source, extraImports)` from
   `runtime-sdk/ts/browser-host` instantiates it **inside a worker** (main
   thread renders; workers own execution and blocking waits).
4. Dispatch rides the 4KB control buffer: input at the input region, epoch
   increment to signal, output read after `IDX_OUTPUT_WRITTEN`. Payloads
   above 4KB use `RuntimeSharedArena` descriptors or transferable buffers —
   the payload router picks by size policy (`<4KB` buffer, `4KB–1MB` arena,
   `>1MB` chunked streams).

## 8. Evidence: what "done" means for a unit

A unit is integrated when all of these exist (this mirrors
`runtime_foundation.md` §18 and CP-36):

- [ ] Rust unit tests for logic and failure paths in the crate, including
      malformed input returning controlled errors (no panics).
- [ ] A Go `runtimehost` integration test for **at least one native lane**
      (stdio for portability proof, FFI when the kernel is trusted/hot). Use
      `global_value_exchange_net_v1/internal/runtime/financial_runtime_test.go`
      as the template: success path, controlled-error path, and a benchmark.
- [ ] Benchmarks per lane you claim (`b.ReportAllocs()` on; stdio and FFI
      measured separately — they answer different questions).
- [ ] Parity evidence when the unit runs on more than one lane: same input,
      byte-identical output across native/WASM (`ParityHarness`; compare full
      buffer state, not just payload bytes).
- [ ] `make check-rust` clean (fmt, Clippy with unsafe-documentation lints,
      runtime practice checks, tests).
- [ ] Descriptor schemas declared under `api/schemas` or `api/protos`, and the
      seven-question definition of done in the PR note.

## 9. Quick reference

| I want… | Use |
| --- | --- |
| The trait to implement | `ovrt_unit::RuntimeUnit` (`descriptor()` + `run()`) |
| The descriptor contract | `ovrt_core::RuntimeUnitDescriptor` (+ `RuntimeRole`) |
| A native host with my units | `NativeRuntimeHost::new(role_limits)` + `register_unit` |
| Portable process lane | `ovrt_native::serve_stdio` + `runtimehost.NewProcessPool` |
| Fastest trusted lane | `ovrt_ffi::export_runtime_ffi!` + `runtimehost.NewFFIPool` |
| Linux shared-memory lane | `OVRT_RUNTIME_TRANSPORT=shm` + pool transport option |
| Browser lane | `make build-rust-wasm wasm-manifest` + `BrowserRuntimeHost.instantiate` |
| The worked example | `global_value_exchange_net_v1/rust/crates/gve-financial` |
