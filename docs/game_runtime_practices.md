# AAA Game Runtime Practices

Status: baseline
Date: 2026-05-22
Owner: Platform Architecture

## Purpose

This document translates best-in-class real-time game-engine practices into
Foundation runtime, frontend, GPU, media, and native-compute work. Foundation is
not becoming Unreal Engine or Unity, but AAA engines have spent decades solving
the same shape of problem we face in interactive systems: every visible result
has a deadline, every frame has a budget, and late work is often worse than
degraded work.

Use this guide when a Foundation feature has any of these traits:

1. A visual or interactive loop where p95/p99 latency is user-visible.
2. GPU/WebGPU/native compute, media processing, canvas, WebGL, video, audio,
   sensor, camera, or map/scene work.
3. Large runtime assets, textures, model data, embeddings, binary blobs, or
   progressive streams.
4. Repeated work that can be represented as passes over known resources.
5. Platform-specific quality, capability, memory, or power constraints.

## Research basis

Primary sources checked in this pass:

1. Unreal Engine performance profiling and configuration:
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/introduction-to-performance-profiling-and-configuration-in-unreal-engine?application_version=5.6>
2. Unreal Engine Rendering Dependency Graph:
   <https://dev.epicgames.com/documentation/ru-ru/unreal-engine/rendering-dependency-graph?application_version=4.27>
3. Unreal Engine AsyncCompute:
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/asynccompute-in-unreal-engine?application_version=5.6>
4. Unreal Engine Virtual Shadow Maps:
   <https://dev.epicgames.com/documentation/unreal-engine/virtual-shadow-maps-in-unreal-engine>
5. Unreal Engine Nanite:
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/nanite-virtualized-geometry-in-unreal-engine>
6. Unreal Engine PSO caches and precaching:
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/optimizing-rendering-with-pso-caches-in-unreal-engine?application_version=5.6>
   and
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/pso-precaching-for-unreal-engine?application_version=5.6>
7. Unreal Engine shader development:
   <https://dev.epicgames.com/documentation/en-us/unreal-engine/shader-development-in-unreal-engine>
8. Unity profiling and graphics optimization:
   <https://unity.com/how-to/best-practices-for-profiling-game-performance>
9. Unity SRP Batcher:
   <https://docs.unity3d.com/6000.0/Documentation/Manual/SRPBatcher.html>
10. Unity GPU instancing:
    <https://docs.unity3d.com/6000.0/Documentation/Manual/GPUInstancing.html>
11. Unity GPU occlusion culling in URP:
    <https://docs.unity3d.com/6000.0/Documentation/Manual/urp/gpu-culling.html>
12. Unity Burst compilation:
    <https://docs.unity3d.com/6000.0/Documentation/Manual/script-compilation-burst.html>
13. NVIDIA GPU performance event best practices:
    <https://developer.nvidia.com/blog/best-practices-gpu-performance-events/>
14. Microsoft PIX GPU captures:
    <https://learn.microsoft.com/en-us/windows/win32/direct3dtools/pix/articles/gpu-captures/pix-gpu-captures>
15. AMD Radeon Developer Tool Suite and GPUOpen performance material:
    <https://gpuopen.com/gdc-presentations/2023/GDC-2023-Optimizing-Game-Performance-with-the-Radeon-Developer-Tool-Suite.pdf>

## Foundation translation

AAA engines organize real-time systems around frame-time budgets, pass graphs,
resource lifetimes, capture tooling, and platform profiles. Foundation should
translate those ideas this way:

| Game-engine practice | Foundation translation |
| --- | --- |
| Frame time over average FPS | Deadline budgets, p95/p99, and hitch budgets over mean latency |
| Render graph / RDG | Runtime pass graph for staged compute, media, GPU, and streaming work |
| GPU performance markers | Stable low-cardinality spans, pass names, and command markers |
| Draw-call batching / SRP batching | Batch same-shaped work and keep material/config data persistent |
| Instancing | Process many same-schema items with one descriptor, kernel, query, or command |
| Culling / occlusion / LOD | Reduce candidates before expensive work using visibility, interest, and quality tiers |
| Texture streaming / virtual texturing | Progressive binary asset loading with memory pools and visible quality states |
| PSO/shader precaching | Prewarm pipelines, shader modules, WASM, FFI, prepared SQL, and caches before first use |
| Device profiles / scalability | Capability profiles and quality knobs per browser, GPU, device class, tenant, or plan |
| Frame capture / PIX / RenderDoc / RGP | Reproducible trace bundles with hardware, driver, build, and pass metadata |

