# Render Surface Lane

Status: baseline  
Date: 2026-09-03  
Owner: Platform Architecture  

## Purpose

`gpu_practices.md` section 247 and `performance_practices.md` section 335.7 specify the browser rendering rule:

> Browser WebGPU remains optional and worker-owned. React render paths receive state and results. They do not create devices, compile pipelines, dispatch workgroups, or map readback buffers.

`RuntimeWebGpuHost` manages compute operations including storage buffers, dispatch, readback, and arena upload.
The Render Surface Lane provides the missing canvas rasterization and 2D stage execution capability.

## Components and Responsibilities

The lane consists of four core primitives in `@ovasabi/runtime-browser`:

| Module | Thread | Responsibility |
| :--- | :--- | :--- |
| `renderSurface.ts` | Main | Transfers offscreen canvas, observes element size and visibility, forwards coalesced state. |
| `renderSurfaceClient.ts` | Worker | Owns graphics context, executes loop, maintains quality ladder, calls pass draw function. |
| `canvasStage.ts` | Main | Provides 2D canvas execution without layout thrashing, with observer culling and cadence gating. |
| `frameClock.ts` | Main | Unifies multiple animation loops into one pulse-scheduled browser animation frame. |
| `renderMarks.ts` | Main/Worker | Records performance marks and diagnostics under `game_runtime_practices.md` section 121. |

## Execution Cadence and Clock Ownership

Dedicated workers lack native `requestAnimationFrame` support.
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

- Demotes one rung after 24 late frames.
- Promotes one rung after 180 on-time frames.
- Reuses a pre-allocated `RenderSurfaceFrame` descriptor to eliminate heap allocations during 60 or 120 FPS ticks.

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
