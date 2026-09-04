# GPU Practices

Status: baseline
Date: 2026-05-22
Owner: Platform Architecture

## Purpose

This document defines when Foundation apps may use GPU/WebGPU/native GPU lanes
and how those lanes must prove correctness, portability, and performance. GPU
work is a batch-throughput optimization lane. It is not a replacement for
Postgres command truth, Go request orchestration, tenant checks, authorization,
or ordinary UI control flow.

GPU is an implementation capability, not a separate product model. A product
request should state the operation, data shape, latency/quality target, and
fallback tolerance. Agents and the lane planner determine whether CPU, SIMD,
Rust/WASM, WebGPU, or native GPU is justified, then preserve the benchmark,
parity, device-capability, and fallback evidence. Users need GPU internals only
when those internals change cost, portability, quality, privacy, or correctness.

The main research input is "Optimization Techniques for GPU Programming"
(ACM Computing Surveys, 2023, DOI `10.1145/3570638`), a survey of 450 GPU
optimization papers. Its useful lesson for Foundation is that GPU performance
depends on interacting bottlenecks: memory access, irregularity, parallelism
balance, synchronization, and host interaction. Treat GPU promotion as measured
lane planning, not as a blanket rewrite.

The second local-source pass also parsed the NVIDIA CUDA Programming Guide PDF
and `GPU_programming.pdf` under five lenses for each source: performance,
testing, optimization, edge cases, and Foundation-specific invariants. That pass
made native-GPU failure modes more explicit: asynchronous errors, default-stream
serialization, memory ordering, graph capture invalidation, numeric drift,
managed-memory migration, device capability gates, and driver/runtime
compatibility must be first-class review terms.

The third research pass compared Foundation vocabulary against AAA game-engine
practice from Unreal Engine, Unity, NVIDIA, AMD, and PIX documentation. The
main carryover is that GPU work should be part of a frame/pass/resource graph
with stable markers, target-hardware captures, explicit quality tiers, and
first-use hitch prevention.

## Foundation GPU posture

1. Prefer the existing lane ladder: direct Go for scalar control, Rust/FFI or
   WASM/SAB for deterministic local compute, WebGPU/native GPU only for wide
   homogeneous batches that amortize transfer, dispatch, and readback.
2. Every GPU lane must preserve the same visible contract as its scalar or
   Rust/WASM fallback: metadata, tenant scope, schema version, result semantics,
   diagnostics, terminal state, and controlled error class.
3. GPU kernels must be app-owned runtime units with stable input/output schema
   names. Do not hide product semantics inside shader-only code.
4. The lane planner must expose copy budget, allocation budget, transfer bytes,
   dispatch count, expected latency class, fallback order, and capability
   requirements before GPU is selected.
5. Browser GPU work must run off the React render path. Workers own WebGPU
   device setup, pipeline creation, command encoding, dispatch, readback, and
   fallback coordination. For visual canvas rasterization and offscreen render
   surfaces, see `render_surface_lane.md`.

## Workload fit

Good GPU candidates:

1. Large contiguous numeric, vector, matrix, image, audio, signal, simulation,
   model-inference, scoring, compression, or transform batches.
2. Structure-of-arrays or columnar data where adjacent invocations touch
   adjacent bytes and the result can stay in a GPU/native/runtime buffer.
3. Repeated kernels where pipeline creation, buffer allocation, and tuning can
   be amortized across many dispatches.
4. Offline, background, report, media, telemetry, or runtime compute lanes where
   slightly higher setup latency is acceptable.

Poor GPU candidates:

1. Request handlers, authorization, tenant lookup, policy evaluation, idempotency
   checks, routing, reconciliation, or small scalar validation.
2. Pointer-heavy, branch-heavy, irregular object graphs unless the data can be
   regularized into compact arrays, sorted groups, sparse formats, or bounded
   worklists first.
3. Tiny payloads where copy, dispatch, or readback costs dominate useful work.
4. Any feature that cannot tolerate browser/runtime fallback when GPU is absent,
   reset, throttled, or denied.

## Bottleneck-first workflow

Before implementing a GPU lane:

1. State the bottleneck: global/storage memory bandwidth, memory latency,
   uncoalesced access, branch divergence, load imbalance, synchronization,
   atomics, host-device transfer, kernel launch throughput, or CPU hot loop.
2. State the data shape: row count, column count, byte size, scalar type,
   alignment, stride, null/validity layout, sortedness, and expected reuse.
3. State the selected optimization class: coalesced access, workgroup/shared
   memory, warp/subgroup functions, register blocking, spatial blocking,
   kernel fusion, kernel fission, recompute/precompute, load balancing,
   reduced synchronization, reduced atomics, or auto-tuning.
4. Benchmark the scalar/Rust/WASM baseline before GPU work. GPU speedup claims
   are invalid without the fallback baseline and transfer/readback included.
5. Keep the first GPU version boring and correct. Add shared memory, fusion,
   subgroup operations, or auto-tuned parameters only after profiling identifies
   the bottleneck.

## Data layout and memory

