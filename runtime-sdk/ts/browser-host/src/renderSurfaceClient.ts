import type { RenderSurfaceCommand, RenderSurfaceEvent } from "./renderSurface";
import { attachRenderStateReader, type RenderStateReader } from "./renderStateChannel";
import type { RenderSurfaceQualityTier } from "./types";

/**
 * The worker half of a render surface.
 *
 * A worker that owns a canvas has three jobs beyond drawing — pacing itself,
 * sizing its backing store, and deciding which rung of the quality ladder it
 * can hold — and all three are the same in every surface that has ever needed
 * them. This runs them, and calls the surface's own `draw` when it is time.
 *
 * ## The clock
 *
 * A dedicated worker has no `requestAnimationFrame`. That is usually written
 * up as a limitation and it is closer to a favour: a decorative pass wants a
 * *cadence*, not a display refresh, and a timer gives it one directly. The
 * loop below aims at the rung's `cadenceMs` and corrects for its own drift, so
 * a surface asking for 40 a second gets 40 a second on a 60 Hz panel and on a
 * 144 Hz one, without the caller thinking about it.
 *
 * ## The ladder
 *
 * Rungs are climbed and descended from measured frame cost, with hysteresis —
 * demotion after a short run of misses, promotion only after a long clean one,
 * so a marginal device settles instead of oscillating. `setTierFloor` lets the
 * host pin a worse rung for reasons the worker cannot see, such as the surface
 * having scrolled behind a wall of text.
 *
 * The surface itself never sees any of this. It is handed a frame — size,
 * scale, detail budget, elapsed time — and draws it.
 */

/** What a surface implementation has to provide. */
export type RenderSurfaceDefinition<TState, TWarm = unknown> = {
  /**
   * Everything that does not need the canvas. Optional, and the whole point.
   *
   * Standing a WebGPU surface up is a chain: spawn the worker, load its module,
   * request an adapter, request a device, compile shader modules, create
   * pipelines, configure the context, draw. Only the last two need a canvas —
   * `getPreferredCanvasFormat()` returns the format without one — and yet the
   * entire chain used to run *after* the transfer, which meant it ran after a
   * DOM element existed, which meant it ran after React had mounted, which put
   * every millisecond of it on the critical path to first paint.
   *
   * Splitting it costs nothing and moves the expensive half off that path. A
   * host can call `prewarmRenderSurface` during idle, before the component that
   * owns the canvas has rendered, and by the time the canvas arrives the device
   * exists and the pipelines are compiled.
   *
   * `gpu_practices.md` has asked for this since it was written: *"pipeline
   * creation must be async or prewarmed outside latency-sensitive UI"*. The
   * compute lane has had `prewarmKernel` all along. This is the raster half.
   *
   * Called at most once per worker. The result is handed to `build`.
   */
  warm?: () => Promise<TWarm> | TWarm;
  /**
   * Build the pass. Called once, after the canvas arrives.
   *
   * Receives whatever `warm` produced, or `undefined` when the surface was
   * never warmed — a definition must work either way, because prewarming is the
   * caller's choice and a cold start has to stay correct.
   *
   * Returns the lane it took — "webgpu", "webgl2", "2d" — or `null` if it
   * cannot draw at all, which stops the loop rather than spinning it.
   */
  build: (
    canvas: OffscreenCanvas,
    warmed: TWarm | undefined,
  ) => Promise<RenderSurfacePass<TState> | null> | RenderSurfacePass<TState> | null;
};

export type RenderSurfacePass<TState> = {
  /** Diagnostic name of the lane taken. */
  lane: string;
  /** Size the backing store. Called on every resize and every tier change. */
  resize: (width: number, height: number) => void;
  /** Draw one frame. */
  draw: (state: TState | undefined, frame: RenderSurfaceFrame) => void;
  /**
   * Release everything the pass created. **Required.**
   *
   * Required, and not optional as it was, because the resources a pass holds
   * are the ones no profiler will tell you about. A texture, a pipeline, a
   * vertex buffer and the device itself all live in GPU memory, outside the JS
   * heap — so a pass that leaks them shows nothing in a heap snapshot, nothing
   * in an allocation profile, and nothing in the retained-bytes figures this
   * package measures. The leak is invisible until the tab is using a gigabyte.
   *
   * The shape that makes it invisible is ordinary: a single-page app mounts a
   * surface, the viewer navigates, the surface unmounts and another mounts. On
   * an owned worker the thread dies and the browser reclaims everything, which
   * is why this is easy to get away with in development. On a *shared* worker —
   * the arrangement this lane recommends, because one device per surface means
   * three devices to draw three figures — the worker outlives the pass and
   * nothing is reclaimed at all.
   *
   * Called exactly once per pass: on `STOP`, when a build is superseded by a
   * second `INIT`, and when the server itself is disposed. A pass with genuinely
   * nothing to release writes an empty body, which is a claim that it has
   * nothing to release rather than an oversight.
   */
  dispose: () => void;
};

