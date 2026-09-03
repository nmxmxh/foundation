import type { RenderSurfaceCommand, RenderSurfaceEvent } from "./renderSurface";
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
export type RenderSurfaceDefinition<TState> = {
  /**
   * Build the pass. Called once, after the canvas arrives.
   *
   * Returns the lane it took — "webgpu", "webgl2", "2d" — or `null` if it
   * cannot draw at all, which stops the loop rather than spinning it.
   */
  build: (canvas: OffscreenCanvas) => Promise<RenderSurfacePass<TState> | null> | RenderSurfacePass<TState> | null;
};

export type RenderSurfacePass<TState> = {
  /** Diagnostic name of the lane taken. */
  lane: string;
  /** Size the backing store. Called on every resize and every tier change. */
  resize: (width: number, height: number) => void;
  /** Draw one frame. */
  draw: (state: TState | undefined, frame: RenderSurfaceFrame) => void;
  dispose?: () => void;
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

/** How many missed frames before the ladder drops a rung. */
const DEMOTE_AFTER = 24;
/** How many clean frames before it climbs one. Seven times, so it settles. */
const PROMOTE_AFTER = 180;
/** How far past its own cadence a frame may run before it counts as a miss. */
const BUDGET_SLACK_MS = 1000 / 30;

/**
 * Serve one surface inside a worker.
 *
 * Call once at the top of the worker module. Returns a disposer, though a
 * worker is usually terminated rather than tidied.
 */
export const serveRenderSurface = <TState,>(
  surface: string,
  definition: RenderSurfaceDefinition<TState>,
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

  const frameDescriptor: RenderSurfaceFrame = {
    width: 0,
    height: 0,
    detail: 0,
    elapsed: 0,
    delta: 0,
    tier: 0,
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

    pass.draw(state, frameDescriptor);

    /*
     * The ladder, measured on the gap the loop actually achieved.
     *
     * Not on how long `draw` took: a surface that fits its own budget while
     * the worker is busy with something else has not fitted anything, and the
     * number that matters to a viewer is how often a frame arrived.
     */
    if (delta > current.cadenceMs + BUDGET_SLACK_MS) {
      overBudget += 1;
      underBudget = 0;
    } else {
      underBudget += 1;
      if (overBudget > 0) overBudget -= 1;
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
    pass?.dispose?.();
    pass = null;
    visible = true;
    tier = 0;
    tiers = message.tiers.length > 0 ? message.tiers : tiers;
    cssWidth = message.width;
    cssHeight = message.height;
    ratio = message.ratio;
    state = message.state;
    startedAt = performance.now();
    let built: RenderSurfacePass<TState> | null;
    try {
      built = await definition.build(message.canvas);
    } catch (error) {
      if (mine === generation) emit({ kind: "FAILED", surface, reason: String(error) });
      return;
    }
    // Superseded while the pipeline was compiling: this pass is drawing to a
    // canvas nobody is showing any more.
    if (mine !== generation || stopped) {
      built?.dispose?.();
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
        pass?.dispose?.();
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
    pass?.dispose?.();
    pass = null;
  };
};
