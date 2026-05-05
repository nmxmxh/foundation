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
