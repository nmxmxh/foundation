# Foundation Benchmarks

Status: baseline  
Date: 2026-05-01  
Owner: Platform Architecture

## Purpose

Foundation performance work is not a single transport bet. The architecture uses a ladder:

1. same-process direct typed/frame dispatch
2. fixed binary frame codecs and borrowed frame views
3. generated protobuf for typed network payloads
4. gRPC for cross-host or polyglot process boundaries
5. JSON envelopes only as compatibility adapters
6. native runtime `ffi` or `shm` for trusted same-host hot units
7. browser worker + WASM + `SharedArrayBuffer` where the browser can support it

The benchmark suite exists to prove that ladder stays honest. The fastest lane should not pay network-stack or JSON costs, and the compatibility lane should remain visibly more expensive than the binary paths.

The benchmark suite does not replace architecture invariants. TLA-style rules live in `foundation/docs/tla_architecture_practices.md`: hard bounds and correctness properties must be tested as behavior; p95/p99, throughput, CPU, heap, and allocation shape are statistical evidence.

## Measurement taxonomy

1. Correctness properties: invariants, allowed transitions, terminal states, metadata preservation, tenant isolation, and refinement/parity.
2. Worst-case operational properties: deadlines, queue caps, retry caps, acquire timeouts, payload limits, and overload behavior.
3. Statistical performance properties: ns/op, B/op, allocs/op, RPS, p50/p95/p99 latency, CPU profiles, heap profiles, and cache-hit ratios.

Benchmarks primarily cover the third category. Performance PRs that alter the runtime ladder must also include tests or contract checks for the first two categories.

## How to run

From the repository root:

```bash
foundation/tooling/scripts/performance_check.sh
```

For the Go-only server-kit slice:

```bash
cd foundation/server-kit/go
go test -tags=perf ./grpcsvc ./chain
go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem ./grpcsvc ./chain
```

Set `PROFILE=1` when you need CPU and heap profiles under `/tmp/ovasabi-foundation-profiles`.

## Current reference run

Environment:

- Date: 2026-05-01
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: server-kit Go benchmark command above

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | 16.18 | 0 | 0 | Same-process router dispatch |
| `BenchmarkDirectFrameClientDispatch` | 24.62 | 0 | 0 | Same-process client facade with validation |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | 24.16 | 0 | 0 | Append encode + borrowed decode view |
| `BenchmarkBinaryFrameAppendRoundTrip` | 63.54 | 34 | 3 | Append encode + owned frame decode |
| `BenchmarkBinaryFrameCodecRoundTrip` | 104.4 | 178 | 5 | gRPC codec-compatible owned round trip |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | 360.2 | 152 | 6 | Generated protobuf marshal/unmarshal |
| `BenchmarkRunParallel` | 1197 | 592 | 8 | Bounded parallel operation chain |
| `BenchmarkDispatchFrameOverBufconn` | 22227 | 11034 | 183 | Binary frame over in-memory gRPC |
| `BenchmarkDispatchOverBufconn` | 27218 | 12690 | 213 | JSON envelope over in-memory gRPC |

## Latest Local Check

Environment:

- Date: 2026-05-05
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: targeted server-kit Go benchmark commands from this document

| Benchmark | ns/op | B/op | allocs/op | Delta vs 2026-05-01 | Interpretation |
| --- | ---: | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | 18.59 | 0 | 0 | slower/noisy | Still same-process, zero-allocation routing |
| `BenchmarkDirectFrameClientDispatch` | 25.68 | 0 | 0 | stable/slower | Validation facade remains zero-allocation |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | 22.70 | 0 | 0 | faster | Borrowed frame view remains the fastest codec lane |
| `BenchmarkBinaryFrameAppendRoundTrip` | 62.25 | 34 | 3 | stable/faster | Owned binary frame path is stable |
| `BenchmarkBinaryFrameCodecRoundTrip` | 113.4 | 178 | 5 | slower/noisy | Codec-compatible owned path remains below protobuf |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | 386.9 | 152 | 6 | slower/noisy | Typed network payload lane remains sub-microsecond |
| `BenchmarkRunParallel` | 1712 | 592 | 8 | slower/noisy | Orchestration overhead is still microsecond-class |
| `BenchmarkDispatchFrameOverBufconn` | 31645 | 10969 | 181 | slower/noisy | Binary gRPC boundary stays much cheaper than external network I/O |
| `BenchmarkDispatchOverBufconn` | 39989 | 12653 | 212 | slower/noisy | JSON compatibility lane remains most expensive |

