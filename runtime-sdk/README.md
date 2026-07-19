# Runtime-SDK

The `runtime-sdk` is the high-performance kernel of the Ovasabi ecosystem. It provides the infrastructure for running computationally intensive code (Rust/C++) within both Browser (WASM) and Native (Sidecar) environments.

## The Performance Model: 4KB RuntimeControlBuffer

Traditional data exchange between runtimes (for example, JS to WASM or Go to C++) relies on heavy serialization (JSON/Protobuf) and dynamic memory allocation. The `runtime-sdk` bypasses this using a **Fixed-Layout 4096-byte Shared Buffer**.

The 4KB buffer is the hot control plane, not the whole runtime. Large payloads use the optional `RuntimeSharedArena`, transferable buffers, or runtime-transport binary envelopes so the control plane stays cache-friendly and predictable.

### Buffer Topology

| Offset | Size | Purpose |
| :--- | :--- | :--- |
| `0` | `128` | **Control Plane (Epochs)**: Atomic counters for synchronization. |
| `128` | `128` | **Headers**: Schema versions, status codes, and context hashes. |
| `256` | `1024` | **Input Data**: Binary payload for the unit to process. |
| `1280` | `2048` | **Output Data**: Result payload from the unit. |
| `3328` | `768` | **Diagnostics**: Trace data and detailed error logs. |

### Technical Advantages

1. **CPU Cache Affinity**: 4KB fits within most L1/L2 cache pages. Keeping the buffer "hot" in the cache prevents expensive main-memory lookups during processing.
2. **Zero-Copy FFI**: The Host and Guest share a pointer to the same memory block. There is no copying of data across the boundary.
3. **Deterministic Memory**: By forbidding dynamic growth of the buffer during the FFI callback, we eliminate heap fragmentation and garbage collection latency.

## Getting Started: Developing a "Unit"

A "Unit" is a single piece of logic (for example, `image:resize`, `tax:calculate_nigeria`).
The full walkthrough — crate layout, all execution lanes (stdio, FFI, shm,
browser WASM), Go integration, and the evidence checklist — lives in
[`docs/rust_unit_guide.md`](../docs/rust_unit_guide.md), with
`global_value_exchange_net_v1/rust/crates/gve-financial` as the worked example.

### Rust Implementation

A unit is a `Send + Sync` type implementing `ovrt_unit::RuntimeUnit`: a
validated descriptor plus a `run` body. A crate without a descriptor is a
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

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        // Input is pre-validated and pulled from the 4KB buffer region.
        // Return controlled errors; never panic.
        Ok(vec![])
    }
}
```

### Dispatch Pattern

1. **Initialize Control Plane**: Set `IDX_KERNEL_READY`.
2. **Write Input**: Place data at `OFFSET_INPUT_BYTES`.
3. **Signal Execution**: Increment `IDX_INPUT_WRITTEN`.
4. **Read Output**: Wait for `IDX_OUTPUT_WRITTEN` and read from `OFFSET_OUTPUT_BYTES`.

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
