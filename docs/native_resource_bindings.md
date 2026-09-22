# Native Resource Bindings

Status: implemented and validated on the reference workstation
Date: 2026-09-22
Owner: Platform Runtime

## Purpose

Bind bounded computations to resources where their data resides.
Local servers use native views. Remote callers send versioned references through the existing frame transport.
Foundation preserves authorization, correlation, input types, resource generations, and output limits across both paths.

## Scope

The first implementation covers synchronous computations over immutable published resource versions.
Resource publication and binding replacement are explicit host operations.
An active computation retains its original resource and binding revisions.
Applications retain ownership of durable writes and their idempotent event lifecycle.

Existing runtime APIs and wire formats remain available.
New binding requests use a separate event family and compiled Cap'n Proto records.
The 4KB control buffer and existing arena offsets remain unchanged.

## Contracts

1. Resource identity includes a registry, resource identifier, generation, and payload type.
2. A binding connects an authorized resource to a registered operation and explicit budgets.
3. Binding replacement compares the expected revision before publication.
4. Every execution checks authorization before exposing resource data.
5. Local execution borrows input data and writes into caller-owned output storage.
6. Remote execution transfers the request and bounded result, without transferring the resident input.
7. Admission rejects stale generations, unsupported types, excess output capacity, input copy violations, and exhausted concurrency.
8. A fallback must satisfy the same requirements and report its actual transport.

## Ownership And Bounds

Published resource bytes remain immutable until all admitted readers finish.
Resource replacement publishes a new generation. Existing readers finish against their pinned generation.
Retired versions remain part of the resident memory budget until their readers release them.
Registries bound resources, bindings, resident bytes, and active executions.
The resident budget counts payload bytes, including retired generations. It excludes allocator overhead and driver memory.
Publication needs capacity for both generations until replacement completes.
Use a distinct registry identifier for each owner incarnation. Reusing identifiers after restart can revive stale references.
Native operation implementations must honor cancellation and declared input bounds.
A caller timeout does not prove termination of arbitrary native code.

## Distributed Authority

Resource owners remain authoritative for their resources and bindings.
Remote placement reports are advisory. The destination validates each request against current local state.
Authentication resolves the caller before object authorization.
Neither a resource identifier nor a reported tenant grants access.
Connection loss does not authorize an automatic replay of a mutating operation.

## Performance Boundary

Copy accounting covers Foundation-managed payload movement.
Each trusted implementation declares its complete input copies through `BindingSpec.InputCopies`.
`InputCopiesKnown` requires an explicit declaration. Registration rejects unknown copy costs.
Admission compares that cost with `RuntimeBindingRequest.MaxInputCopyBytes` before executing the operation.
The receipt reports the admitted copy cost. Arbitrary native code cannot be instrumented by this descriptor alone.
Operation internals, type validators, and opaque driver behavior require separate benchmarks and adapter evidence.
Publication costs are measured separately from steady-state execution.
Local calls do not encode Cap'n Proto messages.
Cross-process calls use generated codecs with bounded frames.
The request occupies 104 bytes. The receipt occupies 72 bytes.
An eight-byte result transfers 184 application payload bytes, independent of resident input size.
Transport headers, encryption, and retransmissions are outside this transfer count.
Dispatch can avoid allocations when the context already contains normalized metadata with the same correlation identifier.
Changing the correlation identifier requires a new context. Payload validators can also allocate.

## Native And Remote Integration

`server-kit/go/placement` owns `BindingRegistry`, `BindingFrameHandler`, and `ExecuteRemoteBinding`.
Register payload types with bounded native validators when constructing the registry.
Publish input through `PublishResource`, then register its operation through `Bind`.
Both methods require authenticated organization context and correlation metadata.
Use the returned request for local execution or the existing authenticated frame connection.

```go
request, err := registry.Bind(ctx, spec, expectedRevision)
if err != nil {
    return err
}
receipt, err := registry.Execute(ctx, request, correlationID, output)
```

Register `BindingFrameHandler(registry)` under `compute:binding:requested` in the existing `grpcsvc.Router`.
The handler returns `compute:binding:success` or `compute:binding:failed` with the same correlation identifier.
The authentication middleware must bind the organization to trusted credentials.
The required object authorizer checks resource access on every publication, binding change, and execution.
Redis placement reports can select a configured owner connection. They cannot supply authority or arbitrary endpoints.
Stale references fail explicitly. The caller obtains a current binding before choosing another attempt.