## Frame budgets

1. Prefer frame time and deadline budgets over average throughput. A 60 FPS loop
   has about 16.67 ms per frame; 120 FPS has about 8.33 ms. Foundation UI,
   canvas, media, and real-time dashboards need comparable wall-clock budgets.
2. Separate steady-state latency from hitches. A rare 200 ms first-use shader,
   WASM compile, cache fill, cold query, asset decode, or GC pause is a product
   bug even when the average is fine.
3. Budget by lane: main thread, worker, render/composite, GPU queue, network,
   database, object store, and decode. Do not hide all costs under one p95.
4. Treat power and thermal throttling as performance constraints. Mobile,
   laptop, and embedded targets should have lower-frequency update modes,
   reduced quality tiers, and adaptive compute budgets.
5. Leave headroom. Profilers, browsers, extensions, OS services, and device
   variability consume part of the frame. A benchmark that barely meets budget
   on a developer machine is not a shipping margin.

## Pass graph discipline

Unreal RDG's useful lesson is whole-frame knowledge: record work, declare
resources, then schedule with known dependencies. Foundation should use the same
shape for complex runtime work.

1. Model heavy interactive work as passes: input, validate, cull, decode,
   transform, compute, upload, dispatch, readback, compose, publish, and clean.
2. Each pass declares resources it reads and writes: arena descriptors, GPU
   buffers, object-store parts, Postgres read models, Redis progress keys,
   media frames, canvases, workers, or sockets.
3. Transient resources need explicit lifetime. Reuse buffers and descriptor
   slots only after the pass graph proves no later pass reads them.
4. Barriers are part of the graph. Cross-worker handoff, GPU queue sync,
   SAB epoch change, database transaction commit, object-store compose, and
   websocket publish all need visible ordering edges.
5. Pass graphs should support validation in development: missing resource
   declaration, read-after-free, write-after-read, cyclic dependency,
   unbounded pass, unsupported capability, or missing fallback is a failure.
6. Do not make every feature a graph. Use this for work that has multiple
   resources, expensive passes, progressive output, or GPU/native boundaries.

## Instrumentation markers

NVIDIA's guidance on GPU performance events maps directly to Foundation spans:
names must be stable, hierarchical, and meaningful across captures.

1. Use stable marker names: `Frame`, `CullVisibleItems`, `UploadArenaBatch`,
   `DispatchScoreKernel`, `ReadbackSummary`, `PublishResults`.
2. Do not include high-cardinality IDs, hashes, timestamps, tenant IDs, request
   IDs, or random suffixes in marker names. Put those in fields.
3. Mark logical work, not every primitive. A marker around every tiny copy,
   mutex, fence, or draw-equivalent creates noise and can distort captures.
4. Keep marker hierarchy aligned with pass graphs so browser traces, server
   traces, GPU captures, and logs can be correlated.
5. Every performance-sensitive Foundation lane should emit:
   - pass name
   - lane name
   - stable feature name
   - item count
   - byte count
   - budget class
   - fallback selected
   - capability profile
   - correlation ID as a field, not part of the marker name

## Data reduction before acceleration

AAA engines win by not drawing what cannot matter. Foundation should not
accelerate work before reducing the candidate set.

1. Frustum culling translates to viewport, subscription, tenant, permission,
   time-window, and interest filtering before decode or compute.
2. Occlusion culling translates to not loading, ranking, rendering, or computing
   items hidden behind current UI state, collapsed panels, offscreen regions, or
   unchanged scene/camera state.
3. LOD translates to summary-first data, coarse aggregates, low-resolution
   previews, approximate vectors, sampled analytics, or progressive detail.
4. HLOD translates to precomputed group summaries, tiles, clusters, partitions,
   and rollups that are refined only when users zoom, drill in, or interact.
5. Overdraw translates to repeated DOM/canvas/layer work, redundant websocket
   updates, repeated JSON materialization, duplicate cache misses, or multiple
   stores reacting to the same event.
6. Use cheap bounds before expensive shapes: bounding boxes, bounding spheres,
   tile IDs, time buckets, tenant partitions, route prefixes, and precomputed
   visibility masks.
7. GPU culling is not always a win. It helps when the setup cost is smaller than
   the avoided work and when the scene has repeated shapes, high occlusion, or
   expensive vertices. Otherwise it can add overhead.

## Batching and instancing

