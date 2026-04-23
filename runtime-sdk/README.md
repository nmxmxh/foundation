# Runtime-SDK

The `runtime-sdk` is the high-performance kernel of the Ovasabi ecosystem. It provides the infrastructure for running computationally intensive code (Rust/C++) within both Browser (WASM) and Native (Sidecar) environments.

## The Performance Model: 4KB Unified Buffer

Traditional data exchange between runtimes (e.g., JS to WASM or Go to C++) relies on heavy serialization (JSON/Protobuf) and dynamic memory allocation. The `runtime-sdk` bypasses this using a **Fixed-Layout 4096-byte Shared Buffer**.

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

A "Unit" is a single piece of logic (e.g., `image:resize`, `tax:calculate_nigeria`).

### Rust Implementation
```rust
impl RuntimeUnit for MyUnit {
    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        // Input is pre-validated and pulled from the 4KB buffer region
        Ok(vec![...])
    }
}
```

### Dispatch Pattern
1. **Initialize Control Plane**: Set `IDX_KERNEL_READY`.
2. **Write Input**: Place data at `OFFSET_INPUT_BYTES`.
3. **Signal Execution**: Increment `IDX_INPUT_WRITTEN`.
4. **Read Output**: Wait for `IDX_OUTPUT_WRITTEN` and read from `OFFSET_OUTPUT_BYTES`.