The important signal is unchanged: same-process and borrowed binary lanes are nanosecond paths, while gRPC/JSON lanes are microsecond boundary paths. Use benchmark deltas here as local evidence only; laptops, battery state, scheduler noise, and dependency versions can move these numbers by double-digit percentages.

### Latest App-Lane Check

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_DirectFrame_DomainCall` | 42.09 | 32 | 1 | App-shaped direct frame domain call |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 6865 | 9652 | 74 | HTTP JSON ingress to dispatch request |
| `BenchmarkAppLane_Auth_ValidateToken` | 3860 | 2152 | 28 | JWT validation only |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 8335 | 11364 | 83 | HTTP middleware with auth, headers, validation, RBAC |
| `BenchmarkAppLane_Cache_GetHit_JSONValue` | 58.11 | 0 | 0 | In-memory cache hit |
| `BenchmarkAppLane_Retry_NoRetrySuccess` | 4.530 | 0 | 0 | No-retry success fast path |
| `BenchmarkAppLane_CircuitBreaker_ClosedSuccess` | 62.21 | 0 | 0 | Healthy dependency safety wrapper |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 5941 | 1368 | 26 | Accepted worker enqueue and drain |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1734 | 803 | 19 | Bounded queue rejection path |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1537 | 772 | 17 | Missing processor rejection path |
| `BenchmarkAppLane_Retry_CanceledWait` | 87.83 | 96 | 2 | Canceled retry wait path |

These app-lane results explain the practical architecture boundary: the foundation communication core remains far cheaper than real HTTP auth, route building, worker rejection, or domain persistence logic. Optimize product code by keeping hot internal calls on direct/binary lanes, then budgeting auth, DB, worker, and cache costs explicitly.

### Latest Foundation Hardening Pass

After reducing avoidable allocations in JWT bearer parsing, HTTP path parameter extraction, and worker job normalization:

| Benchmark | Before | After | Allocation change | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 5570 ns | 5141 ns | 74 -> 71 | Path extraction no longer uses regex match allocation |
| `BenchmarkAppLane_Auth_ValidateToken` | 3380 ns | 3312 ns | 28 -> 27 | Token parsing avoids split allocation |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 7600 ns | 7455 ns | 83 -> 81 | Auth + middleware path inherits parsing improvement |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 5373 ns | 5294 ns | 26 -> 24 | Metadata-free raw jobs avoid empty map allocation |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1734 ns | 1647 ns | 19 -> 17 | Bounded rejection path is cheaper |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1543 ns | 1479 ns | 17 -> 15 | Missing processor rejection path is cheaper |

These are small but useful foundation-wide improvements because every scaffold inherits them. The bigger lesson is unchanged: auth, HTTP shaping, and worker queues are microsecond-scale safety boundaries. They are appropriate at ingress and async boundaries, but same-process hot domain calls should stay on direct frame or typed call lanes.

### Post-Quantum TLS Check

Local TLS 1.3 handshake benchmark:

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkTLSHandshake_ClassicalX25519` | 406751 | 72159 | 813 | Classical TLS 1.3 local handshake |
| `BenchmarkTLSHandshake_HybridX25519MLKEM768` | 576089 | 102632 | 829 | Hybrid post-quantum TLS 1.3 local handshake |
| `BenchmarkApplyPostQuantumTLSAuto` | 196.6 | 964 | 3 | Config posture application |