Unity's SRP Batcher and GPU instancing are both forms of state-change control.
The Foundation equivalent is to batch same-shaped work and persist configuration
data instead of re-uploading or re-validating it.

1. Group by schema, material/config, tenant scope, authorization result,
   destination, priority, and fallback lane before dispatch.
2. Keep stable config in persistent buffers, prepared plans, bind groups,
   worker state, compiled kernels, prepared SQL, or cache entries.
3. Update only dirty per-item data. Avoid re-sending full config when one field
   changes.
4. Instancing works best for repeated shape with per-instance variation. In
   Foundation terms: one kernel/query/command can handle many items if the
   schema is the same and per-item variation fits a compact descriptor.
5. Batching can lose diagnostics. Preserve per-item status, error class,
   timing, and correlation linkage inside the batch.
6. Dynamic batching that transforms every item on the CPU can be worse than
   separate work. Benchmark batching, instancing, and direct dispatch separately.

## Shader and pipeline control

AAA games avoid first-use rendering hitches by reducing variants and preparing
pipeline state ahead of time. Foundation has similar risks with shaders, WASM,
FFI, SQL, codec paths, and GPU pipelines.

1. Keep shader/kernel variants scarce. Feature flags, material options, and
   conditional shader code create compile and cache pressure.
2. Prewarm known pipelines during idle or setup: WebGPU pipelines, WGSL modules,
   WASM modules, native kernels, prepared SQL, JSON schemas, protobuf codecs,
   and hot route handlers.
3. Track first-use costs separately from steady-state costs. A path that wins
   after warmup can still be wrong for an interactive first viewport.
4. Pipeline cache misses need visible diagnostics: shader name, variant key,
   device profile, compile time, source fingerprint, fallback used, and user
   impact.
5. Do not rely on runtime string-built shader or kernel source. Checked-in,
   versioned source keeps cache keys reproducible and reviewable.
6. Stripping or pruning variants requires negative tests. If a needed variant is
   removed, the fallback must be explicit rather than silently changing output.

## Asset and memory streaming

Unreal texture streaming and virtual texturing map to Foundation binary and
media assets: load the useful resolution now, refine later, and stay within
memory pools.

1. Large assets need progressive states: placeholder, coarse, useful, refined,
   full, stale, evicted, and failed.
2. Define memory pools for GPU buffers, decoded media, textures, embeddings,
   object-store chunks, WASM arenas, and preview caches.
3. Streaming should have priority lanes: visible viewport, user-blocking,
   imminent interaction, background warmup, and archival.
4. Mip-like behavior is useful beyond textures: summaries before records,
   low-resolution media before full media, top-k before full ranking, compact
   vectors before full embeddings.
5. Prevent popping where it matters. If visible quality transitions are bad for
   the workflow, disable progressive refinement during the interaction and
   re-enable it after the user commits or leaves the critical state.
6. Track pool pressure, evictions, decode time, upload bytes, and visible
   quality state. "Loaded" is too coarse for interactive assets.
7. Background streaming must be bounded. It must not starve command handling,
   auth refresh, websocket pings, or foreground UI work.

## Async compute and parallel lanes

Async compute is useful when it fills genuinely idle GPU capacity. It is harmful
when it hides synchronization, steals bandwidth, or makes captures misleading.

1. Use async compute only when profiles show unused compute units, queue slots,
   bandwidth, or worker capacity during a pass.
2. Declare explicit fences between graphics/render, compute, copy, worker, and
   host readback work.
3. Capture and profile with async lanes both enabled and disabled when measuring
   pass costs. Async overlap can make per-pass timings hard to interpret.
4. Unsupported operations must fail in development and fall back in production.
5. Priority is a budget. High-priority async work must be rare and tied to
   user-visible deadlines.
6. Async work must preserve cancellation and identity switch behavior. Do not
   let work for an old auth/org/session publish into a new visible state.

## Platform profiles and scalability

Unreal device profiles and Unity quality settings translate directly into
Foundation capability profiles.

1. Define capability profiles by browser/runtime, GPU adapter, CPU class,
   memory class, network class, battery/thermal state, and deployment tier.
2. Centralize quality knobs. Do not scatter magic numbers for batch size,
   update interval, resolution, sample count, animation density, or cache size.
3. Quality knobs need semantic names: `preview`, `balanced`, `high`, `offline`,
   `reduced_motion`, `low_power`, `metered_network`.
4. Profile changes should be logged and testable. A device profile must explain
   why it selected a lower resolution, smaller batch, slower cadence, or
   fallback lane.