1. Prefer structure-of-arrays layouts for scan-heavy kernels. Arrays of row
   objects usually turn into scattered memory access and branch-heavy decoding.
2. Pack GPU buffers from Foundation arena descriptors or typed columnar batches.
   Keep schema metadata, row count, validity bitmap, offsets, and value buffers
   explicit. Null representation and null-aware reduction semantics are fixed by
   `docs/columnar_null_algebra.md`: nulls stay out-of-band in a bitmap, kernels
   substitute the monoid identity rather than branching per lane, and any
   reduction whose identity is a legal value returns a `(value, count)` pair.
3. Align host-shared and storage-buffer data according to the active API:
   WGSL/WebGPU layout rules in the browser, `wgpu`/Vulkan/Metal rules in native
   Rust, and CUDA alignment rules in CUDA-specific adapters.
4. Optimize for coalesced global/storage-buffer access before exotic math.
   Adjacent lanes should usually read adjacent words and write contiguous
   output.
5. Use workgroup/shared memory only when it reduces global memory traffic or
   enables reuse. Account for bank conflicts, capacity, and barrier cost.
6. Watch register pressure. Loop unrolling, fusion, larger work per thread, and
   temporary arrays can improve arithmetic intensity while reducing occupancy
   through spills or fewer resident workgroups.
7. Do not use mapped/readback buffers as the steady-state data path for hot
   browser work. Keep results on the GPU or runtime arena until the domain
   boundary needs materialized output.
8. Treat unified or managed memory as a portability and ergonomics tool, not a
   free performance path. Page migration, CPU writes to GPU-resident memory,
   oversubscription, prefetch, and usage hints must be measured separately from
   explicit copy lanes.
9. Native memory pools and stream-ordered allocation can reduce allocation
   churn, but reuse policy, peer access, IPC/export rules, and physical memory
   footprint must be bounded and observable.
10. Alignment requirements belong in schema tests. Async copy paths, tensor
    maps, vectorized loads, storage buffers, and mapped memory must reject
    misaligned or incorrectly strided descriptors before dispatch.

## Parallelism and synchronization

1. Treat workgroup size, work per invocation, and dispatch shape as tuning
   inputs. Defaults are placeholders, not architecture facts.
2. Occupancy is a diagnostic, not the goal. Low occupancy can be correct when
   instruction-level parallelism, register blocking, or memory reuse produces
   better throughput.
3. Reduce branch divergence by regrouping data, using sparse formats, splitting
   kernels, or moving conditions out of hot inner loops. Do not hide semantic
   branches that enforce authorization or correctness inside a performance
   transform.
4. Use barriers only at the scope required for correctness. Workgroup barriers,
   storage barriers, atomics, and host-visible queue synchronization have
   different costs and visibility rules.
5. Avoid implicit synchronization assumptions. Use explicit subgroup/warp or
   workgroup primitives when the API requires them.
6. Atomics need contention budgets. Prefer per-workgroup reductions, privatized
   counters, prefix scans, or two-stage aggregation before global atomics.
7. Kernel fusion is useful for reducing memory traffic and launch overhead, but
   it can increase register pressure, reduce occupancy, complicate debugging,
   and break when a global synchronization point is semantically required.
8. Memory fences and ordering scopes are correctness controls. Declare the
   narrowest required scope for host, device, workgroup, subgroup, and peer
   visibility instead of assuming a barrier also orders every memory effect.
9. Default stream or implicit queue semantics must not be hidden in adapters.
   Foundation lanes should use explicit streams/queues/events and document
   ordering edges so an optimization cannot accidentally serialize unrelated
   work.

## Host interaction

1. Include host-device transfer, queue submit, pipeline creation, dispatch, and
   readback in measured latency. Kernel-only timings are incomplete for
   Foundation lane decisions.
2. Default GPU batch outputs should remain GPU-resident when the next consumer
   is another GPU pass. Return a Foundation resource receipt for `GPUBuffer` or
   `GPUTexture` state, and materialize back to runtime arena descriptors only
   when a CPU-visible domain boundary requests it.
3. Reuse buffers, bind groups, pipeline layouts, descriptor IDs, and command
   scaffolding where the API allows. Allocation churn can erase kernel wins.
4. Batch small work into fewer dispatches when semantics allow it. Preserve
   per-record diagnostics inside the batch.
5. Use double buffering or staged transfer pipelines only when profiling shows
   transfer and compute overlap is possible and useful.
6. Native adapters may use pinned/page-locked memory or mapped transfer buffers
   only with explicit lifetime, fallback, and memory-pressure budgets.
7. Browser adapters must treat WebGPU availability as dynamic. Device loss,
   adapter limits, cross-origin isolation, browser support, and power policy can
   change the selected lane.
8. Repeated native launch sequences may use CUDA Graphs or API-specific command
   graphs when the graph shape is stable. Capture invalidation, prohibited
   operations, graph update limits, first-upload cost, and memory-node reuse
   must be part of the benchmark.
9. Async copies, staged pipelines, and tensor-memory accelerators are advanced
   lanes. Require device-capability checks, alignment checks, fallback copy
   paths, and overlap evidence before relying on them.