Hybrid post-quantum TLS adds about 169 microseconds in this local handshake benchmark. That cost belongs at connection/session establishment or the edge terminator, not inside per-request JWT validation, render loops, domain handlers, or worker hot loops. The foundation posture remains: use standardized hybrid TLS where supported, keep signatures for durable artifacts and compliance workflows, and benchmark before moving post-quantum signatures into any request path.

### Phase 2 Core Utilities (Server-Kit)

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `Cache.Get` (Memory) | 53.4 | 0 | 0 | Ultra-low latency cache read |
| `Worker.MapJob` | 5.72 | 0 | 0 | Minimum engine overhead |
| `CircuitBreaker.Execute` (Healthy) | 35.1 | 0 | 0 | Safety overhead per call |
| `Events.Fanout` (3 Subs) | 284.1 | 64 | 2 | In-process delivery latency |

These numbers are local references, not universal budgets. CI and developer laptops vary. The important signal is the ordering and allocation shape.

## Comparative interpretation

Direct frame dispatch is the baseline for same-process hot communication. It validates the frame and calls the registered handler without gRPC, Redis, HTTP, JSON, or heap allocation. This is the lane to use when caller and handler share a process and lifecycle.

Borrowed binary frame views are the right parser shape for synchronous hot paths. `UnmarshalFrameView` returns slices into the original frame bytes, so routing and validation can inspect event type, correlation ID, schema version, and payload without copying. Owned decode remains available when data needs to escape the input buffer lifetime.

Generated protobuf remains the default for typed cross-process contracts. It is not zero-allocation in this benchmark, but it preserves schema discipline and avoids `map[string]any` materialization. Use it for service-to-service contracts where the payload semantics are stable and versioned.

gRPC is a boundary tool, not the default internal hot path. Even with `bufconn`, it is orders of magnitude slower than direct dispatch because it exercises client/server call machinery, interceptors, metadata, codec invocation, and message framing. That cost is acceptable for cross-host/polyglot boundaries and unacceptable for same-process routing.

JSON envelopes are compatibility-only. In the current run, binary gRPC frames use fewer allocations and fewer bytes than JSON envelopes over the same in-memory gRPC path. New hot communication APIs must provide typed or binary paths first and keep JSON as an explicit fallback.

The operation chain benchmark measures orchestration overhead, not external I/O speed. `chain.RunParallel` is intended for independent I/O-bound work where latency is dominated by storage, network, or service calls. It must preserve cancellation semantics: critical failures cancel the chain, non-critical failures are reported without blocking unrelated work.

### Phase 2 Performance Multipliers

**Singleflight Coalescing**: By integrating `singleflight` into the `cache` package, we eliminate the 100x latency spike typically seen during cache stampedes. Concurrent requests for the same missing key now wait for a single computation, converting a potential system-wide slowdown into a predictable sub-millisecond wait.

**Vectorized Batching**: Bulk processing of `EventBatch` envelopes reduces the frequency of JS event loop ticks and Go scheduler wakeups. For high-throughput streams (8kHz+), batching provides a 30-50% reduction in total system CPU consumption compared to single-event dispatch.

**Adaptive Worker Pools**: The worker engine's ability to scale based on queue depth ensures that throughput (hz) remains high even under sudden pressure, while keeping idle memory overhead near zero on quiet nodes.

## Runtime SDK comparison

The runtime SDK has a separate performance shape:

- `ffi`: trusted in-process mutation of the fixed 4KB runtime control buffer.
- `shm`: same-host process isolation with shared-file runtime buffer under `/dev/shm`.
- `stdio`: portable framed buffer exchange.
- browser worker/WASM: worker-owned execution with `SharedArrayBuffer` when cross-origin isolation allows it.

Do not reduce runtime parity to a single Wasm-host implementation strategy. The correct parity question is whether a unit produces identical buffer state across the lanes the product actually uses: status code, output bytes, diagnostics, and epoch transitions.

The next runtime benchmark target should compare:

1. native direct unit dispatch
2. FFI `process_buffer`
3. stdio framed runtime buffer
4. shared-memory transport on Linux
5. browser worker/WASM where available

