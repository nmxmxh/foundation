import { startingTierForDevice } from "./deviceProfile";
import { createRenderStateChannel, type RenderStateChannel } from "./renderStateChannel";
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
   * A worker already spawned and warming, from `prewarmRenderSurface`.
   *
   * When given, `createWorker` is not called: this worker is adopted, along
   * with whatever its `warm` has already finished. Adopting also takes
   * ownership, so the handle's own `dispose` becomes a no-op and the host's
   * disposal is the one that counts.
   */
  warmed?: RenderSurfaceWarmHandle;
  /**
   * Stable, low-cardinality surface name — `game_runtime_practices.md` §121.
   * It is the marker name in traces and the key a multi-surface worker
   * dispatches on.
   */
  surface: string;
  /** The quality ladder, best rung first. The worker picks a rung from it. */
  tiers: readonly RenderSurfaceQualityTier[];
  /**
   * Cap on `devicePixelRatio`. Defaults to 2.
   *
   * Two is right for a figure the page is *about*. For decorative full-screen
   * passes it is worth a thought: on a DPR-3 phone a cap of 2 still renders
   * 1.24x the CSS pixels of the panel's own logical resolution, at the top of
   * the ladder, for something sitting behind text. There is no universally
   * correct number here — it is a per-surface judgement that a trace should
   * settle — but a decorative surface that has never asked the question is
   * probably paying more fill than it meant to.
   */
  maxRatio?: number;
  /**
   * Opening rung, overriding the device prior.
   *
   * Left unset, the host reads a cheap synchronous device profile — pointer,
   * viewport, pixel ratio, cores, memory, reduced-motion — and opens the ladder
   * where that profile says the device can hold. Set this only when the caller
   * knows something the browser does not, such as a surface the user has
   * already pinned to a quality in settings. `0` forces the best rung.
   */
  startingTier?: number;
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
  /**
   * A degradation the surface survived — a prewarm that failed, so the build
   * ran cold. Distinct from `onFailed`, which is a dead lane.
   */
  onWarning?: (reason: string) => void;
  /** The first state, sent with the transfer so the first frame is not blank. */
  initialState?: TState;
  /**
   * Elements of a shared-memory state channel, in `float32`s. Off by default.
   *
   * `setState` posts a message, and a posted message is a structured clone: the
   * runtime walks the object and allocates a copy, then the worker allocates
   * another. For a pointer position that is free. For per-entity transforms, a
   * particle field, or audio levels it is the frame's dominant cost, paid twice
   * on the main thread, every frame.
   *
   * Setting this allocates a `SharedArrayBuffer` alongside the canvas and gives
   * the host `writeState`, which publishes a generation with three atomics and
   * one bulk copy and no allocation at all. `setState` keeps working and stays
   * the right tool for anything that is not a flat run of numbers.
   *
   * Requires cross-origin isolation. Where that is unavailable the channel is
   * simply absent — `stateChannel` is `null`, `writeState` returns `false`, and
   * the surface runs exactly as it does today. A lane that *required* COOP/COEP
   * headers to draw a background texture would not be a lane anybody adopted.
   */
  stateChannelElements?: number;
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
  /**
   * Publish a generation of shared state, without a clone.
   *
   * `fill` writes into the channel's slot and returns the element count. It is
   * called synchronously, must not retain the view, and must not allocate —
   * this runs on whatever cadence the page updates at, which on a ProMotion
   * panel is 120 times a second.
   *
   * Returns `false` when no channel was negotiated, so a caller can fall back
   * to `setState` on the same branch that checks it.
   */
  writeState: (fill: (slot: Float32Array) => number) => boolean;
  /** The channel, when one was negotiated. `null` otherwise. */
  readonly stateChannel: RenderStateChannel | null;
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
      /**
       * Which rung to open on. Chosen by the host, because the signals it is
       * chosen from — `matchMedia` in particular — do not exist in a worker.
       * Optional so a hand-built command still initialises at the best rung.
       */
      tier?: number;
      /**
       * The shared state channel, when one was negotiated.
       *
       * Sent, never transferred: a `SharedArrayBuffer` is shared by being
       * referenced from both sides, and putting it in the transfer list would
       * detach it from the host that has to keep writing to it.
       */
      stateBuffer?: SharedArrayBuffer;
      ratio: number;
      width: number;
      height: number;
      state?: TState;
    }
  | { kind: "WARM"; surface: string }
  | { kind: "RESIZE"; surface: string; width: number; height: number; ratio: number }
  | { kind: "STATE"; surface: string; state: TState }
  | { kind: "VISIBILITY"; surface: string; visible: boolean }
  | { kind: "TIER_FLOOR"; surface: string; tier: number }
  | { kind: "STOP"; surface: string };

/** Messages the worker sends back. Diagnostics only; no pixels come this way. */
export type RenderSurfaceEvent =
  | { kind: "READY"; surface: string; lane: string }
  /**
   * Something degraded without the surface dying — a failed prewarm, say, which
   * costs the latency the warm was for and nothing else. Separate from `FAILED`
   * because a caller that treats the two alike will show an error for a surface
   * that is drawing perfectly well, one frame later than it might have.
   */
  | { kind: "WARNING"; surface: string; reason: string }
  | { kind: "DIAGNOSTICS"; surface: string; diagnostics: RenderSurfaceDiagnostics }
  | { kind: "FAILED"; surface: string; reason: string };

