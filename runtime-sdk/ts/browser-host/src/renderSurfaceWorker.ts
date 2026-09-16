import {
  serveRenderSurface,
  type RenderSurfaceDefinition,
  type RenderSurfaceFrame,
  type RenderSurfacePass,
  type RenderSurfaceScope,
} from "./renderSurfaceClient";

/** See `RenderSurfaceWorkerOptions.releaseWhenIdleMs`. */
export const DEFAULT_RELEASE_WHEN_IDLE_MS = 10_000;

/**
 * One worker serving several surfaces, on one device.
 *
 * ## The arrangement this lane has recommended and never supported
 *
 * `renderSurface.ts` has said it since it was written: *"One worker serving
 * several surfaces is the arrangement that matters for GPU work: a device per
 * worker means a device per surface, and three devices to draw three things on
 * one page is three adapters, three sets of pipelines and three lots of driver
 * state."* `gpu_practices.md` states it as a rule — *one `GPUDevice` per worker,
 * not one per pass*.
 *
 * The SDK's support for it was `ownsWorker: false`, which stops the host
 * terminating a worker somebody else is using. That is the easy half. The hard
 * half is that every surface's `build` reached for its own adapter and its own
 * device, so putting three surfaces in one worker got you one thread and still
 * three devices — the thing the arrangement exists to avoid.
 *
 * This owns the shared half. `acquire` runs **once per worker**, lazily, on
 * whichever surface needs it first, and every surface's `build` receives the
 * result. `release` runs once, after the last surface is retired.
 *
 * ## Why the acquisition is a promise, not a value
 *
 * Three surfaces on one page mount in the same tick and their `INIT` messages
 * arrive back to back. If acquisition were a value guarded by a flag, all three
 * would find it unset and request three devices — the exact bug, reintroduced by
 * the code meant to fix it. Holding the in-flight promise makes the second and
 * third callers wait for the first one's device instead of asking for their own.
 *
 * ## Reference counting, because a shared device has no other owner
 *
 * A single-surface worker can be terminated and the browser reclaims. A shared
 * worker outlives every surface in it, so something has to notice when the last
 * one goes — and that is the case where a leaked device is never reclaimed at
 * all. The count is the whole reason `release` exists.
 *
 * ## Dispatch is a lookup, not a scan
 *
 * `serveRenderSurface` filters by surface name, so N servers on one scope means
 * every message wakes N listeners and N-1 of them decide it was not for them.
 * One listener and a map makes that O(1) instead of O(N), and it is the same
 * tested loop underneath: each surface is served through a proxy scope that
 * records its handler rather than attaching a listener of its own.
 */

/** A surface that draws on a device it did not create. */
export type SharedRenderSurfaceDefinition<TState, TShared> = {
  /**
   * Canvas-independent setup *for this surface*, on the shared resource.
   *
   * The shared acquisition has already happened by the time this runs — that is
   * the expensive part, and it happens once for the worker. Use this for
   * whatever is per-surface and still canvas-independent: a pipeline, a bind
   * group layout, a shader module particular to this figure.
   */
  warm?: (shared: TShared) => Promise<unknown> | unknown;
  /** Build the pass, on the shared resource. */
  build: (
    canvas: OffscreenCanvas,
    shared: TShared,
  ) => Promise<RenderSurfacePass<TState> | null> | RenderSurfacePass<TState> | null;
};

export type RenderSurfaceWorkerOptions<TShared> = {
  /**
   * Negotiate the shared resource. Called at most once per worker.
   *
   * This is where the adapter and device are requested. Nothing else in the
   * worker may request them, which is the point.
   */
  acquire: () => Promise<TShared> | TShared;
  /**
   * Release it, once no surface is using it.
   *
   * A shared worker outlives its surfaces, so nothing else will. `device.destroy()`
   * belongs here.
   */
  release?: (shared: TShared) => void;
  /**
   * How long the resource outlives the last live pass. Defaults to 10 seconds.
   *
   * A served surface is registered for the life of the worker — `serve` runs at
   * module load and nobody disposes it — so releasing only when a registration
   * is disposed meant releasing never. Measured on real hardware (frontend lab,
   * `gpu` lane): three hosts disposed, every pass retired, the device alive.
   *
   * So release follows the *passes*: when the last one is retired (a host's
   * `STOP`, a superseded build), a timer starts, and a pass built before it
   * fires keeps the device. The window is there because re-acquiring is not
   * free — the lab measured first frame at 28–37 ms cold against 7–11 ms on a
   * warm device on an M1 Pro, and adapter negotiation on a phone is slower — and
   * the ordinary unmount is followed by a mount: React StrictMode, a route
   * change and back. `0` releases on the next task; `Infinity` keeps the
   * resource for the worker's life, which was the previous behaviour.
   */
  releaseWhenIdleMs?: number;
  /** The worker global. Injected for tests. */
  scope?: RenderSurfaceScope;
};

export type RenderSurfaceWorker<TShared> = {
  /**
   * Serve one surface. Returns a disposer for that surface alone.
   *
   * Disposing the last served surface releases the shared resource.
   */
  serve: <TState>(
    surface: string,
    definition: SharedRenderSurfaceDefinition<TState, TShared>,
  ) => () => void;
  /** How many surfaces are currently served. */
  readonly size: number;
  /** Whether the shared resource has been acquired. */
  readonly acquired: boolean;
  /** Retire every surface and release the shared resource. Idempotent. */
  dispose: () => void;
};