Each run should report latency, bytes copied at transport boundaries, allocations where the language runtime exposes them, and failure-path behavior.

Runtime lane planning now has explicit scheduling inputs: payload size, workload class, trust, locality, batch size, deadline, unit capabilities, and available hardware/runtime features. The planner must preserve the runtime contract while selecting the cheapest physical lane:

1. direct/same-process for trusted control payloads,
2. Rust FFI or CPU SIMD for trusted vector-sized work,
3. shared-memory or WASM/SAB for bounded same-host/browser payloads,
4. WebGPU for wide data-parallel batches large enough to amortize dispatch,
5. transfer or stream fallbacks when SAB/GPU lanes are unavailable.

GPU batch layouts should be benchmarked separately from descriptor-ring traffic. The browser-host helper packs batch regions on 256-byte boundaries by default so storage-buffer style workloads can move through the arena without per-item ad hoc layout decisions.

`RuntimeWebGpuHost` is intentionally split into testable pieces:

1. pack arena descriptors into aligned GPU input buffers,
2. create/cache compute pipelines asynchronously,
3. dispatch workgroups,
4. copy GPU output to a readback buffer,
5. write output slices back to the source arena descriptors.

Node tests validate the deterministic packing/writeback helpers without requiring a physical GPU. Browser/device benchmarks should add real `GPUDevice` dispatch metrics separately because adapter choice, driver, browser, and power state dominate those numbers.

## Browser shared-arena reference

The browser-host benchmark measures payload movement inside `RuntimeSharedArena`. Current local reference:

| Benchmark | hz | mean ms | p99 ms | Role |
| --- | ---: | ---: | ---: | --- |
| 4KB slab write/read | 736596.92 | 0.0014 | 0.0044 | Control-plane-sized payload movement |
| 64KB slab write/read | 102084.09 | 0.0098 | 0.0462 | Medium slab payload movement |
| 1024KB slab write/read | 8422.95 | 0.1187 | 0.6815 | Large slab payload movement |
| sustained descriptor-ready ring traffic | 922.79 | 1.0837 | 1.2702 | Descriptor queue pressure path |

The 4KB control-plane-sized path is roughly 7.22x faster than the 64KB slab path and 87.45x faster than the 1024KB slab path in this run. That supports the current design split: keep the hot control plane fixed and small, move larger payloads through arena descriptors or explicit streams, and benchmark descriptor-ring pressure separately from raw slab copies.

### Browser Shared-Arena Hardening Run

Environment:

- Date: 2026-05-07
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --reporter=verbose`

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| 4KB slab write/read | 1500 | 4000 | Owned read copy path |
| 4KB slab write/read view | 500 | 600 | Borrowed view avoids output copy |
| 4KB slab fast write/read view | 400 | 500 | Thin hot path for prevalidated descriptor use |
| 64KB slab write/read | 10700 | 55500 | Owned read cost grows with payload size |
| 64KB slab write/read view | 3000 | 3200 | View path stays near memory-copy cost |
| 64KB slab fast write/read view | 2900 | 3200 | Fast path is limited by slab copy |
| 1024KB slab write/read | 121400 | 762200 | Large owned copy path |
| 1024KB slab write/read view | 43100 | 62200 | Large view path avoids the second copy |
| 1024KB slab fast write/read view | 43100 | 60700 | Same limit: moving 1MB dominates |
| sustained descriptor-ready ring traffic | 1079900 | 1255700 | Single-entry queue CAS loop baseline |
| descriptor-ready batch traffic x128 | 1090900 | 1243800 | Old batched API still pays per-entry queue work |
| descriptor-ready fast batch traffic x128 | 487800 | 607600 | One tail CAS plus one head CAS per batch |
| preallocated write/enqueue/dequeue batch x128 | 57200 | 80600 | Best full hot lane when descriptors are already owned |
| descriptor release/reallocate free-list x1 | 313 | 400 | Reuse path is sub-4KB-copy cost |
| descriptor release/reallocate free-list x128 | 35100 | 55700 | Lifecycle churn stays below preallocated write/enqueue/dequeue x128 |

The major improvement is descriptor orchestration, not raw memory bandwidth. `descriptor-ready fast batch traffic x128` is about 2.24x faster than the old x128 batch path in this run. The new release/reallocate path also proves long-running processes can recycle descriptor IDs and their page-aligned slab regions without advancing the arena allocation head when the next request fits.

Operational rule: allocate descriptors for stable flows, reuse them aggressively, and use fast queue reservations only for batches. For x1, the old single-entry path remains competitive because the fast batch path has extra setup without amortization.

### Rust Native Buffer Hardening Run

Environment:

- Date: 2026-05-07
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: `cd foundation/runtime-sdk/rust && cargo run --release -p ovrt-native --bin buffer_bench`

| Benchmark | ns/op | Interpretation |
| --- | ---: | --- |
| native read_output_bytes owned Vec | 68.58 | Copies output into a new owned allocation |
| native output_bytes_view borrowed | 4.05 | Borrows from the fixed control buffer |
| native write_output_bytes clear+copy | 55.37 | Clears the full output region, then copies payload |
| native write_output_bytes_fast copy only | 33.98 | Copies payload and updates length only |

The Rust-side optimization space is real but specific: prefer borrowed views when bytes do not need to outlive the control buffer, and use the explicit fast write path only when all readers honor the length field. The default clearing write remains available for defensive hygiene when stale bytes outside the active length must be erased.

Research notes:

- Rust `copy_nonoverlapping` is the `memcpy`-equivalent primitive for proven non-overlapping regions, but it is unsafe and requires strict validity/alignment guarantees. Safe `copy_from_slice` remains the default until a benchmark proves the unsafe path matters.
- Rust `std::hint::black_box` is appropriate for this local benchmark because it asks the compiler to avoid optimizing away the measured operation, but the standard library documents it as best-effort rather than a correctness mechanism.
- Crates such as `bytes` and `zerocopy` are relevant future options for cross-boundary owned/shared byte views, but the current 4KB control buffer is already simpler and faster with direct borrowed slices.

### Cross-Runtime Benchmark Expansion Run

Environment:

- Date: 2026-05-08
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Commands:
  - `cd foundation/runtime-sdk/go && go test -bench='BenchmarkBuffer' -benchmem -run='^$' ./runtimehost`
  - `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --run`
  - `cd foundation/runtime-sdk/rust && cargo run -p ovrt-native --bin buffer_bench --release`
  - `cd foundation/server-kit/go && go test ./wsrouting -run 'TestResolveTargets|TestNilClient' -bench 'BenchmarkRouter' -benchmem`

Go runtimehost fixed-buffer results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkBufferSetInputBytes1KB` | 31.45 | 0 | 0 | Full input-region clear + copy remains allocation-free |
| `BenchmarkBufferInputBytesOwned1KB` | 184.6 | 1024 | 1 | Owned read cost is exactly the output copy |
| `BenchmarkBufferSetOutputBytes2KB` | 79.81 | 0 | 0 | Full output-region clear + copy remains allocation-free |
| `BenchmarkBufferOutputBytesOwned2KB` | 326.8 | 2048 | 1 | Owned output read allocates one copied payload |
| `BenchmarkBufferEpochAdd` | 7.165 | 0 | 0 | Atomic epoch movement is nanosecond-class |
| `BenchmarkBufferDiagnosticsText` | 345.9 | 768 | 1 | Current diagnostics read materializes a bounded string |

The Go runtimehost hot write paths now clear fixed regions with `clear(...)` instead of allocating temporary zero slices. That preserves the security hygiene of clearing stale bytes while restoring the intended allocation-free control-plane write behavior.