export type RenderSurfaceFrame = {
  /** Backing-store size in device pixels, already applied by `resize`. */
  width: number;
  height: number;
  /** Detail budget for this rung — steps, samples, particles. */
  detail: number;
  /** Milliseconds since the surface started. */
  elapsed: number;
  /** Milliseconds since the previous drawn frame. */
  delta: number;
  /** Which rung this frame was drawn at. */
  tier: number;
  /**
   * The shared state channel's newest data, or `null` when no channel was
   * negotiated and nothing has ever been published.
   *
   * A stable view into a scratch buffer the loop owns — read it during `draw`,
   * never retain it. Unlike `state`, which arrives by structured clone, this
   * crossed no boundary: the host wrote the bytes and the worker read them out
   * of the same memory.
   */
  shared: Float32Array | null;
  /**
   * Which generation `shared` holds. Unchanged between frames means the host
   * published nothing new, and a pass whose work depends only on this can skip
   * it — the temporal-coherence lever, exposed rather than guessed at.
   */
  sharedGeneration: number;
};

/**
 * The worker global, structurally.
 *
 * Named by shape rather than by `DedicatedWorkerGlobalScope` so this module
 * compiles under the SDK's DOM-only lib set — the package is consumed by
 * bundlers that add the worker lib themselves, and widening the whole
 * package's libs to satisfy one file would change what every other module in
 * it is allowed to reference.
 */
export type RenderSurfaceScope = {
  postMessage: (message: unknown) => void;
  addEventListener: (type: "message", listener: (event: MessageEvent) => void) => void;
  removeEventListener: (type: "message", listener: (event: MessageEvent) => void) => void;
};

/**
 * How far past its own cadence a frame may run before it counts as a miss.
 *
 * Proportional, not a fixed number of milliseconds. A fixed slack means
 * something different on every rung, and it meant the wrong thing on exactly
 * the rungs that matter: the previous `1000 / 30` constant, added to a 25 ms
 * cadence, put the miss line at 58.3 ms — 17.1 fps. A phone holding a 40 Hz
 * surface at 20 fps is failing its budget by 125% and recorded *zero* misses,
 * so it never demoted, and the ladder that existed to protect it never moved.
 *
 * As a factor the line means the same thing everywhere: 1.35 puts it at 33.8 ms
 * on a 40 Hz rung and 22.5 ms on a 60 Hz one, which is "noticeably late" in
 * both cases rather than "catastrophic" in one and "unreachable" in the other.
 */
const MISS_FACTOR = 1.35;
/** How many missed frames before the ladder drops a rung. ~150 ms at 40 Hz. */
const DEMOTE_AFTER = 6;
/** How many clean frames before it climbs one. Slow, so it settles. */
const PROMOTE_AFTER = 240;
/**
 * A clean run this long forgives the misses behind it.
 *
 * Misses accumulate rather than decaying one-for-one, because a device
 * alternating hit/miss is failing and one-for-one decay let it fail forever
 * without ever reaching the demote count. But cumulative-and-never-cleared is
 * its own bug: a healthy surface pinned at rung zero can never promote — there
 * is no better rung to move to — so nothing would ever reset the tally, and six
 * unrelated hiccups spread over an hour would demote a machine that had held
 * cadence the entire time. Forgiving after a sustained clean run makes the
 * count a burst detector, which is what it was always meant to be.
 */
const FORGIVE_AFTER = 48;

/**
 * Serve one surface inside a worker.
 *
 * Call once at the top of the worker module. Returns a disposer, though a
 * worker is usually terminated rather than tidied.
 */