10. Lazy module loading, JIT compilation, binary cache state, and first-launch
   behavior can skew measurements. Benchmarks must distinguish cold compile,
   warm cache, first launch, and steady-state dispatch.
11. Interactive GPU work should expose pass markers compatible with external
    tools: frame/pass/dispatch/readback names stay stable across runs, while
    correlation IDs and tenant IDs remain fields.
12. Treat WebGPU pipelines, native PSOs, shader variants, bind groups, and
    command graph state as cacheable resources. Prewarm them before the user
    reaches a deadline-sensitive interaction when feasible.
13. GPU culling, occlusion, LOD, and progressive refinement are optimization
    passes, not correctness gates. They can reduce upload/render/dispatch work,
    but they must not decide authorization, command truth, or durable state.
14. Browser uploads from arena-backed bytes should use `GPUQueue.writeBuffer`
    offsets and sizes against stable typed-array views when the region is
    4-byte aligned. Pack first when a region would violate WebGPU write
    validation.
15. Transient GPU buffers may be pooled only behind explicit count limits.
    Pools must key by size and usage, release exposed resources only after the
    caller destroys the Foundation receipt, and remain observable through
    benchmarks.
16. Browser/device timing probes must separate adapter acquisition, device
    acquisition, pipeline warmup, upload, dispatch, optional queue drain,
    explicit materialization, and total wall time. Use
    `measureRuntimeWebGpuDeviceRoundTrip` for this shape; keep
    `GPUQueue.onSubmittedWorkDone()` as a measurement/throttle barrier, not as
    the default steady-state readback path.
17. Device loss is a normal lane event. Browser adapters must observe
    `GPUDevice.lost`, drop resident resource receipts tied to the lost device,
    recreate pipelines and buffers on a replacement device, or fall back to
    WASM/SAB or transferable-worker lanes with the same visible contract.

### Capture Bundle Schema

Every GPU/WebGPU/native-GPU performance claim should leave a capture bundle.
Keep the bundle small enough for review, but complete enough to reproduce the
claim.

Required fields:

1. workload name, schema name, row/count shape, byte size, and quality tier
2. fallback lane and scalar/WASM/Rust parity test command
3. host CPU, GPU adapter, driver/runtime, browser/native backend, OS, and build
   SHA
4. warmup policy, dispatch count, workgroup size, transfer bytes, readback
   bytes, pipeline/cache state, and device limits
5. capture tool and artifact path: browser trace, WebGPU markers, wgpu trace,
   RenderDoc, PIX, Nsight Systems, Nsight Compute, AMD uProf, or ROCProfiler
6. failure evidence for device loss, validation error, unsupported feature, and
   fallback materialization

Do not accept a kernel-only timing as a Foundation lane decision. Host/device
boundary cost and fallback parity are part of the visible runtime contract.

## WebGPU and WGSL rules

1. WebGPU is the browser GPU compute lane. It must remain optional,
   capability-gated, and backed by WASM/SAB or transferable-worker fallback.
2. WGSL shader modules are checked-in code, not user-provided text. Do not
   compile untrusted shader source from product data.
3. Validate all buffer sizes and dispatch dimensions before shader execution.
   Out-of-bounds behavior can terminate invocations, poison collective
   operations, or create dynamic errors.
4. Define buffer structs mechanically from shared schemas where possible.
   Host-side packing tests must assert WGSL alignment, size, stride, and offset
   compatibility.
5. Storage buffers are for large read/write data. Uniform buffers are for small
   read-only parameters. Workgroup memory is for invocations in one compute
   workgroup only.
6. Pipeline creation must be async or prewarmed outside latency-sensitive UI.
   Render paths should receive readiness state, not create GPU resources.
7. A pass that is not the subject of the page requests
   `powerPreference: "low-power"`. `"high-performance"` is a request for the
   discrete adapter, and on macOS it forces a GPU switch for the whole browser
   process; on a phone it biases the driver toward its highest-power operating
   point, which on a thermally constrained device means faster throttling and
   *worse* sustained frame times than the low-power path would have given. A
   background texture behind a headline does not get to make that trade for the
   rest of the browser. Reserve `"high-performance"` for a compute lane or a
   figure the page exists to show.
8. One `GPUDevice` per worker, not one per pass. A device per pass means an
   adapter, a pipeline set, and a lot of driver state per pass; three decorative
   figures on one page become three of each. Negotiate the device once where the
   worker starts and hand it to every pass it serves.

## Device profile and starting tier

A quality ladder only ever descends from where it opens. A surface that opens
every device at its best rung has therefore made every phone discover that it
is a phone by janking — drawing at full resolution, missing cadence, and
stuttering through the demotion count on first paint, which is the one moment
somebody is definitely looking.

The fix is not a better measurement. It is not measuring at all.

1. **A render surface must choose its opening rung from a device prior before
   the first frame, and may only climb from there.** The prior is synchronous
   and costs no GPU work. `@ovasabi/runtime-browser`'s `readDeviceProfile()` and
   `startingTier()` implement this, and `createRenderSurfaceHost` applies them
   by default; `startingTier` on the host options overrides it.
