# Runtime SAB And Cap'n Proto Contracts

Status: implemented foundation runtime guide
Owner: Platform Runtime

## Purpose

Foundation runtime lanes use guest-owned linear memory,
bounded epochs, Cap'n Proto descriptors, stable WASM exports, and
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

The Rust guest owns region allocations. The TypeScript host requests regions and controls their lifetime.
`RuntimeModuleLoader.load(name)` selects shared WASM when capabilities permit it. No opt-in flag is required.
Each guest has one allocator memory and one host. Other workers may view that memory.
Never instantiate another Rust guest over the same allocator memory.

Browser buffer ABI version 2 exports these functions:

```text
ovrt_buffer_abi_version() -> u32                  // Returns 2.
ovrt_buffer_alloc(byte_length: u32) -> u32        // Returns a handle, or zero on failure.
ovrt_buffer_ptr(handle: u32) -> u32              // Returns a byte offset, or zero on failure.
ovrt_buffer_free(handle: u32) -> i32             // Returns 1 on success, or zero on failure.
```

Handles start at `0x80000000` and are never reused within an instance.
The registry permits 64 regions, 64 MiB per region, and 96 MiB total.
Allocations have eight-byte alignment. The shared artifact has a 128 MiB memory maximum.
The existing 4 KiB layout and generated schema version remain unchanged.
Arena descriptors retain offsets relative to their owning region.

`BrowserRuntimeHost.createRuntimeBuffer()` now returns `RuntimeMemoryRegion`.
Use `loaded.host` and `loaded.controlBuffer` from the loader result.
A supplied loader host provides configuration. Each module receives its own host fork.
Use `.bytes`, `.ints`, `.view`, or `.subarray(offset, length)` for region access.
The `.buffer` property returns the whole backing memory.
Worker messages must include `byteOffset`, `byteLength`, and the guest buffer handle.
Handles belong to the guest that allocated them.
Pulse and orchestrator messages carry the region offset automatically.

Cached views refresh after `memory.grow`. Request a new view after any call that can grow memory.
Previously retained typed arrays can become detached or retain the old shared memory length.
The loader globals also expose the current memory buffer.

### Publication and lifetime

1. The producer writes payload and header bytes before it increments the input epoch atomically.
2. The consumer observes publication before it reads those bytes.
3. Rust uses `SafeBuffer.with_input_bytes` or `with_bytes` for a scoped payload borrow.
4. The producer must not mutate borrowed bytes until guest execution returns.
5. The guest writes output and status before it increments the output epoch atomically.
6. The host consumes output before it permits the next input write.
7. Stop workers and release all borrowed views before calling `region.release()` or `loader.clear()`.

Nested byte access during a Rust borrow returns a busy error. Freeing a borrowed region fails.
Released handles fail validation. Released host regions reject further view access.
JavaScript cannot revoke previously escaped typed arrays. Callers must obey the view lifetime contract.
Parallel jobs use separate workers and guest memories. A control region permits one exchange at a time.

### Copy budget and fallback

The direct ABI performs zero `ovrt_copy_*` calls for control headers, payload access, and epoch operations.
Rust scoped reads borrow bytes without a payload allocation or copy.
Writing an existing JavaScript input into a region still copies that input once.
Rust output construction and writes can still allocate and copy.
`read_at`, `read_input_bytes`, and `readOutputBytes` return owned results and therefore copy.

Without shared capability, a scalar ABI version 2 guest still uses direct access within its owning thread.
The loader tries `.shared.wasm` first, then existing compressed and raw scalar artifacts.
Older guests retain the bounded copy imports automatically.
Unshared regions cannot enter worker dispatch. Their owning thread executes them.
No main-thread blocking wait is permitted.

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