`runtimehost.NewFFIBinding` adapts an existing `FFIPool` and unit identifier.
It uses `ExecuteInto` with caller-owned output storage.
The adapter declares one input copy into the existing control buffer.
A zero-input-copy request therefore fails before entering the native unit.
The caller retains ownership of the native pool and its shutdown lifecycle.

The initial registry holds host memory. Device buffers require an adapter with verified ownership, synchronization, and driver support.
The implementation does not introduce shared mutable memory across machines or distributed write consensus.
Resource replacement remains authoritative at its owner.

## Graphics Integration

`RenderSurfaceHostOptions.requirements` supports required GPU completion, required shared state, and a backing pixel limit.
The worker checks the selected pass before reporting `READY`.
The host checks the returned evidence, including responses from older workers.
Required completion failures and timeouts stop the pass. They cannot silently resume submission without a completed frame.
Pixel limits apply to backing dimensions after device ratio, resize, and quality changes.
Requirements remain pinned until the host is replaced.

Existing callers can omit requirements and retain their current fallback behavior.
Main-thread fallback remains application-owned. Applications must validate its requirements separately.
Shared state uses the existing channel and its generation protocol.
The shared channel copies published state; sharing does not imply that every operation performs zero copies.

Ovasabi's paper and black-hole passes now forward `device.queue.onSubmittedWorkDone()` through `settled`.
Their shaders and WebGL fallback remain unchanged.
The existing Foundation worker can use these callbacks without a vendor refresh.
The new strict requirements are available after adopting the updated Core package.
Pronto and Ovasabi now contain that package. See [Application Adoption Evidence](application_adoption_2026-09-22.md).
The running application container was not rebuilt during this validation.
The [FPS refinement](graphics_fps_refinement.md) records subsequent scheduling improvements at fixed visual quality.

## Schema Compatibility

Cap'n Proto defines the typed scalar records. Go, Rust, and TypeScript codecs derive their layouts from compiler output.
The codecs use the standard single-segment encoding with a fixed version-one layout.
They reject other layouts and unsupported versions. Format changes require explicit version negotiation.
JavaScript identifiers use `bigint` to preserve all 64 bits.
Compiler-produced fixtures verify cross-language encoding.

Runtime schemas contained duplicate identifiers, one invalid identifier, and unsupported constant names.
Those declarations now compile together.
Existing exported numeric constants and buffer offsets are unchanged.
Explicit struct identifiers preserve prior valid identities where file identifiers changed.
The generated manifest now recognizes those explicit identifiers.
Schema constant names in the manifest follow the corrected source spelling.
Existing protobuf contracts and runtime entry points remain available.