2. **Read the prior on the main thread.** `matchMedia` does not exist in a
   worker, so pointer, viewport and reduced-motion are unreadable there. The
   host chooses the rung and sends it with the canvas.
3. **Signals, all free:**

   | Signal | Meaning |
   | :--- | :--- |
   | `navigator.hardwareConcurrency` | ≤ 4 → phone or low-end laptop |
   | `navigator.deviceMemory` | ≤ 4 → constrained (Chromium only) |
   | `matchMedia("(pointer: coarse)")` | touch-first |
   | `matchMedia("(max-width: 48rem)")` | small viewport |
   | `devicePixelRatio >= 2.5` | phone-class panel, disproportionate fill cost |
   | `prefers-reduced-motion` | a stated preference, not an inference |

4. **Weigh display constraints separately from compute constraints.** Pointer,
   viewport and pixel ratio describe the *panel*; a current phone trips all
   three and outruns a laptop. They may cost at most half the ladder's span.
   Only core count and memory — the signals that say the device is actually
   slow — push past the midpoint.
5. **An unreadable signal is unknown, not unconstrained.** `deviceMemory` is
   Chromium-only. Scoring its absence as "plenty of memory" makes every Safari
   user look fast, which is a browser-sniff wearing a heuristic's clothes.
   Exclude an absent signal from its group's numerator *and* denominator.
6. **The ladder's miss threshold is proportional to the rung's cadence, never a
   flat number of milliseconds.** A flat slack means something different on
   every rung: `cadence + 1000/30` on a 40 Hz rung puts the miss line at
   58.3 ms — 17.1 fps — so a device holding the surface at 20 fps records no
   misses at all and never demotes. Foundation uses `cadenceMs * 1.35`.
7. **Demote on a short run of misses; promote only after a long clean one.**
   Descent that needs two dozen misses per rung spends seconds of visible jank
   getting somewhere the device can hold.
8. **Misses accumulate within a burst and are forgiven by a sustained clean
   run.** Decaying one-for-one lets a device alternating hit and miss fail
   forever without reaching the demote count. Never clearing is the opposite
   bug: a healthy surface pinned at the best rung has no better rung to promote
   to, so nothing resets the tally and unrelated hiccups eventually demote
   hardware that held cadence throughout.
9. **Report the rung a surface actually opened on.** A diagnostic that reports
   tier 0 on `READY` regardless makes every sink believe every device started at
   the top, which is exactly the claim the prior exists to stop being true.
10. **Cap `devicePixelRatio` per surface, and justify the cap for a decorative
    one.** A cap of 2 on a DPR-3 phone still renders 1.24× the panel's logical
    resolution, at the best rung, for something sitting behind text. There is no
    universally right number; a decorative surface that has never asked the
    question is probably paying more fill than it meant to.

## One worker, several surfaces, one device

`renderSurface.ts` has recommended this since it was written — *"a device per
worker means a device per surface, and three devices to draw three things on one
page is three adapters, three sets of pipelines and three lots of driver
state"* — and the SDK's support for it was `ownsWorker: false`, which only stops
a host terminating a worker somebody else is using. That is the easy half. The
hard half is that every surface's `build` reached for its own adapter and its own
device, so three surfaces in one worker got you one thread and still three
devices: the exact thing the arrangement exists to avoid.

`createRenderSurfaceWorker` owns the shared half.

| Surfaces | devices, unshared | devices, shared | listeners, unshared | listeners, shared |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 1 | 1 | 1 |
| 3 | 3 | **1** | 3 | **1** |
| 8 | 8 | **1** | 8 | **1** |

1. **`acquire` runs once per worker, and nothing else may request a device.**
   That is the rule; the module is how it is kept.
2. **Hold the in-flight acquisition, not a flag.** Three surfaces on one page
   mount in the same tick and their `INIT`s arrive back to back. A flag-guarded
   acquisition is found unset three times and requests three devices — the bug,
   reintroduced by the code meant to fix it. The second and third callers must
   await the first one's device.
3. **Acquire lazily.** A page that renders no surface should not have negotiated
   a GPU device. Nothing is acquired until a surface is actually initialised or
   warmed.
4. **Reference-count the release.** A single-surface worker can be terminated
   and the browser reclaims; a shared worker outlives every surface in it, so
   something has to notice when the last one goes. This is the case where a
   leaked device is never reclaimed at all.
5. **Release an acquisition that lands after the last surface left.** Dropping
   the in-flight promise loses a device that is about to arrive and that nothing
   else references. It is the one path where a shared device can leak with no
   way to observe it.
6. **Dispatch by lookup, not by scan.** N servers filtering by name on one scope
   means every message wakes N listeners and N-1 decide it was not for them —
   O(N) per message, paid by every `RESIZE`, `STATE`, `VISIBILITY` and
   `TIER_FLOOR`. Measured, unshared dispatch grows with surface count (16 ns at
   one, 40 at three, 71 at eight) while shared dispatch stays flat. At a single
   surface the map lookup makes sharing marginally *slower*; the arrangement is
   for pages with several.
