# Runtime-SDK

The `runtime-sdk` is the high-performance kernel of the Ovasabi ecosystem. It provides the infrastructure for running computationally intensive code (Rust/C++) within both Browser (WASM) and Native (Sidecar) environments.

## The Performance Model: 4KB RuntimeControlBuffer

The `runtime-sdk` provides a **Fixed-Layout 4096-byte Shared Buffer** for bounded host and guest exchanges.
Borrowed access reduces materialization at supported boundaries.
Input publication, owned output, clearing, and unit internals retain their own costs.

The 4KB buffer is the hot control plane, not the whole runtime. Large payloads use the optional `RuntimeSharedArena`, transferable buffers, or runtime-transport binary envelopes so the control plane stays cache-friendly and predictable.

### Buffer Topology

The generated runtime constants define these byte ranges.

| Offset | Size | Purpose |
| :--- | :--- | :--- |
| `0` | `64` | **Epochs**: Atomic counters for synchronization. |
| `64` | `64` | **Route Bytes**: Bounded routing metadata. |
| `128` | `32` | **Header Integers**: Schema versions, lengths, status, and context hashes. |
| `160` | `96` | **Reserved**: Unused control space before the input region. |
| `256` | `1024` | **Input Data**: Binary payload for the unit to process. |
| `1280` | `2048` | **Output Data**: Result payload from the unit. |
| `3328` | `768` | **Diagnostics**: Trace data and detailed error logs. |

### Technical Advantages

1. **Bounded Layout**: Fixed offsets permit contiguous access. Cache behavior still requires measurement.
2. **Borrowed FFI Access**: Host and guest can access one control block during execution.
   Go input publication and caller-owned output still copy bytes.
3. **Fixed Capacity**: Control-buffer capacity bounds inline payloads. Unit internals and result construction can still allocate.

### Allocation and movement evidence

The Go FFI caller-buffer path counts input copies, output copies, control clearing, and error-buffer clearing separately.
`foundation_movement` enables diagnostic probes. Ordinary builds use empty probes.
The Core gate runs through `make check-benchmark-ratchet` at the repository root.
It covers selected Hermes and Go FFI paths, with no automatic browser, WASM, process, or GPU byte budgets.

Required ownership copies protect results when pooled buffers are reused.
Preserve those lifetimes and tenant-safe clearing when evaluating reduced movement.
Use [RUNTIME-02](../docs/performance_lab.md) for additional lanes.
See the [movement evidence](../docs/info/movement_accounting_20261013.md) for counts, mutation tests, and runtime confirmation.

## Getting Started: Developing a "Unit"

A "Unit" is a single piece of logic (for example, `image:resize`, `tax:calculate_nigeria`).
The full walkthrough — crate layout, all execution lanes (stdio, FFI, shm,
browser WASM), Go integration, and the evidence checklist — lives in
[`docs/rust_unit_guide.md`](../docs/rust_unit_guide.md), with
`global_value_exchange_net_v1/rust/crates/gve-financial` as the worked example.

### Rust Implementation

A unit is a `Send + Sync` type implementing `ovrt_unit::RuntimeUnit`: a
validated descriptor plus an `execute` body. A crate without a descriptor is a
library, not a unit — the descriptor is what makes it selectable by the lane
planner and registrable in `UnitRegistry`/`NativeRuntimeHost`.

```rust
impl RuntimeUnit for MyUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "myapp.scoring.v1".to_string(),
            role: RuntimeRole::Compute, // pulse | compute | gpu | io
            input_schema: "myapp/scoring/v1/features.capnp".to_string(),
            output_schema: "myapp/scoring/v1/scores.capnp".to_string(),
            supports_wasm: true,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 2,
        }
    }

    fn execute(&self, input: &[u8], output: &mut dyn ovrt_unit::RuntimeOutput) -> Result<(), String> {
        // Stream encoded bytes into the checked destination when possible.
        output.write(input)
    }
}
```

### Dispatch Pattern

1. **Initialize Control Plane**: Set `IDX_KERNEL_READY`.
2. **Write Input**: Place data at `OFFSET_INPUT_BYTES`.
3. **Signal Execution**: Increment `IDX_INPUT_WRITTEN`.
4. **Read Output**: Wait for `IDX_OUTPUT_WRITTEN` and read from `OFFSET_OUTPUT_BYTES`.

### Parity Evidence

A unit that declares `supports_wasm: true` must prove both lanes agree.
`ovrt_wasm_host::ParityHarness::compare_units` runs the same input through
the native lane and a wasmtime-instantiated guest build, then compares the
full control-buffer state: header integers, every epoch slot, payload
regions, and diagnostics. Time-reading units pin one instant through
`ParityOptions::fixed_now_ms` and inject it into the native side; volatile
counters are excluded by name. The reference guest lives in
`crates/ovrt-parity-fixture`; enable the engine with
`--features wasm-runtime`.

## RuntimeSharedArena

`RuntimeSharedArena` is an optional SharedArrayBuffer data plane for large payloads. It provides page-aligned slabs, a descriptor table, a ring queue, diagnostics, and epoch counters. Main-thread code should render only; workers own WASM execution and blocking waits. If SAB or shared WebAssembly memory is unavailable, callers must fall back to transferable buffers or postMessage through `negotiateRuntimeMemory`.

### Columnar Batch Descriptors

Scan-heavy runtime work can publish a descriptor of type
`ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH`. The descriptor's slab contains a compact
metadata payload:

1. a 32-byte batch header with magic, schema version, row count, column count,
   flags, and optional metadata/dictionary descriptor IDs;
2. one 64-byte field descriptor per column;
3. descriptor IDs for validity, offsets, values, and auxiliary buffers.

This layout borrows the useful physical model from Apache Arrow: a record batch
has fields with the same row count, fixed-width values can live in contiguous
typed buffers, variable-width values use offsets plus values buffers, nulls use
validity buffers, and metadata is separate from the raw column bytes. It is not
full Arrow IPC. The goal is a small Foundation-native descriptor that can be
adapted to Arrow-compatible readers later without forcing command/event paths to
carry analytical payloads.

Use this lane only for analytical, telemetry, media, model, or signal batches
where contiguous column access is measurable. Row-oriented Cap'n Proto/protobuf
messages remain the visible command/event contract.