5. Scalability should degrade detail before correctness. Lower frame rate,
   lower resolution, fewer samples, coarser previews, or delayed refinement are
   acceptable. Tenant/auth/idempotency/ledger semantics are not.

## Capture-backed testing

AAA engine practice is clear: target hardware beats editor assumptions.

1. Test on target hardware and target browsers, not only development machines.
2. Capture a representative frame or interaction when changing GPU, rendering,
   media, canvas, or runtime pipeline code.
3. Captures must include build SHA, browser/runtime version, driver version,
   GPU/CPU model, quality profile, feature flags, and input seed.
4. Use validation layers and debug tools before trusting a failed or flaky
   capture. Invalid graphics/API use can make captures unreliable.
5. GPU captures are not perfectly portable across hardware or driver versions.
   Keep capture metadata and avoid treating one capture as universal proof.
6. Tests should include:
   - first-use warmup
   - steady-state frame loop
   - device loss or fallback
   - resize
   - hidden/offscreen state
   - auth/org switch
   - memory pressure
   - reduced-power profile
   - no-GPU profile

## Hacks worth formalizing

These are useful only when measured and made explicit:

1. Freeze camera/input/state during profiling to reduce nondeterminism.
2. Profile with async overlap disabled to attribute pass cost, then re-enable
   it to measure shipping behavior.
3. Move invariant material/config data into persistent buffers and update only
   per-item data.
4. Prewarm pipelines, shader variants, WASM modules, prepared SQL, and route
   handlers during idle time.
5. Use coarse visibility or interest masks before expensive decode, search,
   ranking, or GPU upload.
6. Convert object arrays to structure-of-arrays before GPU/native compute.
7. Split long work across frames when freshness allows it.
8. Defer high-resolution assets until the item is visible or likely to be
   visible soon.
9. Use small proxy geometry, bounding volumes, compact descriptors, or
   summaries to decide whether full detail is needed.
10. Record a "hitch ledger" for first-use compile, cold cache, asset load,
    database plan, object-store fetch, worker startup, and GC events.
11. Keep profiling marker names stable; put entropy in fields.
12. Compare editor/dev/profiled and production-like builds. Tooling overhead can
    hide or create bottlenecks.
13. Use screenshot/pixel/capture diffing for visual regressions when changing
    rendering, shader, media, or canvas code.
14. Treat upscaling, interpolation, and approximation as quality modes with
    user-visible contracts, not as proof the native path is fast.

## Edge cases

1. Frame-time improvements can regress input latency if work is moved to the
   wrong queue or synchronization point.
2. Culling can create missing data if bounds are wrong. Bounds are correctness
   data, not just performance data.
3. Occlusion using previous-frame depth can be conservative or one frame late.
   Do not use it for security, accounting, or durable state decisions.
4. LOD can hide important state. Use it for visual/detail quality, not for
   authorization, balance, inventory, or command truth.
5. Shader/pipeline cache misses can create hitches long after startup if rare
   states are not captured during warmup.
6. Progressive streaming can create visible popping or stale decisions. Critical
   workflows need a "no popping during interaction" mode.
7. GPU memory pressure may fail as device loss, slow fallback, browser kill, or
   silent quality drop depending on platform.
8. Tool captures can lie when async compute, multithreaded rendering, invalid
   API use, or unsupported queues are involved.
9. Dynamic quality changes can break reproducible tests unless tests pin a
   quality profile.
10. First-frame and first-interaction paths need separate budgets from warmed
    loops.

## Foundation keywords to carry forward

New or strengthened review vocabulary:

1. frame time, frame budget, hitch, frame pacing
2. pass graph, render graph, resource lifetime, transient resource, barrier
3. stable performance marker, capture bundle, frame capture, pass marker
4. culling, occlusion, visibility, LOD, HLOD, overdraw, interest mask
5. batching, instancing, draw-call equivalent, state-change equivalent
6. shader variant, pipeline state, PSO cache, pipeline warmup
7. texture streaming, mip level, asset streaming, memory pool, progressive
   refinement
8. device profile, scalability tier, quality knob, reduced-power mode
9. target-hardware validation, debug layer, GPU validation, capture replay
10. async compute, queue overlap, explicit fence, pass attribution

Foundation-specific terms that must remain authoritative:

1. tenant scope
2. authorization
3. idempotency
4. correlation ID
5. runtime envelope
6. fallback lane
7. bounded operation
8. controlled error class
9. worker ownership
10. Postgres command truth