7. **A surface built without being warmed still lands on the shared device.**
   Warming is optional; sharing is not.

## Prewarming a surface

Standing a WebGPU surface up is a chain: spawn the worker, load its module,
request an adapter, request a device, compile shader modules, create pipelines,
configure the context, draw. **Only the last two need a canvas** —
`getPreferredCanvasFormat()` returns the format without one — and yet the whole
chain used to run after the transfer, which means after a DOM element existed,
which means after the component owning it had mounted. Every millisecond of it
sat on the critical path to first paint because of where it was called from, not
because anything required it to be there.

Measured on the protocol, with the canvas-independent phase at 100 units and the
canvas-dependent phase at 10: first frame lands **110 units after the canvas
cold, 10 units warmed — 91% of the startup chain removed from the critical
path.** The fraction is what transfers to real hardware; the units are a
parameter.

1. **A definition splits its build.** `warm()` does everything that does not
   need a canvas; `build(canvas, warmed)` does the rest and receives the result.
   `gpu_practices` has asked for this since it was written — *"pipeline creation
   must be async or prewarmed outside latency-sensitive UI"* — and the compute
   lane has had `prewarmKernel` all along. This is the raster half.
2. **Prewarm as early as the page knows a surface is coming.** A route's module
   loading, an idle callback, a hover on the link that leads there.
   `prewarmRenderSurface` spawns the worker and starts the warm; the host adopts
   it with `warmed`.
3. **Asking to warm must be free, or nobody will do it speculatively.**
   Measured at **16 ns** per repeat request, and warming is idempotent — a
   second request does not build a second device.
4. **A late `INIT` waits for an in-flight warm; it never restarts it.** Prewarm
   and mount in the same tick is the ordinary case, and a protocol that started
   the work again would make a fast mount *slower* than not warming at all.
   Measured: warming with zero lead costs nothing against a cold start.
5. **A build must stay correct cold.** Prewarming is the caller's choice, so
   `warmed` is `undefined` whenever it was not made, and `build` handles that.
6. **A failed warm is a warning, not a failure.** It costs the latency it was
   meant to save and nothing else; the build runs cold and the surface draws.
   Reporting it as `FAILED` would show an error for a surface that is working,
   one frame later than it might have.
7. **Adopting a warmed worker transfers ownership.** Two references to one
   worker, and only one of them may terminate it — otherwise a caller that
   prewarms, mounts, then tidies up its handle kills a surface that is drawing.

## Releasing what you allocated

GPU resources are the ones no profiler will tell you about. A texture, a buffer,
a compiled pipeline and the device itself live in driver memory, outside the JS
heap — so a leak shows nothing in a heap snapshot, nothing in an allocation
profile, and nothing in any retained-byte figure. It is invisible until the tab
is using a gigabyte, and then it is invisible *why*.

That is the whole reason these are rules rather than hygiene.

1. **Every pass declares a `dispose`, and it is not optional.** A pass with
   genuinely nothing to release writes an empty body, which is a claim that it
   has nothing to release rather than an oversight. Optional disposal is a
   contract that reads as "release if convenient", and the leak it permits is
   the one that cannot be measured.
2. **Every host that acquires a device offers a public teardown.** Releasing on
   device *loss* is not teardown: loss is the one case where the driver has
   already taken the memory back. The case that matters is ordinary — a
   single-page app mounts a workbench, the viewer navigates away — and it must
   release the tracked resources, the pooled buffers, the pipeline cache, and
   the device.
3. **Destroy a device you requested; never destroy one you were handed.** One
   device per worker rather than one per pass is the rule elsewhere in this
   document, so a device will routinely be shared between a compute host and a
   render surface. Destroying somebody else's device out from under them is
   worse than leaking one, so ownership is tracked and only an owned device is
   destroyed.
4. **Disposal is idempotent.** A double unmount is ordinary — React does it in
   development on every mount — and it must not double-free.
5. **A superseded build still disposes.** A pass that compiled a pipeline before
   learning it had been replaced is holding GPU memory that nothing else
   references. The stale build releases it rather than dropping it on the floor.
6. **Terminating a worker is not a disposal strategy.** It works for a worker
   the host owns, because the thread dies and the browser reclaims — which is
   exactly why this is easy to get away with in development. On a shared worker
   the worker outlives the pass and nothing is reclaimed at all.
7. **Verify disposal by counting calls, not by measuring bytes.** There are no
   bytes to measure. The test is that every teardown path calls `dispose`
   exactly once, and every buffer created is destroyed.

## Getting state to a surface

A worker-owned surface has to be told what to draw, and how that crosses is a
performance decision the lane used to make for you.

1. **`postMessage` is a copy, and it is two of them.** A posted message is a
   structured clone: the runtime walks the object and allocates a copy, and the
   worker allocates another on the way out. For a pointer position this is
   free. Measured, a 16KB `Float32Array` — per-entity transforms, a particle
   field, audio levels — costs **3.0 µs and 16.3KB of garbage per frame** on the
   main thread, before the worker has paid for its own copy.
