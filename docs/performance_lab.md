# Low-Level Performance Lab

Status: recommended for performance-sensitive changes
Owner: Runtime Performance

## Purpose

Performance claims need repeatable evidence. This lab contract defines the
extra artifacts required when Foundation work depends on CPU, memory, syscall,
I/O, WebGPU, native GPU, WASM, Rust, or transport-lane performance.

Do not use these requirements to slow down ordinary product work. Use them when
the change claims a performance win, changes a hot path, or introduces a new
runtime lane.

## Measurement Lanes

| Lane | Minimum evidence |
| --- | --- |
| CPU hot path | `go test -bench`, Criterion, or Vitest bench with allocation counts and repeated runs. |
| Scheduler/concurrency | `runtime/trace`, pprof block/mutex profile, goroutine leak evidence, or bounded load test. |
| Allocator/memory | bytes/op, allocs/op, heap profile, RSS/PSS where relevant, and object lifetime explanation. |
| Cache/TLB/branch | CPU-counter capture where available, or explicit fallback when counters are unavailable. |
| Syscall/I/O | syscall count, copy path, sendfile/splice/io_uring decision, and fallback path. |
| Database/Redis | query plan, WAL/rows/bytes, pool acquire timing, Redis command mix, and p95/p99 under load. |
| WASM/FFI/native | host/guest boundary cost, ABI version, pointer/length validation, and scalar fallback parity. |
| GPU/WebGPU | device matrix, dispatch timing, device-loss behavior, layout conformance, and CPU fallback. |

## CPU Counter Taxonomy

Native/runtime claims should collect this taxonomy where the host allows it:

1. cycles, instructions, and IPC
2. cache references, cache misses, L1/L2/L3 misses where available
3. TLB misses, branch instructions, and branch misses
4. allocator churn, bytes/op, allocs/op, heap live bytes, and GC pressure
5. syscall count, context switches, page faults, and kernel time
6. NUMA locality, remote memory, thermal throttling, and CPU frequency state

Intel and AMD captures should use the current vendor toolchain where available:
Linux `perf`, Intel VTune/pmu-tools, AMD uProf/IBS/L3PMC/DFPMC, or platform
equivalents. If counters are unavailable, record that explicitly and keep the
benchmark valid through ordinary timing, allocation, and trace evidence.

## Tool Lanes

Go pprof/trace:

- Use CPU, heap, goroutine, block, and mutex profiles for request/runtime paths.
- Use `runtime/trace` when scheduler, network poller, GC, goroutine lifecycle,
  or blocking behavior is part of the claim.
- Foundation's benchmark runner supports `PROFILE=1`, `TRACE=1`,
  `PROFILE_DIR=...`, and `PERF_COUNTERS=1` for local evidence capture.

Rust Miri/Loom:

- Use Miri for unsafe, FFI, pointer, alignment, endian, aliasing, and buffer
  lifetime changes when the crate can run under Miri's host model.
- Use Loom for Rust concurrency primitives, atomics, queues, and cancellation
  paths where a small state space can expose interleavings.
- Foundation's Rust runtime check exposes opt-in `RUST_RUNTIME_MIRI=1` and
  `RUST_RUNTIME_LOOM=1` lanes.

WebGPU/WGSL:

- Capture adapter/device limits, WGSL layout conformance, dispatch dimensions,
  buffer sizes, device-loss behavior, pipeline warmup, upload/readback cost,
  and fallback lane.
- Shader claims must include scalar or WASM/SAB parity tests.

CUDA/Nsight:

- CUDA-specific lanes should include Nsight Systems for host/device timeline
  questions and Nsight Compute for kernel-level occupancy, memory, scheduler,
  and instruction analysis.
- CUDA Graphs, streams, async copies, pinned memory, shared memory, and tensor
  paths require a capture bundle and fallback/parity evidence.

## Variance Rules

1. A single benchmark run is a smoke test, not proof.
2. Report sample shape, duration/count, allocs/op, and max latency where
   available.
3. If p99 moves more than the expected regression threshold, rerun with higher
   count or longer duration before calling it noise.
4. Investigate fixture allocation, timer placement, GC, scheduler pressure,
   lock contention, thermal state, cold/warm cache, and hidden filesystem or
   network work.

## Do-Not-Optimize Gate

Do not add a faster lane unless all are true:

1. the bottleneck is measured
2. the visible contract is preserved
3. the fallback is documented and tested
4. the new lane has a rollback plan
5. the owning practice doc and controls matrix still match the evidence
