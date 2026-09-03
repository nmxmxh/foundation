import { getRuntimeCapabilities } from "./pulse/runtimeCaps";
import type { RenderSurfaceDiagnostics, RenderSurfaceMode, RenderSurfaceQualityTier } from "./types";

/**
 * A render lane that a React tree can own without touching a GPU.
 *
 * `gpu_practices.md` §247 and `performance_practices.md` §335.7 both say the
 * same thing: browser WebGPU is worker-owned, and React render paths receive
 * state and results — they do not create devices, compile pipelines, or
 * dispatch. Until now the SDK had no way to honour that for a *raster* lane.
 * `RuntimeWebGpuHost` covers compute — dispatch, storage buffers, readback —
 * and there was nothing at all for a pass that draws to a canvas, so every
 * project that needed one created a device inside a `useEffect` and submitted
 * on the main thread, next to layout, scrolling and input.
 *
 * This is the missing half. The host side runs on the main thread and does
 * only the four things that genuinely have to happen there:
 *
 *   1. transfer the canvas to the worker, once;
 *   2. observe its size;
 *   3. observe whether anybody can see it;
 *   4. forward state the page already has.
 *
 * Everything else — device, pipelines, uniforms, the frame clock, the quality
 * ladder — lives in the worker the caller supplies. Nothing in this file
 * imports WebGPU, and nothing in it draws.
 *
 * ## Why the clock is the worker's
 *
 * A dedicated worker has no `requestAnimationFrame`, and that is not a gap to
 * work around: it is the right answer for this kind of pass. Driving frames
 * from the main thread would put a message on the main thread's queue for
 * every frame, which reintroduces exactly the coupling the worker was for. A
 * render surface paces itself against a target cadence — see
 * `game_runtime_practices.md` §82 on budgeting by lane — and the host tells it
 * only when the target should change.
 *
 * ## Degrading
 *
 * `OffscreenCanvas` and `transferControlToOffscreen` are not universal, and a
 * canvas whose control has been transferred can never be drawn to from the
 * main thread again — so the decision is made once, before the transfer, and
 * `mode` reports it. A host in `main-thread` mode has transferred nothing and
 * the caller is free to run its own loop; a host in `worker` mode owns the
 * canvas and the caller must not touch it. There is no third state where both
 * might be true.
 */

/** Everything the host needs to stand a lane up. */
export type RenderSurfaceHostOptions<TState> = {
  /** The canvas to hand over. Must not have a 2D/WebGL context already. */
  canvas: HTMLCanvasElement;
  /**
   * Builds the worker. A factory rather than a URL so the caller's bundler
   * resolves the module — `new Worker(new URL("./x.worker.ts", import.meta.url))`
   * is a bundler-visible form and a string path is not.
   *
   * May return a worker shared with other surfaces. One worker serving several
   * surfaces is the arrangement that matters for GPU work: a device per worker
   * means a device per surface, and three devices to draw three things on one
   * page is three adapters, three sets of pipelines and three lots of driver
   * state. Set `ownsWorker` to `false` when the worker is shared, and the host
   * will stop listening rather than terminating it.
   */
  createWorker: () => Worker;
  /**
   * Whether disposing this host terminates the worker. Defaults to `true`.
   * Set `false` for a worker shared between surfaces.
   */
  ownsWorker?: boolean;
  /**
   * Stable, low-cardinality surface name — `game_runtime_practices.md` §121.
   * It is the marker name in traces and the key a multi-surface worker
   * dispatches on.
   */
  surface: string;
  /** The quality ladder, best rung first. The worker picks a rung from it. */
  tiers: readonly RenderSurfaceQualityTier[];
  /** Cap on `devicePixelRatio`. Defaults to 2. */
  maxRatio?: number;
  /**
   * Whether the surface should be paused when it leaves the viewport.
   *
   * Off for a surface that is fixed to the viewport and therefore always
   * intersecting; the caller then reports interest itself with `setVisible`.
   */
  cullOffscreen?: boolean;
  /** Diagnostics from the worker: lane taken, rung settled on, cadence held. */
  onDiagnostics?: (diagnostics: RenderSurfaceDiagnostics) => void;
  /**
   * A surface that could not be built.
   *
   * Separate from `onDiagnostics` because it is not a quality report, it is a
   * dead lane: the canvas has already been transferred, so the caller cannot
   * take it back, and the only remaining choices are to show nothing or to say
   * something. A host without this handler stays silent, which is how a
   * failed pass becomes an unexplained blank rectangle.
   */
  onFailed?: (reason: string) => void;
  /** The first state, sent with the transfer so the first frame is not blank. */
  initialState?: TState;
};