2. **A flat run of numbers that changes every frame belongs in shared memory.**
   `stateChannelElements` on the host options negotiates a `SharedArrayBuffer`,
   and `writeState` publishes a generation. Measured against the clone it
   replaces: **3.0–5.1x faster, 0 bytes allocated**, and the full round trip
   including the worker's take is **3.0–4.4x faster**. `setState` remains
   correct and remains the right tool for anything that is not a flat run of
   numbers.
3. **Shared memory is negotiated, never required.** `SharedArrayBuffer` needs
   cross-origin isolation; a render surface does not. A lane that refused to
   draw without COOP/COEP headers would have forced every consumer to adopt them
   to render a background texture. Where isolation is absent the channel is
   simply absent and the surface behaves exactly as before.
4. **Keep more versions than there are participants.** This is MVCC, and it is
   the rule that makes the reading side free.

   Two buffers selected by `epoch % 2` — the INOS pattern — is safe only while
   the writer and reader run at the same rate. A render surface breaks that on
   purpose: the host writes on `requestAnimationFrame` at up to 120 Hz and a
   paper texture reads at its rung's 4 Hz cadence, so the writer laps the reader
   thirty times between reads and two laps land back on the slot being read.

   A sequence lock repairs the correctness and costs a full copy of the payload
   on every read to do it — the reader has to copy into private memory for the
   check to mean anything. Measured, that was 750 ns per frame at 16KB and
   10.5 µs at 256KB.

   Three slots removes the copy entirely. The writer owns one, the reader owns
   one, and the third is a handoff swapped by a single compare-and-exchange, so
   neither side ever waits and neither ever touches memory the other owns.
   **Taking a generation becomes an index lookup: 36–65 ns, flat across a
   4,096x range of payload sizes**, against a cost that previously grew linearly
   with the payload. The price is one extra slot of memory.
5. **Hand the reader the slot, not a copy of it.** Under two buffers a live view
   is a torn read waiting to happen; under three it is simply correct, because
   the writer cannot reach the slot the reader holds. Cache one view per slot
   and rebuild it only when that slot's length changes, so a surface drawing at
   40 Hz allocates nothing.
6. **Let the caller write into the slot.** `fill` receives the destination
   directly, so a pass that *computes* its state — transforms, a particle field,
   audio levels — writes it once, into its final location. Measured against
   computing into a staging array and copying: **657 ns saved per frame at 16KB,
   7.9 µs at 256KB**, for the same work.
7. **Cross-boundary work is bulk or it is nothing.** Never read or write shared
   memory an element at a time from the other side of a language or thread
   boundary. INOS records three orders of magnitude between the per-float form
   and the bulk form of the same transfer.
8. **Expose the generation so a pass can skip.** A surface reading state that
   only changes on pointer input spends most of its frames with nothing new; a
   take that finds no new generation does no work at all, not even an exchange —
   **29 ns**. A pass whose work depends only on that state should skip it too,
   which it can only do if the lane tells it.

## Native GPU rules

1. Prefer portable Rust `wgpu` adapters for app-owned native GPU kernels unless
   a CUDA, Metal, Vulkan, or OpenCL-specific feature is required and benchmarked.
2. Public native GPU receipts must map to
   `runtime-sdk/protocols/system/v1/runtime_native_gpu.capnp`. Expose only
   opaque descriptor fields such as id, kind, platform, dimensions, format,
   schema name, producer, and fallback. Raw DMA-BUF fds, IOSurface objects,
   Android hardware-buffer pointers, CUDA/Vulkan external-memory handles,
   Metal textures, stream objects, events, and fences remain in
   `runtime-native` or plugin-owned side tables.
3. `runtime-native` owns the private platform handle registry. A registry
   record contains the public descriptor, a private platform handle, a native
   fence snapshot, an owner scope, and a reference count. Snapshots,
   materialization plans, tests, logs, and Cap'n Proto messages must never
   expose the private handle.
4. Native plugins may register Unix fd-backed handles for Linux DMA-BUF,
   Vulkan opaque fd, or CUDA opaque fd style resources, but Foundation snapshots
   expose only handle kind, platform, plane count, and external-sync presence.
   The fd itself remains owned by the registry record and is dropped only after
   the final fence-gated release.
5. IOSurface/Metal, Android `AHardwareBuffer`, CUDA, and Vulkan SDK objects
   enter Foundation Core as opaque plugin handles until their platform adapters
   provide the unsafe SDK boundary. The plugin owns retain/release, device
   compatibility, and imported semaphore/fence behavior; Foundation owns the
   descriptor receipt and lifecycle table.
6. Registry ownership is part of tenant isolation. Acquire, release, and
   materialization operations must validate descriptor id plus owner scope
   before returning a descriptor, mutating a reference count, or planning a
   fallback copy.
7. Registry lifetime is bounded and fence-gated. Record count has a hard cap,
   duplicate descriptor ids are rejected, reference counts must not overflow,
   and final release must fail until the producer/consumer fence is complete.
8. Materialization is an explicit fallback plan: `copy-to-arena`,
   `copy-to-webgpu`, or `cpu-materialize`. A native GPU descriptor receipt does
   not imply CPU-visible bytes.