export const createRenderSurfaceWorker = <TShared>(
  options: RenderSurfaceWorkerOptions<TShared>,
): RenderSurfaceWorker<TShared> => {
  const scope = options.scope ?? (globalThis as unknown as RenderSurfaceScope);
  const handlers = new Map<string, (event: MessageEvent) => void>();
  const stoppers = new Map<string, () => void>();

  let sharing: Promise<TShared> | null = null;
  let acquiredValue: TShared | undefined;
  /*
   * A separate flag rather than `acquiredValue !== undefined`: `TShared` is the
   * caller's type and `undefined` may be a perfectly good device handle in a
   * test double or a null-object implementation. Using the value as its own
   * sentinel would silently skip releasing it.
   */
  let settled = false;
  let disposed = false;
  /*
   * Passes built or building on the shared resource. A build in flight counts:
   * releasing while it awaits would hand it a device about to be destroyed.
   */
  let live = 0;
  let idleTimer: ReturnType<typeof setTimeout> | null = null;
  const idleMs = options.releaseWhenIdleMs ?? DEFAULT_RELEASE_WHEN_IDLE_MS;

  const cancelIdle = () => {
    if (idleTimer === null) return;
    clearTimeout(idleTimer);
    idleTimer = null;
  };

  /*
   * One in-flight acquisition, shared by everyone who asks while it runs.
   * Three surfaces mounting in the same tick is the ordinary case, and each of
   * them requesting its own device is the bug this module exists to remove.
   */
  const share = (): Promise<TShared> => {
    sharing ??= Promise.resolve(options.acquire()).then((value) => {
      acquiredValue = value;
      settled = true;
      return value;
    });
    return sharing;
  };

  const onMessage = (event: MessageEvent) => {
    const surface = (event.data as { surface?: unknown } | undefined)?.surface;
    if (typeof surface !== "string") return;
    // A lookup, not a scan across every surface in the worker.
    handlers.get(surface)?.(event);
  };
  scope.addEventListener("message", onMessage);

  const releaseIfEmpty = () => {
    if (handlers.size > 0 || !sharing) return;
    releaseShared();
  };

  /** No pass left: release after the idle window, unless one is built first. */
  const scheduleIdleRelease = () => {
    if (disposed || live > 0 || !sharing || !Number.isFinite(idleMs)) return;
    cancelIdle();
    idleTimer = setTimeout(() => {
      idleTimer = null;
      if (live === 0 && sharing) releaseShared();
    }, Math.max(0, idleMs));
  };

  const releaseShared = () => {
    cancelIdle();
    const pending = sharing;
    if (!pending) return;
    const value = acquiredValue;
    const wasSettled = settled;
    sharing = null;
    acquiredValue = undefined;
    settled = false;

    if (wasSettled) {
      options.release?.(value as TShared);
      return;
    }
    /*
     * Still in flight. Dropping the promise here would leak the device that is
     * about to arrive — nothing else holds a reference to it, and the surface
     * that asked for it has already gone. So the release is chained onto the
     * acquisition rather than skipped, which is the one path where a shared
     * device can be leaked without anybody being able to see it.
     */
    void pending.then((late) => options.release?.(late)).catch(() => undefined);
  };

  return {
    serve(surface, definition) {
      if (disposed) return () => undefined;

      /*
       * A proxy scope. `serveRenderSurface` wants to own a listener; here it
       * gets a handler slot instead, so all the loop, ladder, state-channel and
       * disposal behaviour underneath is the code that is already tested rather
       * than a second implementation of it.
       */
      const proxy: RenderSurfaceScope = {
        postMessage: (message) => scope.postMessage(message),
        addEventListener: (_type, handler) => handlers.set(surface, handler),
        removeEventListener: () => handlers.delete(surface),
      };

      const wrapped: RenderSurfaceDefinition<never, TShared> = {
        warm: async () => {
          // A surface is coming: an idle release now would destroy the device
          // this warm is about to compile against.
          cancelIdle();
          const shared = await share();
          await definition.warm?.(shared);
          return shared;
        },
        build: async (canvas) => {
          live += 1;
          cancelIdle();
          let built: RenderSurfacePass<unknown> | null;
          try {
            // `share()` rather than the warm result: a surface can be built
            // without ever being warmed, and it must still land on the one device.
            const shared = await share();
            built = (await definition.build(canvas, shared)) as RenderSurfacePass<unknown> | null;
          } catch (error) {
            live -= 1;
            scheduleIdleRelease();
            throw error;
          }
          if (!built) {
            live -= 1;
            scheduleIdleRelease();
            return null;
          }
          const pass = built;
          let retired = false;
          // A wrapper, not a patched `dispose`: the pass is the caller's object.
          return {
            lane: pass.lane,
            resize: (width: number, height: number) => pass.resize(width, height),
            draw: (state: unknown, frame: RenderSurfaceFrame) => pass.draw(state, frame),
            // Forwarded, or a shared surface loses GPU backpressure without a sound.
            settled: pass.settled ? () => pass.settled!() : undefined,
            dispose: () => {
              if (retired) return;
              retired = true;
              live -= 1;
              try {
                pass.dispose();
              } finally {
                scheduleIdleRelease();
              }
            },
          } as never;
        },
      };

      const stop = serveRenderSurface(surface, wrapped, proxy);
      stoppers.set(surface, stop);

      let released = false;
      return () => {
        if (released) return;
        released = true;
        stop();
        handlers.delete(surface);
        stoppers.delete(surface);
        releaseIfEmpty();
      };
    },

    get size() {
      return handlers.size;
    },
    get acquired() {
      return sharing !== null;
    },

    dispose() {
      if (disposed) return;
      disposed = true;
      for (const stop of stoppers.values()) stop();
      stoppers.clear();
      handlers.clear();
      scope.removeEventListener("message", onMessage);
      releaseIfEmpty();
    },
  };
};
