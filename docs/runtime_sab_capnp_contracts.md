# Runtime SAB And Cap'n Proto Contracts

Status: implemented foundation runtime guide
Owner: Platform Runtime

## Purpose

Foundation runtime lanes should follow the INOS SDK pattern: host-managed
memory, bounded epochs, Cap'n Proto descriptors, stable WASM exports, and
TypeScript host orchestration.

This document defines the intended contract so future implementation does not
drift into ad hoc JSON glue or bindgen-first module execution.

## Public ABI

Runtime modules expose stable entrypoints:

```text
<module>_init_with_sab() -> i32
<module>_alloc(size: usize) -> *mut u8
<module>_free(ptr: *mut u8, size: usize)
compute_execute(service, action, input, params) -> result_ptr
compute_dispatch(capnp_job_request) -> result_ptr
optional <module>_poll()
```

The result pointer convention is:

```text
u32 little-endian length
payload bytes
```

Large payloads should move through the shared arena, packet ring, stream, bulk,
or object-store lanes instead of the small FFI result frame.

This ABI is the performance path, but it does not promise fixed latency across
browsers, devices, payload sizes, or scheduler pressure. Projects must use the
runtime and generated frontend benchmarks to establish their own SLOs. The
contract guarantees bounded queues, explicit timeouts, fail-closed fallback,
stable exported entrypoints, and no main-thread blocking waits.

## SAB Contract

The TypeScript host owns allocation and lifecycle of the shared buffer. Rust
modules receive access through stable host imports and initialization globals.

Required behavior:

- all offsets and sizes come from generated Cap'n Proto layout constants,
- views are cached and reused on hot paths,
- epochs are 4-byte aligned,
- workers may block with `Atomics.wait`,
- main-thread code must use `Atomics.waitAsync` or polling fallback,
- ring buffers are bounded and reject oversized frames,
- registry and capability tables signal epoch changes after updates.

## Cap'n Proto Contract

Cap'n Proto is used as a lens over runtime data:

- SAB layout constants,
- syscall messages in `runtime_syscall.capnp`,
- compute capsules in `runtime_compute.capnp`,
- runtime descriptors,
- chunk/store descriptors,
- diagnostics and receipts.

`runtime-sdk/scripts/generate_system_bindings.sh` emits constants for Rust,
TypeScript, and Go. `tooling/scripts/generate_runtime_contract_manifest.mjs`
also emits a TypeScript manifest so host code can discover available runtime
contract files, structs, enums, and constants without parsing schemas at
runtime.

Protobuf remains the app/backend/Hermes business contract. Do not force Cap'n
Proto into ordinary app CRUD or durable service APIs unless a runtime lane
requires descriptor or zero-copy behavior.

## Worker Rules

- Workers own blocking waits, autonomous loops, and hot WASM execution.
- Main thread owns DOM, user interaction, and browser APIs that cannot run in
  workers.
- Every worker request has a timeout, a bounded pending queue, and a terminal
  observation path.

## Invariants

- `FrameSizeBound`: no runtime frame exceeds the lane maximum.
- `EpochMonotonic`: epochs never move backwards.
- `OutputAfterInput`: output epoch does not advance before input is visible.
- `RegistryReadable`: active registry entries have valid module IDs and
  bounded capability tables.
- `NoMainThreadBlockingWait`: browser main-thread code does not call blocking
  `Atomics.wait`.
- `FallbackRefinement`: fallback lanes preserve the same visible command or
  controlled error semantics.

## Tests And Benchmarks

Required test families:

- SAB bounds and invalid offsets,
- ring buffer wraparound and oversized frames,
- registry collision and capability scanning,
- syscall timeout and response matching,
- Cap'n Proto capsule round trip,
- worker queue saturation and timeout,
- compute export allocation/free behavior.

Required benchmark families:

- epoch signal latency,
- ring buffer read/write,
- registry scan,
- Cap'n Proto decode,
- `compute_execute`,
- `compute_dispatch`,
- worker round trip,
- shared arena transfer.