9. Platform adapters must follow their OS/API ownership and synchronization
   rules. Vulkan external memory normally pairs imported/exported memory with
   matching external fence or semaphore state. Android `AHardwareBuffer`
   requires usage/format support checks and explicit reference ownership.
   Apple `IOSurface`/Metal interop must keep IOSurface/texture lifetime and
   pixel-format compatibility inside the plugin side table.
10. Lane planning may select `native-gpu` only for trusted same-process or
   same-host work with a validated native GPU descriptor and matching platform
   capability. Browser WebGPU remains a separate portable lane.
11. CUDA-specific code must declare compute capability targets, fallback behavior,
   and build flags. Do not expose CUDA-only types across Foundation public APIs.
12. Vulkan/Metal/wgpu compute must query device limits such as workgroup size,
   shared memory, storage buffer size, subgroup support, and queue capabilities
   at runtime.
13. Native GPU profiling should use stable clocks/cache settings where the tool
   supports them, or explicitly report when clocks, thermals, and cache state
   are uncontrolled.
14. Native adapters must query device count, selected device, compute capability
   or feature level, driver/runtime versions, memory limits, peer access,
   cooperative-launch support, graph support, and async-copy support before lane
   selection.
15. CUDA-specific kernels may use launch bounds, maximum register controls,
   `__restrict__`, cooperative groups, warp shuffle/reduce primitives, async
   barriers, or memory pools only behind benchmark evidence and fallback code.
16. Do not expose default stream behavior through Foundation APIs. Adapters must
   own explicit streams, events, synchronization, and error-drain points.
17. Multi-GPU, peer-to-peer, IPC, and external-memory interop are separate lanes.
   They require topology discovery, access-right checks, handle lifetime rules,
   and fallback to single-device execution.
18. CUDA Dynamic Parallelism, device-side graph launch, and thread-block
    cancellation are advanced native-only features. Use them only when they
    reduce a measured launch/scheduling bottleneck and when pending-launch,
    memory-footprint, and cancellation constraints are tested.

## Continuous GPU feedback loop

1. Each GPU optimization pass must record the bottleneck it targets: packing,
   upload, buffer allocation, pipeline creation, bind group churn, dispatch,
   readback, or writeback.
2. Keep the scalar/Rust/WASM baseline and the previous GPU measurement in the
   same benchmark note. A faster kernel is not an improvement if transfer,
   allocation, or readback moved the cost elsewhere.
3. Add one correctness test for every optimization guard: alignment fallback,
   pool bounds, resource lifetime, device-limit rejection, and materialization
   semantics.
4. Promote an optimization from report-only only after it has a benchmark,
   fallback path, and TLA-style refinement note showing the visible contract did
   not change.

## Testing and verification

1. Every GPU lane must have scalar or Rust/WASM parity tests over empty,
   one-item, small, large, boundary, misaligned, and just-over-limit inputs.
2. Floating-point kernels must define tolerance, rounding mode expectations,
   NaN/Inf handling, deterministic reduction strategy, and architecture drift
   risk. Financial kernels must not use floating point.
3. Buffer layout tests must verify byte length, alignment, stride, offset,
   endian assumptions, and invalid descriptor rejection.
4. Rust-owned GPU adapters and native descriptor registries must pass
   `make check-rust`; scaffolded apps inherit the same gate through
   `scripts/checks/check-rust.sh` for app-owned `rust/`, `native/src-tauri/`,
   and vendored Foundation runtime manifests.
5. Race and synchronization tests must cover missing barrier, duplicate write,
   partial batch, canceled dispatch, device loss, and fallback replay where the
   adapter can simulate them.
6. Benchmarks must report baseline lane, transfer bytes, dispatch count,
   kernel time, total wall time, readback time, allocation count, p95/p99, and
   thermal/device details when available.
7. Auto-tuning must be bounded and reproducible. Record the parameter search
   space, chosen parameters, device identity, driver/runtime version, and
   fallback when the tuned configuration is unavailable.
8. Native launch tests must check both launch-time and completion-time errors.
   CUDA-style errors can surface asynchronously, so tests need explicit error
   drains at synchronization points as well as immediate launch checks.
9. Use sanitizer/debug tools where the adapter supports them: race checks,
   initialization checks, synchronization checks, memory access checks, and
   device assertions. Treat sanitizer absence as a reported test gap.
10. Numeric tests must cover fused multiply-add drift, associativity changes,
   reduction order, ULP/tolerance budgets, subnormal handling, NaN/Inf behavior,
   and host/device accuracy differences.
11. Edge-case tests must cover default-stream serialization, graph capture
    invalidation, unsupported operations, lazy-loading first launch, ECC/device
    faults when simulatable, page migration, oversubscription, peer-access
    failure, and memory-pool reuse.
12. Rendering or media GPU changes need capture-backed tests on target hardware
    or target browsers. Record driver/runtime version, adapter, quality tier,
    async overlap state, shader/pipeline cache warmth, and capture tool.

## Security and operations

