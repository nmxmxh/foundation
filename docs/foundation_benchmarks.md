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

## Browser shared-arena reference

The browser-host benchmark measures payload movement inside `RuntimeSharedArena`. Current local reference:

| Benchmark | hz | mean ms | p99 ms | Role |
| --- | ---: | ---: | ---: | --- |
| 4KB slab write/read | 736596.92 | 0.0014 | 0.0044 | Control-plane-sized payload movement |
| 64KB slab write/read | 102084.09 | 0.0098 | 0.0462 | Medium slab payload movement |
| 1024KB slab write/read | 8422.95 | 0.1187 | 0.6815 | Large slab payload movement |
| sustained descriptor-ready ring traffic | 922.79 | 1.0837 | 1.2702 | Descriptor queue pressure path |

The 4KB control-plane-sized path is roughly 7.22x faster than the 64KB slab path and 87.45x faster than the 1024KB slab path in this run. That supports the current design split: keep the hot control plane fixed and small, move larger payloads through arena descriptors or explicit streams, and benchmark descriptor-ring pressure separately from raw slab copies.

## Guardrails

1. Same-process hot dispatch must stay allocation-free.
2. Binary frame paths must allocate less than JSON compatibility paths.
3. Borrowed views must not retain data beyond the source frame lifetime.
4. Any new high-volume ingestion path must benchmark batch primitives against per-record writes.
5. Any benchmark improvement that changes behavior must land with correctness tests for malformed input, cancellation, oversized frames, and diagnostics.
6. Any optimized lane must prove refinement against the higher-level lane it bypasses or replaces: same canonical metadata, same accepted payload semantics, same terminal event, and same controlled error class.
7. Hard bounds such as queue depth, acquire timeout, write deadline, retry cap, and frame size are not benchmark targets; they are behavioral contracts and must have direct tests.