export const serveRenderSurface = <TState, TWarm = unknown>(
  surface: string,
  definition: RenderSurfaceDefinition<TState, TWarm>,
  scope: RenderSurfaceScope = globalThis as unknown as RenderSurfaceScope,
): (() => void) => {
  let pass: RenderSurfacePass<TState> | null = null;
  let tiers: readonly RenderSurfaceQualityTier[] = [{ scale: 1, cadenceMs: 1000 / 60 }];
  let state: TState | undefined;
  let cssWidth = 0;
  let cssHeight = 0;
  let ratio = 1;
  let tier = 0;
  let tierFloor = 0;
  let visible = true;
  let running = false;
  let stopped = false;
  /*
   * Which `INIT` the current pass belongs to.
   *
   * A surface server lives for the life of the worker and can be initialised
   * more than once: a host unmounts and remounts — React does exactly that on
   * every mount in development StrictMode — and the second mount transfers a
   * new canvas to the same surface name. So `STOP` retires the pass, not the
   * server, and a later `INIT` revives it.
   *
   * The counter is what makes that safe while a build is in flight. Building a
   * pipeline is asynchronous; if a `STOP` and a second `INIT` arrive during
   * one, the first build must not install itself over the second. Comparing
   * generations after the await is how it finds out it is stale.
   */
  let generation = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let overBudget = 0;
  let underBudget = 0;
  let startedAt = 0;
  let lastDrawAt = 0;
  let targetNextFrame = 0;

  let stateReader: RenderStateReader | null = null;
  /*
   * The warm result, held as a promise rather than a value.
   *
   * A `WARM` and an `INIT` can arrive back to back — a host that prewarms and
   * then mounts immediately does exactly that — so `INIT` has to be able to
   * *wait* for warming rather than find it unfinished and start again. Holding
   * the promise makes the two orders identical.
   */
  let warming: Promise<TWarm> | null = null;

  const frameDescriptor: RenderSurfaceFrame = {
    width: 0,
    height: 0,
    detail: 0,
    elapsed: 0,
    delta: 0,
    tier: 0,
    shared: null,
    sharedGeneration: 0,
  };

  const rung = () => tiers[Math.min(Math.max(tier, tierFloor), tiers.length - 1)]!;

  const emit = (event: RenderSurfaceEvent) => scope.postMessage(event);

  const report = () => {
    const current = rung();
    emit({
      kind: "DIAGNOSTICS",
      surface,
      diagnostics: {
        surface,
        mode: "worker",
        lane: pass?.lane ?? "none",
        tier: Math.min(Math.max(tier, tierFloor), tiers.length - 1),
        cadenceMs: current.cadenceMs,
        scale: current.scale,
        visible,
        issues: [],
      },
    });
  };

  const applySize = () => {
    if (!pass || cssWidth < 1 || cssHeight < 1) return;
    const scale = rung().scale * ratio;
    pass.resize(
      Math.max(1, Math.round(cssWidth * scale)),
      Math.max(1, Math.round(cssHeight * scale)),
    );
  };

  const step = () => {
    timer = null;
    if (!pass || stopped || !visible) {
      running = false;
      return;
    }
    const current = rung();
    const now = performance.now();
    const delta = lastDrawAt === 0 ? current.cadenceMs : now - lastDrawAt;
    lastDrawAt = now;

    const scale = current.scale * ratio;
    frameDescriptor.width = Math.max(1, Math.round(cssWidth * scale));
    frameDescriptor.height = Math.max(1, Math.round(cssHeight * scale));
    frameDescriptor.detail = current.detail ?? 0;
    frameDescriptor.elapsed = now - startedAt;
    frameDescriptor.delta = delta;
    frameDescriptor.tier = tier;

    /*
     * Read the channel once per frame, not once per rung of the ladder and not
     * on a listener. `read()` returns `null` when the generation has not moved,
     * in which case the previous view still stands and no copy happened at all.
     */
    if (stateReader) {
      const fresh = stateReader.read();
      if (fresh !== null) frameDescriptor.shared = fresh;
      else if (frameDescriptor.shared === null) frameDescriptor.shared = stateReader.current;
      frameDescriptor.sharedGeneration = stateReader.generation;
    }

    pass.draw(state, frameDescriptor);

    /*
     * The ladder, measured on the gap the loop actually achieved.
     *
     * Not on how long `draw` took: a surface that fits its own budget while
     * the worker is busy with something else has not fitted anything, and the
     * number that matters to a viewer is how often a frame arrived.
     */
    if (delta > current.cadenceMs * MISS_FACTOR) {
      overBudget += 1;
      underBudget = 0;
    } else {
      underBudget += 1;
      if (underBudget >= FORGIVE_AFTER) overBudget = 0;
    }
    if (overBudget >= DEMOTE_AFTER && tier < tiers.length - 1) {
      tier += 1;
      overBudget = 0;
      underBudget = 0;
      applySize();
      report();
    } else if (underBudget >= PROMOTE_AFTER && tier > 0) {
      tier -= 1;
      overBudget = 0;
      underBudget = 0;
      applySize();
      report();
    }

    // Aim at the cadence and compensate for drift, resetting if jitter is huge.
    if (targetNextFrame === 0 || Math.abs(now - targetNextFrame) > current.cadenceMs * 5) {
      targetNextFrame = now + current.cadenceMs;
    } else {
      targetNextFrame += current.cadenceMs;
    }
    const delay = Math.max(0, targetNextFrame - performance.now());
    timer = setTimeout(step, delay);
  };

  const start = () => {
    if (running || stopped || !pass || !visible) return;
    running = true;
    lastDrawAt = 0;
    targetNextFrame = 0;
    overBudget = 0;
    step();
  };

  const stop = () => {
    running = false;
    targetNextFrame = 0;
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  };

  /**
   * Stand the pass up.
   *
   * Split out of the message switch because building a WebGPU pipeline is
   * asynchronous and a message handler that awaits is a handler that can be
   * re-entered — a second `INIT`, or a `RESIZE` arriving mid-build, would run
   * against a half-built pass.
   */
  const initialise = async (message: Extract<RenderSurfaceCommand<TState>, { kind: "INIT" }>) => {
    // A new canvas retires whatever was drawing to the old one.
    generation += 1;
    const mine = generation;
    stopped = false;
    stop();
    pass?.dispose();
    pass = null;
    visible = true;
    tiers = message.tiers.length > 0 ? message.tiers : tiers;
    /*
     * The opening rung comes from the host, which read the device before the
     * canvas was transferred.
     *
     * It has to be the host: `matchMedia` does not exist in a worker, so the
     * pointer, viewport and reduced-motion signals are unreadable from here,
     * and the ladder can only ever descend from where it opens. Defaulting to
     * zero keeps a host that sends no prior — an older one, or a caller that
     * built the command by hand — behaving exactly as it did before.
     */
    tier = Math.min(Math.max(Math.trunc(message.tier ?? 0), 0), tiers.length - 1);
    /*
     * Attached, not copied. A buffer the host declined to negotiate, or one
     * from a foreign writer, leaves the reader null and the surface reads its
     * state from `STATE` messages exactly as before.
     */
    stateReader = message.stateBuffer ? attachRenderStateReader(message.stateBuffer) : null;
    frameDescriptor.shared = null;
    frameDescriptor.sharedGeneration = 0;
    cssWidth = message.width;
    cssHeight = message.height;
    ratio = message.ratio;
    state = message.state;
    startedAt = performance.now();
    let built: RenderSurfacePass<TState> | null;
    try {
      const warmed = warming ? await warming : undefined;
      // Superseded while warming: the canvas below belongs to a later INIT.
      if (mine !== generation || stopped) return;
      built = await definition.build(message.canvas, warmed);
    } catch (error) {
      if (mine === generation) emit({ kind: "FAILED", surface, reason: String(error) });
      return;
    }
    // Superseded while the pipeline was compiling: this pass is drawing to a
    // canvas nobody is showing any more.
    if (mine !== generation || stopped) {
      built?.dispose();
      return;
    }
    if (!built) {
      emit({ kind: "FAILED", surface, reason: "no lane available" });
      return;
    }
    pass = built;
    applySize();
    emit({ kind: "READY", surface, lane: pass.lane });
    start();
  };

  const onMessage = (event: MessageEvent) => {
    const message = event.data as RenderSurfaceCommand<TState> | undefined;
    if (!message || message.surface !== surface) return;

    switch (message.kind) {
      case "WARM": {
        // Idempotent: warming twice would build a second device.
        if (!warming && definition.warm) {
          warming = Promise.resolve(definition.warm()).catch((error) => {
            // A failed warm is not a failed surface. `build` runs cold and gets
            // `undefined`, which is the contract it already has to honour.
            emit({ kind: "WARNING", surface, reason: `warm failed: ${String(error)}` });
            warming = null;
            return undefined as TWarm;
          });
        }
        return;
      }
      case "INIT": {
        void initialise(message);
        return;
      }
      case "RESIZE": {
        cssWidth = message.width;
        cssHeight = message.height;
        ratio = message.ratio;
        applySize();
        start();
        return;
      }
      case "STATE": {
        state = message.state;
        return;
      }
      case "VISIBILITY": {
        const was = visible;
        visible = message.visible;
        if (visible && !was) start();
        if (!visible && was) stop();
        return;
      }
      case "TIER_FLOOR": {
        tierFloor = Math.max(0, message.tier);
        applySize();
        report();
        return;
      }
      case "STOP": {
        // Retires the pass, not the server. A later INIT revives the surface;
        // see `generation`.
        stopped = true;
        stop();
        pass?.dispose();
        pass = null;
        return;
      }
    }
  };

  scope.addEventListener("message", onMessage);

  return () => {
    stopped = true;
    stop();
    scope.removeEventListener("message", onMessage);
    pass?.dispose();
    pass = null;
  };
};