1. GPU code runs in a hostile input environment. Validate dimensions, offsets,
   lengths, formats, and type tags before buffer upload.
2. GPU resource quotas are denial-of-service controls. Bound buffer sizes,
   dispatch counts, queue depth, in-flight command buffers, and compile attempts
   per session/user/org.
3. Device loss and GPU reset are normal failure modes. Convert them into
   controlled lane fallback or visible retryable errors.
4. Sensitive tenant data must not be mixed in shared browser GPU resources
   across auth or organization changes. Destroy or quarantine buffers on
   identity switch.
5. Diagnostics may include shader name, kernel version, parameter class, byte
   counts, and timing. Do not log raw buffers, embeddings, media frames, or
   secret-bearing payloads.
6. GPU debug and environment toggles such as forced JIT, launch blocking,
   module loading, cache paths, or performance boost controls are operational
   inputs. They must be captured in benchmark metadata and kept out of product
   request semantics.

## Keyword comparison against Foundation

The survey uses terms that Foundation already partially covers under broader
runtime language, but several should become explicit review vocabulary:

1. Already common in Foundation: GPU, WebGPU, kernel, shared memory,
   coalescing/coalesced layouts, atomics, profiling, columnar buffers.
2. Underrepresented and now required in GPU reviews: warp/subgroup, occupancy,
   achieved occupancy, branch divergence, register pressure, launch bounds,
   max-register controls, pinned/page-locked memory, mapped memory, storage
   buffer, default stream, stream/event synchronization, workgroup barriers,
   memory fences, memory ordering, host-device transfer, kernel fusion/fission,
   CUDA Graphs or command graphs, async copy, memory pools, ULP tolerance, and
   auto-tuning.
3. Foundation-specific translation: `warp` maps to API-specific subgroup/warp
   lanes; `shared memory` maps to workgroup memory or native shared memory
   depending on context; `coalesced access` maps to contiguous descriptor and
   structure-of-arrays layout; `host interaction` maps to Foundation copy,
   allocation, dispatch, and fallback budgets.
4. Terms that are deliberately Foundation-owned rather than GPU-source terms:
   tenant scope, authorization, idempotency, correlation ID, runtime envelope,
   fallback, bounded budget, deadline, diagnostics, worker ownership, arena
   descriptor, and schema version. GPU lanes must refine these contracts, not
   replace them.
5. AAA/game-engine terms now required for interactive GPU reviews: frame budget,
   hitch, pass graph, resource lifetime, transient resource, pipeline/PSO
   warmup, shader variant, stable performance marker, capture bundle, culling,
   occlusion, LOD, overdraw, quality tier, device profile, and scalability
   profile.

## References

1. Hijma, Heldens, Sclocco, van Werkhoven, and Bal, "Optimization Techniques for
   GPU Programming", ACM Computing Surveys 55(11), 2023:
   <https://doi.org/10.1145/3570638>
2. NVIDIA CUDA C++ Best Practices Guide:
   <https://docs.nvidia.com/cuda/cuda-c-best-practices-guide/index.html>
3. W3C WebGPU Shading Language:
   <https://www.w3.org/TR/WGSL/>
4. W3C WebGPU:
   <https://www.w3.org/TR/webgpu/>
5. Vulkan Guide, Compute Shaders:
   <https://docs.vulkan.org/guide/latest/compute_shaders.html>
6. NVIDIA Nsight Compute:
   <https://docs.nvidia.com/nsight-compute/NsightCompute/index.html>
7. Local source pass: `/Users/okhai/Desktop/cuda-programming-guide.pdf`
8. Local source pass: `/Users/okhai/Desktop/GPU_programming.pdf`
9. Foundation AAA game-runtime translation: `docs/game_runtime_practices.md`
10. Khronos WebGPU Best Practices:
    <https://www.khronos.org/developers/linkto/webgpu-best-practices>
11. MDN `GPUQueue.onSubmittedWorkDone`:
    <https://developer.mozilla.org/en-US/docs/Web/API/GPUQueue/onSubmittedWorkDone>
12. MDN `GPUDevice.lost`:
    <https://developer.mozilla.org/en-US/docs/Web/API/GPUDevice/lost>
13. Vulkan Guide, External Memory and Synchronization:
    <https://docs.vulkan.org/guide/latest/extensions/external.html>
14. Android NDK `AHardwareBuffer`:
    <https://developer.android.com/ndk/reference/group/a-hardware-buffer>
15. Apple Metal `MTLTexture`:
    <https://developer.apple.com/documentation/metal/mtltexture>
16. Linux kernel DMA-BUF:
    <https://docs.kernel.org/driver-api/dma-buf.html>
17. Linux kernel Sync File API:
    <https://kernel.org/doc/html/next/driver-api/sync_file.html>
18. NVIDIA CUDA external resource interoperability:
    <https://docs.nvidia.com/cuda/cuda-driver-api/group__CUDA__EXTRES__INTEROP.html>
19. Columnar null representation, identity-substitution reductions, and the
    cross-lane bitmap word-width contract: `docs/columnar_null_algebra.md`
