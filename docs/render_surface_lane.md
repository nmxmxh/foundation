# Render Surface Lane

Status: baseline  
Date: 2026-09-03  
Owner: Platform Architecture  

## Purpose

`gpu_practices.md` and `performance_practices.md` specify the browser rendering rule:

> Browser WebGPU remains optional and worker-owned. React render paths receive state and results. They do not create devices, compile pipelines, dispatch workgroups, or map readback buffers.

`RuntimeWebGpuHost` manages compute operations including storage buffers, dispatch, readback, and arena upload.
The Render Surface Lane provides the missing canvas rasterization and 2D stage execution capability.

## Components and Responsibilities

The lane exposes these primitives in `@ovasabi/runtime-browser`:

| Module | Thread | Responsibility |
| :--- | :--- | :--- |
| `renderSurface.ts` | Main | Transfers offscreen canvas, observes element size and visibility, forwards coalesced state. |
| `renderSurfaceClient.ts` | Worker | Owns graphics context, executes loop, maintains quality ladder, calls pass draw function. |
| `canvasStage.ts` | Main | Provides 2D canvas execution without layout thrashing, with observer culling and cadence gating. |
| `frameClock.ts` | Main | Unifies multiple animation loops into one pulse-scheduled browser animation frame. |
| `renderMarks.ts` | Main/Worker | Records performance marks and diagnostics under `game_runtime_practices.md` section 121. |

## Execution Cadence and Clock Ownership

Dedicated workers can support `requestAnimationFrame` when their owner chain includes a window.
Check capability before selecting that clock. See the [HTML animation specification](https://html.spec.whatwg.org/multipage/imagebitmap-and-animations.html#animation-frames).
Decorative and simulation passes require targeted execution cadence rather than raw display refresh rates.
`serveRenderSurface` paces execution against the current ladder tier `cadenceMs` and corrects for cumulative drift.
Driving frame updates from the main thread would overload the main queue and introduce unwanted coupling.

`frameClock.ts` provides the main-thread clock. It listens to the Foundation pulse worker and schedules a single browser frame.

## Quality Ladder

`RenderSurfaceQualityTier` contains only three fields:

- `scale`: Render scale fraction relative to CSS pixels.
- `cadenceMs`: Target interval in milliseconds between rendered frames.
- `detail`: Optional sample or step computation budget.

Moving between ladder rungs adjusts resolution, cadence, and computation budget without altering underlying scene state.
`renderSurfaceClient.ts` monitors achieved frame intervals:

- A frame is late past `cadenceMs × 1.35`.
- Demotes one rung after 6 misses; a clean run of 48 forgives earlier misses.
- Promotes one rung after 240 on-time frames.
- Reuses a pre-allocated `RenderSurfaceFrame` descriptor to eliminate heap allocations during 60 or 120 FPS ticks.

### GPU backpressure

A WebGPU `draw` returns at `queue.submit`, before the GPU has done the work, so a loop that times `draw` cannot see GPU overload.
Measured on real hardware: 101 frames queued and seconds of latency while the loop reported 40 Hz on rung zero.

- A pass that sets `settled` (for WebGPU, `() => device.queue.onSubmittedWorkDone()`) keeps at most one frame in flight.
- A tick that finds the previous frame unsettled draws nothing and counts as a miss, so GPU overload demotes like CPU overload.
- The loop stops waiting after 2 s, so a promise that never resolves cannot freeze the surface.
- WebGL2 fence completion is verified in the lab. Production backpressure still requires a pass-owned `settled` callback.

### Reporting from a pass

`draw` returns `void` by design. It is a fire-and-forget call on the frame's hot
path, and a pass that returned a value would make every frame allocate and every
caller decide what to do with it.

A pass therefore has no way to report what it measured, and the diagnostics
channel had no slot for it. Numbers a surface computes stayed inside the worker,
where a page-side reader cannot reach them.

- `RenderSurfaceDiagnostics.detail` is the application-owned slot.
- `RenderSurfacePass.detail` is the optional accessor that fills it.
- Both are optional. A pass that sets neither costs nothing and behaves exactly
  as before.

`detail` carries names, numbers, booleans and `null` only. Free text at frame
rate turns a diagnostics channel into a log transport, so the type refuses it.
The worker copies the record, drops values a structured clone would refuse, and
keeps at most 32 keys, so a misbehaving accessor cannot grow a message without
bound. An accessor that throws is swallowed: diagnostics never stop a surface
from drawing or from reporting.

### Diagnostics cadence

By default the worker reports **on ladder movement only**: a rung change, or a
new floor. That is the right frequency for a low-cardinality lane summary.

`detail` is per-frame information, so it needs a clock rather than an event:

- `diagnosticsIntervalMs` on the host asks the worker for periodic reports.
- It is **off by default**. A host that sets nothing sees no extra messages.
- The interval is floored at the settled cadence, so it can never become a
  per-frame stream. `diagnosticsIntervalMs: 1` against a 25 ms rung refreshes at
  25 ms.
- The floor is re-read whenever the settled cadence moves, including a
  `TIER_FLOOR` change, so a pinned surface does not keep reporting at the rate of
  the rung it was pinned away from.
- The timer is cleared on stop, so an unmounted surface stops posting.

The cost per interval is one `postMessage` and one structured clone. It is not a
redraw, and it does not touch the ladder.

### Render marks and correlation

`markLane` publishes lane facts to `performance.mark`, in-memory snapshots, and `window.__ovasabiRender`.
`LaneFacts.correlation` holds an optional correlation identifier for the state read that caused the pass.
The field is `null` or omitted when a pass executes without a backing state read.
`correlation` remains distinct from `reason` to prevent overloaded strings during incident investigations.

## Graceful Degradation

`OffscreenCanvas` and `transferControlToOffscreen` are one-way browser operations.
`createRenderSurfaceHost` probes browser capabilities prior to transfer:

- `worker` mode: Canvas control transfers to the worker. Caller must not draw from main thread.
- `main-thread` mode: Canvas control remains on main thread when capabilities are missing or transfer fails.

> [!NOTE]
> When `transferControlToOffscreen` throws an exception, `createRenderSurfaceHost` catches the error.
> The host records the failure in `issues` and returns in `main-thread` mode safely.
> [!WARNING]
> If a worker pass fails after transfer succeeds, `onFailed` notifies the consumer.
> Because transferred control cannot reverse, the consumer must render an alternative UI element.

## Shared Worker Architecture

A worker owns a GPU device. Creating one worker per surface creates redundant devices and driver state.
Pass `ownsWorker: false` to share one worker across multiple render surfaces.
The host unregisters its listener on disposal without terminating the shared worker instance.

Key design requirements:

- Listeners use `addEventListener` instead of `onmessage` to prevent handler overwrite bugs.
- The `STOP` command retires the individual pass while keeping the worker server running.
- A generation counter ignores completed asynchronous pipeline builds that were superseded by newer init commands.
- `createRenderSurfaceWorker` acquires one device for every surface it serves, and releases it once no pass is built or building for `releaseWhenIdleMs` (default 10 s). Registrations made with `serve()` last for the worker's life and do not hold the device.

## Pulse Worker Resolution

`createPulseManager` includes a default factory for internal package tests.
Bundlers in consumer projects cannot resolve package-internal worker asset paths.
Consumers must provide `createWorker`:

```ts
import '@ovasabi/runtime-browser/pulse.worker';

createPulseManager({
  createWorker: () => new Worker(new URL('./pulse.worker.ts', import.meta.url), { type: 'module' }),
});
```

The package exports `./pulse.worker` directly to support this pattern.