export type RenderSurfaceHost<TState> = {
  readonly mode: RenderSurfaceMode;
  /** Why the worker lane was refused, when it was. */
  readonly issues: readonly string[];
  /**
   * Hand the worker new state.
   *
   * Coalesced: calling this more than once between frames costs one structured
   * clone, not one per call, because a surface that is drawing at 40 Hz has no
   * use for the 120 pointer positions that arrived since its last frame.
   */
  setState: (state: TState) => void;
  /** Report whether anybody is looking. Cheap and idempotent. */
  setVisible: (visible: boolean) => void;
  /** Move the ladder floor — e.g. a surface that has scrolled behind text. */
  setTierFloor: (tier: number) => void;
  /** Stop the lane and release the worker. Idempotent. */
  dispose: () => void;
};

/** What a host would have transferred, when it cannot. */
export type RenderSurfaceRefusal = {
  offscreenCanvas: boolean;
  worker: boolean;
  transferControl: boolean;
};

/** Whether a worker-owned render lane can be built in this browser. */
export const probeRenderSurface = (): RenderSurfaceRefusal => ({
  offscreenCanvas: typeof OffscreenCanvas !== "undefined",
  worker: typeof Worker !== "undefined",
  transferControl:
    typeof HTMLCanvasElement !== "undefined" &&
    typeof HTMLCanvasElement.prototype.transferControlToOffscreen === "function",
});

/* ── the wire ───────────────────────────────────────────────────────── */

/**
 * Messages the host sends. Exported because the worker half has to name them,
 * and a protocol described in two places is a protocol with two versions.
 */
export type RenderSurfaceCommand<TState> =
  | {
      kind: "INIT";
      surface: string;
      canvas: OffscreenCanvas;
      tiers: readonly RenderSurfaceQualityTier[];
      ratio: number;
      width: number;
      height: number;
      state?: TState;
    }
  | { kind: "RESIZE"; surface: string; width: number; height: number; ratio: number }
  | { kind: "STATE"; surface: string; state: TState }
  | { kind: "VISIBILITY"; surface: string; visible: boolean }
  | { kind: "TIER_FLOOR"; surface: string; tier: number }
  | { kind: "STOP"; surface: string };

/** Messages the worker sends back. Diagnostics only; no pixels come this way. */
export type RenderSurfaceEvent =
  | { kind: "READY"; surface: string; lane: string }
  | { kind: "DIAGNOSTICS"; surface: string; diagnostics: RenderSurfaceDiagnostics }
  | { kind: "FAILED"; surface: string; reason: string };

/* ── the host ───────────────────────────────────────────────────────── */