The encoding rules follow the [Cap'n Proto specification](https://capnproto.org/encoding.html).

## Validation

- Compile every runtime schema and preserve generated constant values.
- Compare generated codecs with the Cap'n Proto compiler.
- Test stale references, conflicting revisions, authorization failures, and output bounds.
- Test replacement during active execution and release after failures.
- Compare direct, frame, and real gRPC execution with an independent result oracle.
- Exercise distributed discovery through real Redis and source data through PostgreSQL.
- Measure dispatch overhead, payload movement, allocations, and representative computation costs.
- Verify existing runtime tests and generated-project compatibility.

## Evidence Ledger

| Item | Evidence |
| :--- | :--- |
| Public contract | Additive binding records, registry APIs, native adapter, and optional graphics requirements. |
| Invariant | Current authorization, payload types, generations, and budgets gate every execution. |
| Scope | Owner memory, native FFI boundary, remote frame transport, and browser render worker. |
| Mutation | Concurrent revision conflicts fail. Active work retains its original input and implementation. |
| Fallback | Existing lanes remain available. Required capabilities reject unsupported implementations. |
| Regression guards | Race tests, malformed-frame tests, allocation guard, compiler fixtures, and GPU lifecycle tests. |
| Reference applications | Pronto's real Rust FFI unit; Ovasabi's real black-hole pass and paper completion tests. |
| Documentation | This contract, reproducible commands, raw measurements, and reference application handover. |

The full PostgreSQL/Redis service suite passed with the race detector.
It exercised source records, Redis placement reports, authenticated TCP execution, and stale reference rejection.
Placement coverage reached 96.9%. The native adapter reached 100% coverage.
Runtimehost coverage reached 88.0%, above its existing 87.9% floor.
Core browser tests, Rust tests, schema compilation, type checks, and relevant practice checks passed.
Ovasabi passed twenty targeted tests against the new Core package.
Its test process reported existing style-plugin warnings and a delayed Vite shutdown.

An initial full benchmark sweep encountered a parallel PostgreSQL pool timeout.
The full service race suite passed again, followed by five successful focused binding benchmark samples.
No production data or running application containers were modified by the service harness.

## Measurements

The reference machine runs macOS on an Apple M1 Pro.
These measurements describe this workstation and these implementations.
They do not establish device-independent latency guarantees.
Local validation used Go 1.26.6, Rust 1.92.0, Cap'n Proto 1.5.0, and Node 24.1.0.
Rust 1.95 validation remains a CI requirement. It was not available locally.

| Operation | Median | Allocation evidence |
| :--- | ---: | :--- |
| Eight-byte checked resident execution | 0.738 microseconds | Zero allocations with prepared correlation metadata. |
| Native execution after service preparation | 0.217 microseconds | Zero allocations. |
| Authenticated loopback gRPC execution | 97.9 microseconds | 217 allocations, about 15.6 KB per operation. |
| Pronto direct Rust FFI | 0.676 microseconds | Zero allocations. |
| Pronto checked Rust binding | 0.862 microseconds | One eight-byte allocation in the existing response validator. |

The native binding adds checks; it does not make the underlying kernel faster.
The distributed benefit is keeping input resident and moving bounded requests and results.
The final resident dispatch samples ranged from 0.217 to 0.933 microseconds.
These separate runs do not provide a stable latency ratio between the service and isolated benchmarks.

The larger fixture sums one mebibyte of bytes through the same native operation.
The inline comparison additionally encodes and decodes the existing compute ticket.
Publication occurs separately from execution.

| One-mebibyte operation | Median | Allocations per operation |
| :--- | ---: | :--- |
| Direct computation | 0.403 milliseconds | Zero. |
| Checked resident computation | 0.417 milliseconds | Zero. |
| Inline ticket and computation | 0.630 milliseconds | Seven, about 2.38 MB. |
| Resource publication | 0.069 milliseconds | Two, about 1.05 MB. |

Each row contains five samples. These measurements exclude network latency.
The large-resource regression verifies an eight-byte result using 184 application payload bytes across the remote frame path.

Ovasabi's warm samples used a 1280 × 720 viewport with device ratio two.
Both modes completed 200 draws in approximately five seconds at 999,507 backing pixels.
Submission pacing reached two outstanding frames. Required completion held the maximum at one.
Observed completion latency was 9.87/15.17 milliseconds at p50/p95 before admission enforcement.
The matching enforced sample measured 8.15/13.80 milliseconds.
Each mode has one warm sample. These values do not establish a statistically reliable speedup.
A separate cold sample had a 246.72 millisecond p95 and reduced its backing size.
No phone GPU was measured. The timing includes submission and queue completion, rather than hardware timestamps.

Raw evidence:

- [Core benchmarks](evidence/native_bindings_2026-09-22/core_benchmarks.txt)
- [Service benchmarks](evidence/native_bindings_2026-09-22/service_benchmarks.txt)
- [Pronto native benchmarks](evidence/native_bindings_2026-09-22/pronto_native.txt)
- [Graphics samples](evidence/native_bindings_2026-09-22/graphics_samples.json)

## Reproduction

```sh
make check-runtime-contract-field-drift
cd server-kit/go
go test -race ./placement ./grpcsvc
go test -run '^$' -bench BenchmarkBinding -benchmem -count=5 ./placement
```

Run these commands from Core for the service and application checks:

```sh
SERVICE_BACKED_BENCH_PATTERN=BenchmarkServiceBackedResidentBinding \
SERVICE_BACKED_BENCH_COUNT=5 bash tests/service_backed_foundation_test.sh

make test-pronto-bindings PRONTO_PROJECT=/absolute/path/to/pronto_v1
make graphics-binding-lab OVASABI_PROJECT=/absolute/path/to/ovasabi_v1
```

Pronto's reference script uses a temporary Go overlay and the actual native library.
It leaves application sources and vendored modules unchanged.
The graphics lab binds only to loopback port 5187 and runs bounded samples.
It imports Ovasabi's application pass through the existing package boundary for Foundation.
Use its controls to compare pacing and pixel budgets, then save the visible evidence.
Stop the Vite process after the session.