/* ── prewarming ─────────────────────────────────────────────────────── */

/** A worker already spawned and warming, waiting for a canvas. */
export type RenderSurfaceWarmHandle = {
  readonly surface: string;
  readonly worker: Worker;
  /**
   * Hand ownership to a host. Called by `createRenderSurfaceHost`.
   *
   * After this the handle's own `dispose` is a no-op, because there are now two
   * references to one worker and only one of them should be able to terminate
   * it. A caller that prewarms, mounts, and then tidies up its handle would
   * otherwise kill a surface that is drawing.
   */
  adopt: () => void;
  /**
   * Release the worker if no host ever adopted it — a route the viewer never
   * visited, a hover that went nowhere. Safe to call always.
   */
  dispose: () => void;
};

/**
 * Spawn the worker and start the canvas-independent half of the build.
 *
 * Standing a surface up is a chain — spawn the worker, load its module, request
 * an adapter, request a device, compile shaders, create pipelines, configure
 * the context, draw — and only the last two steps need a canvas. Everything
 * before them used to run *after* the transfer, which meant after a DOM element
 * existed, which meant after the component owning it had mounted. All of it sat
 * on the critical path to first paint for no reason other than where it was
 * called from.
 *
 * Call this as early as the page knows a surface is coming: a route's module
 * loading, an idle callback, a hover on the link that leads to it. Then pass
 * the handle to `createRenderSurfaceHost` as `warmed`, and the only work left
 * at mount is the transfer itself.
 *
 * Returns `null` where a worker lane is unavailable, so a caller can prewarm
 * unconditionally and let the host degrade on its own terms.
 */
export const prewarmRenderSurface = (options: {
  surface: string;
  createWorker: () => Worker;
}): RenderSurfaceWarmHandle | null => {
  const probe = probeRenderSurface();
  if (!probe.worker || !probe.offscreenCanvas || !probe.transferControl) return null;

  let worker: Worker;
  try {
    worker = options.createWorker();
  } catch {
    return null;
  }

  worker.postMessage({ kind: "WARM", surface: options.surface } satisfies RenderSurfaceCommand<never>);

  let released = false;
  return {
    surface: options.surface,
    worker,
    adopt() {
      released = true;
    },
    dispose() {
      if (released) return;
      released = true;
      worker.terminate();
    },
  };
};

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
      writeState: () => false,
      stateChannel: null,
      dispose: () => undefined,
    };
  }

  const { canvas, surface, tiers } = options;
  const maxRatio = options.maxRatio ?? 2;
  /*
   * Read the device once, here, before anything is transferred.
   *
   * A ladder only descends from where it opens, so opening every device at the
   * best rung means every phone finds out it is a phone by janking through the
   * demotion count on first paint. The prior costs a handful of `matchMedia`
   * calls and removes that window entirely.
   */
  const openingTier = Math.min(
    Math.max(Math.trunc(options.startingTier ?? startingTierForDevice(tiers.length)), 0),
    Math.max(0, tiers.length - 1),
  );
  /*
   * Negotiated once, before the transfer. Absent where cross-origin isolation
   * is not in force, which is a supported configuration rather than a failure —
   * see `stateChannelElements`.
   */
  const stateChannel =
    options.stateChannelElements && options.stateChannelElements > 0
      ? createRenderStateChannel(options.stateChannelElements)
      : null;
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
    // Adopt, or spawn. A warmed worker has been compiling pipelines since
    // before this component existed.
    worker = options.warmed?.worker ?? options.createWorker();
    options.warmed?.adopt();
  } catch {
    return {
      mode: "main-thread",
      issues: ["worker construction failed"],
      setState: () => undefined,
      setVisible: () => undefined,
      setTierFloor: () => undefined,
      writeState: () => false,
      stateChannel: null,
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
    if (message.kind === "WARNING") options.onWarning?.(message.reason);
    if (message.kind === "READY") {
      // The rung the surface actually opened on, not rung zero: reporting a
      // constant here made every diagnostic sink believe every device started
      // at the top, which is precisely the claim the device prior exists to
      // stop being true.
      const opened = tiers[openingTier] ?? tiers[0];
      options.onDiagnostics?.({
        surface,
        mode: "worker",
        lane: message.lane,
        tier: openingTier,
        cadenceMs: opened?.cadenceMs ?? 0,
        scale: opened?.scale ?? 1,
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
      writeState: () => false,
      stateChannel: null,
      dispose: () => undefined,
    };
  }

  post(
    {
      kind: "INIT",
      surface,
      canvas: offscreen,
      tiers,
      tier: openingTier,
      stateBuffer: stateChannel?.buffer,
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

    stateChannel,

    writeState(fill) {
      /*
       * No coalescing, on purpose. `setState` coalesces to one clone per frame
       * because a clone is expensive and the surface has no use for the 120
       * pointer positions since its last draw. A channel write is three atomics
       * and one bulk copy into memory the worker already maps, so deferring it
       * to `requestAnimationFrame` would add a frame of latency to save nothing.
       */
      if (stopped || !stateChannel) return false;
      return stateChannel.write(fill);
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