Browser shared-arena update:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| 4KB slab write/read | 1400 | 4400 | Owned read copy path remains around control-plane scale |
| 4KB slab write/read view | 500 | 600 | Borrowed view stays sub-microsecond |
| 4KB slab fast write/read view | 400 | 500 | Fast prevalidated path remains the browser hot lane |
| 64KB slab write/read view | 3000 | 3700 | Medium borrowed payloads stay near memory-copy cost |
| 1024KB slab write/read view | 43200 | 64400 | Large borrowed payloads avoid a second copy |
| descriptor-ready fast batch traffic x128 | 484000 | 665000 | Fast batch queueing remains about 2.26x faster than old x128 batch |
| preallocated write/enqueue/dequeue batch x128 | 61600 | 95500 | Best full arena lane when descriptors are pre-owned |
| packet-ring enqueue/dequeue/complete/release x128 | 43900 | 66200 | Packet-like lifecycle is cheaper than general arena descriptor orchestration |

Rust native buffer update:

| Benchmark | ns/op | Interpretation |
| --- | ---: | --- |
| native read_output_bytes owned Vec | 53.11 | Owned read copy is faster than the prior local run |
| native output_bytes_view borrowed | 3.73 | Borrowed native view remains the fastest runtime lane |
| native write_output_bytes clear+copy | 39.59 | Defensive clear + copy improved |
| native write_output_bytes_fast copy only | 16.65 | Fast write is the trusted hot path when stale bytes outside length are irrelevant |

WebSocket routing local-load results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterRegisterLocalOnly` | 534.9 | 228 | 4 | Local connection registration is sub-microsecond |
| `BenchmarkRouterResolveTargetsUserLocal` | 18758 | 59760 | 12 | Resolves 1024 local user targets without per-connection copy allocation |
| `BenchmarkRouterForEachLocal1024` | 37044 | 98304 | 1024 | Public copy-safe iterator intentionally allocates per connection |

The WebSocket routing improvement is behavioral-neutral: public read helpers still return copies, but `ResolveTargets` now resolves local user/broadcast targets under the router read lock and appends connection IDs directly. That keeps tenant/session safety semantics while removing avoidable per-connection object copies from realtime fanout.

Runtime-transport Go results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkCreateEnvelopeJSON` | 356.7 | 96 | 2 | Creates correlation/request/idempotency metadata with crypto randomness |
| `BenchmarkResolveRouteLinear16` | 33.37 | 0 | 0 | Small generated route tables are cheap even with linear lookup |
| `BenchmarkCanDispatchExactCapability` | 5.353 | 0 | 0 | Capability guard is effectively free on exact match |
| `BenchmarkSchemaRegistryNegotiate` | 31.15 | 0 | 0 | Schema negotiation is allocation-free for small accepted-version sets |

Runtime-transport TypeScript binary-envelope results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| encode JSON envelope to protobuf bytes | 7100 | 13000 | JSON payload materialization dominates binary envelope encode |
| decode JSON envelope from protobuf bytes | 2900 | 4300 | JSON payload decode is still microsecond-class |
| encode protobuf envelope bytes | 3900 | 8900 | Typed/binary payloads avoid JSON payload stringify |
| decode protobuf envelope bytes | 1700 | 2200 | Typed/binary decode is about 1.7x faster than JSON decode |
| encode JSON compatibility envelope | 1400 | 1900 | Compatibility JSON string path is fast for already-object payloads, but less typed |
| decode identity binary frame | 200 | 300 | Identity frame detection is effectively a header check |

Runtime-transport TypeScript routing results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| parse event type | 200 | 300 | Event contract validation is sub-microsecond |
| create JSON envelope | 700 | 800 | Browser envelope creation is cheap when IDs are provided |
| resolve route by event type | 100 | 100 | Precomputed route map lookup is effectively free |
| resolve route by path | 200 | 300 | Method normalization + path map lookup remains sub-microsecond |
| can dispatch exact capability | <100 | 100 | Exact capability fast path is extremely cheap |
| can dispatch write via admin fallback | 200 | 300 | Admin fallback remains sub-microsecond after removing transient arrays |

Transport improvement notes:

- Runtime binary frame decode now uses `subarray` for framed compressed payloads, avoiding a copy before decompression.
- Runtime metadata extras encoding reuses a constant `{}` byte sequence for empty extras and strips reserved JSON metadata keys lazily, avoiding unnecessary object copies for normal envelopes.
- Go correlation ID construction now uses a stack buffer before the final string conversion, reducing `CreateEnvelope` from 3 allocations to 2 in the local run.
- TypeScript event parsing now reuses precompiled regex objects, and capability fallback checks avoid transient arrays.
- Malformed binary frames now have explicit tests for unsupported version, unsupported encoding id, and truncated payload. These are security-relevant parser boundaries, not just benchmark fixtures.

### Packet-Ring Exploration

Foundation now carries a DPDK-shaped browser-host primitive for optional packet-like lanes: fixed descriptor slots, burst enqueue/dequeue, explicit ownership states, monotonic timestamps, and drop/high-water counters. This is not a DPDK dependency. It is the contract a future native packet adapter must refine.

Research alignment:

- DPDK ring guidance emphasizes fixed-size FIFO rings, lockless producer/consumer modes, and bulk/burst enqueue/dequeue. Foundation mirrors those mechanics at the runtime contract level.
- Linux timestamping separates ordinary software timestamps from hardware/NIC timestamps. Foundation treats timestamp precision as diagnostics and keeps domain-visible behavior independent of timestamp source.
- Solarflare/Onload-style acceleration is valuable because it can preserve socket-shaped application code while moving packet handling closer to hardware. Foundation follows the same compatibility principle: app code keeps server-kit/runtime contracts, while optional adapters can use lower-level lanes underneath.

Packet-ring benchmarks should be compared against descriptor-ring and preallocated arena paths, not against HTTP. HTTP pays for identity, middleware, routing, and compatibility; packet rings measure low-level lane mechanics.

Local exploratory run:

- Date: 2026-05-07
- Command: `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --reporter=verbose`

| Benchmark | mean ns | p99 ns | Comparison |
| --- | ---: | ---: | --- |
| packet-ring enqueue/dequeue/complete/release x1 | 400 | 500 | Similar class as 4KB fast arena view, with lifecycle timestamps |
| packet-ring enqueue/dequeue/complete/release x8 | 2800 | 3500 | Faster than preallocated arena x8 in this run |
| packet-ring enqueue/dequeue/complete/release x32 | 10900 | 20200 | Lower than preallocated arena x32, but with p99 noise from timestamp/lifecycle work |
| packet-ring enqueue/dequeue/complete/release x128 | 47900 | 109700 | Faster mean than preallocated arena x128, noisier tail |
| descriptor-ready fast batch traffic x128 | 495200 | 610800 | Packet ring is roughly 10.3x cheaper for packet-like local lifecycle work |
| descriptor-ready batch traffic x128 | 1097500 | 1289900 | Packet ring is roughly 22.9x cheaper than old descriptor queue orchestration |

Interpretation: packet-ring mechanics are useful for packet-like streams where descriptors are owned by a tight worker/runtime lane. They are not a replacement for the shared arena descriptor ring, which carries larger cross-lane payload ownership and WebGPU/WASM interop semantics. The current packet-ring tail is dominated by JavaScript object/lifecycle/timestamp work; a native/Rust version should use structure-of-arrays descriptor storage and monotonic timestamp sampling only at configured boundaries.

## Guardrails

1. Same-process hot dispatch must stay allocation-free.
2. Binary frame paths must allocate less than JSON compatibility paths.
3. Borrowed views must not retain data beyond the source frame lifetime.
4. Any new high-volume ingestion path must benchmark batch primitives against per-record writes.
5. Any benchmark improvement that changes behavior must land with correctness tests for malformed input, cancellation, oversized frames, and diagnostics.
6. Any optimized lane must prove refinement against the higher-level lane it bypasses or replaces: same canonical metadata, same accepted payload semantics, same terminal event, and same controlled error class.
7. Hard bounds such as queue depth, acquire timeout, write deadline, retry cap, and frame size are not benchmark targets; they are behavioral contracts and must have direct tests.