export const createRenderSurfaceHost = <TState,>(
  options: RenderSurfaceHostOptions<TState>,
): RenderSurfaceHost<TState> => {
  const probe = probeRenderSurface();
  const issues: string[] = [];
  if (!probe.offscreenCanvas) issues.push("OffscreenCanvas is unavailable");
  if (!probe.worker) issues.push("Worker is unavailable");
  if (!probe.transferControl) issues.push("transferControlToOffscreen is unavailable");
  // Reported rather than required: a render surface transfers a canvas, which
  // needs neither shared memory nor cross-origin isolation. A caller that also
  // wants a shared-memory unit in the same worker reads this and decides.
  const runtime = getRuntimeCapabilities();

  if (issues.length > 0) {
    return {
      mode: "main-thread",
      issues,
      setState: () => undefined,
      setVisible: () => undefined,
      setTierFloor: () => undefined,
      dispose: () => undefined,
    };
  }

  const { canvas, surface, tiers } = options;
  const maxRatio = options.maxRatio ?? 2;
  let worker: Worker | null = null;
  let stopped = false;
  let pending: TState | undefined;
  let flushHandle: number | null = null;
  let width = canvas.clientWidth;
  let height = canvas.clientHeight;

  const ratio = () => Math.min(globalThis.devicePixelRatio || 1, maxRatio);

  const post = (command: RenderSurfaceCommand<TState>, transfer?: Transferable[]) => {
    if (stopped || !worker) return;
    if (transfer) worker.postMessage(command, transfer);
    else worker.postMessage(command);
  };

  try {
    worker = options.createWorker();
  } catch {
    return {
      mode: "main-thread",
      issues: ["worker construction failed"],
      setState: () => undefined,
      setVisible: () => undefined,
      setTierFloor: () => undefined,
      dispose: () => undefined,
    };
  }

  // `addEventListener`, never `onmessage`: assigning the handler would evict
  // whichever surface registered first, which on a shared worker is a bug that
  // shows up as one figure rendering and the other silently not.
  const onWorkerMessage = (event: MessageEvent<RenderSurfaceEvent>) => {
    const message = event.data;
    if (!message || message.surface !== surface) return;
    if (message.kind === "DIAGNOSTICS") options.onDiagnostics?.(message.diagnostics);
    if (message.kind === "FAILED") options.onFailed?.(message.reason);
    if (message.kind === "READY") {
      options.onDiagnostics?.({
        surface,
        mode: "worker",
        lane: message.lane,
        tier: 0,
        cadenceMs: tiers[0]?.cadenceMs ?? 0,
        scale: tiers[0]?.scale ?? 1,
        visible: true,
        issues: runtime.issues.map((issue) => issue.capability),
      });
    }
  };

  worker.addEventListener("message", onWorkerMessage as EventListener);

  let offscreen: OffscreenCanvas;
  try {
    offscreen = canvas.transferControlToOffscreen();
  } catch (error) {
    worker.removeEventListener("message", onWorkerMessage as EventListener);
    if (options.ownsWorker ?? true) worker.terminate();
    return {
      mode: "main-thread",
      issues: [`canvas transfer failed: ${error instanceof Error ? error.message : String(error)}`],
      setState: () => undefined,
      setVisible: () => undefined,
      setTierFloor: () => undefined,
      dispose: () => undefined,
    };
  }

  post(
    {
      kind: "INIT",
      surface,
      canvas: offscreen,
      tiers,
      ratio: ratio(),
      width,
      height,
      state: options.initialState,
    },
    [offscreen],
  );

  /*
   * Size comes from a `ResizeObserver`, never from `getBoundingClientRect`.
   *
   * A rect read inside a frame is a forced synchronous layout, and it is worst
   * during a scroll — when layout is already dirty and the browser was about
   * to do it anyway on its own schedule. The observer reports the same number
   * without asking for it.
   */
  let sizeObserver: ResizeObserver | null = null;
  if (typeof ResizeObserver !== "undefined") {
    sizeObserver = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const box = entry.contentBoxSize?.[0];
        width = box ? box.inlineSize : entry.contentRect.width;
        height = box ? box.blockSize : entry.contentRect.height;
      }
      if (width > 0 && height > 0) post({ kind: "RESIZE", surface, width, height, ratio: ratio() });
    });
    sizeObserver.observe(canvas);
  }

  let visibility: IntersectionObserver | null = null;
  if ((options.cullOffscreen ?? true) && typeof IntersectionObserver !== "undefined") {
    visibility = new IntersectionObserver(
      (entries) => {
        post({ kind: "VISIBILITY", surface, visible: entries.some((entry) => entry.isIntersecting) });
      },
      { threshold: 0 },
    );
    visibility.observe(canvas);
  }

  // A hidden tab is not a slower tab. The worker's clock is a timer and timers
  // keep running in the background, so a surface that did not hear about this
  // would keep submitting GPU work for a tab nobody is looking at.
  const onTabVisibility = () => {
    if (typeof document !== "undefined") {
      post({ kind: "VISIBILITY", surface, visible: !document.hidden });
    }
  };
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onTabVisibility);
  }

  const flush = () => {
    flushHandle = null;
    if (pending === undefined) return;
    post({ kind: "STATE", surface, state: pending });
    pending = undefined;
  };

  return {
    mode: "worker",
    issues,

    setState(state) {
      pending = state;
      // One clone per frame at most. `requestAnimationFrame` is the right
      // coalescing window even though nothing here draws: it is the rate at
      // which the page's own state can actually have changed.
      if (flushHandle === null && !stopped) {
        if (typeof requestAnimationFrame === "function") {
          flushHandle = requestAnimationFrame(flush);
        } else {
          flushHandle = setTimeout(flush, 16) as unknown as number;
        }
      }
    },

    setVisible(visible) {
      post({ kind: "VISIBILITY", surface, visible });
    },

    setTierFloor(tier) {
      post({ kind: "TIER_FLOOR", surface, tier });
    },

    dispose() {
      if (stopped) return;
      stopped = true;
      if (flushHandle !== null) {
        if (typeof cancelAnimationFrame === "function") {
          cancelAnimationFrame(flushHandle);
        } else {
          clearTimeout(flushHandle);
        }
        flushHandle = null;
      }
      sizeObserver?.disconnect();
      visibility?.disconnect();
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onTabVisibility);
      }
      worker?.postMessage({ kind: "STOP", surface } satisfies RenderSurfaceCommand<TState>);
      worker?.removeEventListener("message", onWorkerMessage as EventListener);
      if (options.ownsWorker ?? true) worker?.terminate();
      worker = null;
    },
  };
};
