import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  serveRenderSurface,
  type RenderSurfaceFrame,
  type RenderSurfaceScope,
} from "./renderSurfaceClient";
import { createRenderStateChannel } from "./renderStateChannel";
import type { RenderSurfaceCommand, RenderSurfaceEvent } from "./renderSurface";

/**
 * A worker global, faked well enough to drive the loop.
 *
 * The point of these tests is the part a surface author never writes twice —
 * pacing, sizing, the ladder, and the visibility contract. None of that needs
 * a GPU, and a test that needed one would only run where a GPU is.
 */
const makeScope = () => {
  let listener: ((event: MessageEvent) => void) | null = null;
  const sent: RenderSurfaceEvent[] = [];
  const scope: RenderSurfaceScope = {
    postMessage: (message) => sent.push(message as RenderSurfaceEvent),
    addEventListener: (_type, handler) => {
      listener = handler;
    },
    removeEventListener: () => {
      listener = null;
    },
  };
  const send = (command: RenderSurfaceCommand<unknown>) =>
    listener?.({ data: command } as MessageEvent);
  return { scope, sent, send };
};

const canvas = {} as OffscreenCanvas;

const init = (
  overrides: Partial<Extract<RenderSurfaceCommand<unknown>, { kind: "INIT" }>> = {},
): RenderSurfaceCommand<unknown> => ({
  kind: "INIT",
  surface: "test",
  canvas,
  tiers: [
    { scale: 1, cadenceMs: 25, detail: 100 },
    { scale: 0.5, cadenceMs: 50, detail: 40 },
  ],
  ratio: 2,
  width: 100,
  height: 50,
  ...overrides,
});

describe("a render surface inside a worker", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("sizes the backing store from the rung and the ratio, not from the caller", async () => {
    const resize = vi.fn();
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "webgpu", dispose: () => undefined, resize, draw: () => undefined }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);

    // 100 CSS px at ratio 2 and scale 1. A surface that read `devicePixelRatio`
    // itself would be reading it in the worker, where it is not the page's.
    expect(resize).toHaveBeenCalledWith(200, 100);
    expect(sent.find((event) => event.kind === "READY")).toMatchObject({ lane: "webgpu" });
  });

  it("paces itself to the rung's cadence rather than to a display", async () => {
    const draw = vi.fn();
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    const first = draw.mock.calls.length;

    // 250ms at a 25ms cadence is ten frames, give or take the one in flight.
    await vi.advanceTimersByTimeAsync(250);
    const drawn = draw.mock.calls.length - first;
    expect(drawn).toBeGreaterThanOrEqual(8);
    expect(drawn).toBeLessThanOrEqual(12);
  });

  it("stops completely when nothing can see it, and starts again when something can", async () => {
    const draw = vi.fn();
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(100);
    send({ kind: "VISIBILITY", surface: "test", visible: false });
    const atRest = draw.mock.calls.length;

    // Not "fewer frames" — none. An invisible surface that still draws is the
    // bug this contract exists to make impossible.
    await vi.advanceTimersByTimeAsync(500);
    expect(draw.mock.calls.length).toBe(atRest);

    send({ kind: "VISIBILITY", surface: "test", visible: true });
    await vi.advanceTimersByTimeAsync(100);
    expect(draw.mock.calls.length).toBeGreaterThan(atRest);
  });

  it("holds a floor the host pins, and resizes to it", async () => {
    const resize = vi.fn();
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize, draw: () => undefined }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    resize.mockClear();

    send({ kind: "TIER_FLOOR", surface: "test", tier: 1 });
    await vi.advanceTimersByTimeAsync(0);

    // Rung 1 is half scale: 100 CSS px * 0.5 * ratio 2.
    expect(resize).toHaveBeenCalledWith(100, 50);
    const diagnostics = sent.filter((event) => event.kind === "DIAGNOSTICS").at(-1);
    expect(diagnostics).toMatchObject({ diagnostics: { tier: 1, scale: 0.5 } });
  });

  it("ignores messages addressed to another surface", async () => {
    const draw = vi.fn();
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw }) },
      scope,
    );

    send(init({ surface: "somebody-else" }));
    await vi.advanceTimersByTimeAsync(100);
    // One worker can serve several surfaces; a shared clock that drew for the
    // wrong canvas would be very hard to see and very easy to write.
    expect(draw).not.toHaveBeenCalled();
  });

  it("reports a lane it could not build instead of spinning an empty loop", async () => {
    const { scope, sent, send } = makeScope();
    serveRenderSurface("test", { build: () => null }, scope);

    send(init());
    await vi.advanceTimersByTimeAsync(100);

    expect(sent.find((event) => event.kind === "FAILED")).toMatchObject({
      reason: "no lane available",
    });
    expect(sent.find((event) => event.kind === "READY")).toBeUndefined();
  });
});

describe("a surface that is stopped and started again", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  /*
   * React mounts an effect, tears it down and mounts it again on every mount in
   * development StrictMode. A surface server lives for the life of the worker,
   * so it sees INIT, STOP, INIT — and the version of this that treated STOP as
   * terminal left the second mount with a transferred canvas and nothing
   * drawing to it. In development only, which is the worst place for it.
   */
  it("revives on a second INIT rather than staying dead", async () => {
    const draw = vi.fn();
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(50);
    send({ kind: "STOP", surface: "test" });
    const afterStop = draw.mock.calls.length;
    await vi.advanceTimersByTimeAsync(200);
    expect(draw.mock.calls.length).toBe(afterStop);

    send(init());
    await vi.advanceTimersByTimeAsync(100);
    expect(draw.mock.calls.length).toBeGreaterThan(afterStop);
    expect(sent.filter((event) => event.kind === "READY")).toHaveLength(2);
  });

  it("throws away a pass that finished building after it was superseded", async () => {
    const disposeFirst = vi.fn();
    const disposeSecond = vi.fn();
    let call = 0;
    let release: undefined | (() => void);
    const { scope, sent, send } = makeScope();

    serveRenderSurface(
      "test",
      {
        build: async () => {
          call += 1;
          const mine = call;
          if (mine === 1) {
            // Still compiling when the second INIT arrives.
            await new Promise<void>((resolve) => {
              release = resolve;
            });
          }
          return {
            lane: "2d",
            resize: () => undefined,
            draw: () => undefined,
            dispose: mine === 1 ? disposeFirst : disposeSecond,
          };
        },
      },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    send(init());
    await vi.advanceTimersByTimeAsync(0);
    release?.();
    await vi.advanceTimersByTimeAsync(50);

    // The stale pass is disposed and never installed; the live one is running.
    expect(disposeFirst).toHaveBeenCalledTimes(1);
    expect(disposeSecond).not.toHaveBeenCalled();
    expect(sent.filter((event) => event.kind === "READY")).toHaveLength(1);
  });
});

/**
 * A clock that runs faster than the timers driving it.
 *
 * The loop measures itself with `performance.now()` and paces itself with
 * `setTimeout`. Making perceived time advance by `factor` for every tick of
 * timer time is therefore the same thing, from the ladder's point of view, as
 * a device taking `factor` times its budget to draw — without a test needing a
 * GPU, or a real second, to say so.
 */
const slowDevice = (factor: number) => {
  const origin = Date.now();
  return vi.spyOn(performance, "now").mockImplementation(() => (Date.now() - origin) * factor);
};

const demotions = (sent: RenderSurfaceEvent[]) =>
  sent.filter((event) => event.kind === "DIAGNOSTICS" && event.diagnostics.tier > 0);

describe("the quality ladder", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  /*
   * The bug this pins.
   *
   * The miss threshold used to be the cadence plus a flat `1000 / 30` ms. On
   * the 25 ms rung above that put the line at 58.3 ms — 17.1 fps — so a phone
   * holding the surface at 20 fps was failing its budget by 125% and recorded
   * no misses at all. It pinned to the best rung, at full resolution, with the
   * GPU saturated, for as long as the page was open. This is that device.
   */
  it("demotes a device holding half its cadence, which a flat slack never did", async () => {
    slowDevice(2);
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw: () => undefined }) },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    expect(demotions(sent)).toHaveLength(0);

    // Six misses at a 25ms cadence. Well inside the second that a viewer would
    // otherwise spend watching the figure stutter at the wrong rung.
    await vi.advanceTimersByTimeAsync(400);
    expect(demotions(sent).length).toBeGreaterThan(0);
    expect(demotions(sent).at(-1)).toMatchObject({ diagnostics: { tier: 1, scale: 0.5 } });
  });

  it("leaves a device that is holding cadence alone, however long it runs", async () => {
    slowDevice(1);
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize: () => undefined, draw: () => undefined }) },
      scope,
    );

    send(init());
    // Twenty seconds — eight hundred frames. Misses accumulate rather than
    // decaying, so without the forgiveness rule a long clean run would collect
    // enough stray late frames to demote hardware that never struggled.
    await vi.advanceTimersByTimeAsync(20_000);
    expect(demotions(sent)).toHaveLength(0);
  });

  /*
   * The host reads the device; the worker cannot. `matchMedia` does not exist
   * in a worker, so a surface asked to work out its own opening rung would be
   * left guessing from `hardwareConcurrency` alone.
   */
  it("opens on the rung the host chose rather than always at the top", async () => {
    const resize = vi.fn();
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize, draw: () => undefined }) },
      scope,
    );

    send(init({ tier: 1 }));
    await vi.advanceTimersByTimeAsync(0);

    // Rung 1 is half scale: 100 CSS px * 0.5 * ratio 2.
    expect(resize).toHaveBeenCalledWith(100, 50);
  });

  it("clamps a prior the host got wrong instead of indexing off the ladder", async () => {
    const resize = vi.fn();
    const { scope, sent, send } = makeScope();
    serveRenderSurface(
      "test",
      { build: () => ({ lane: "2d", dispose: () => undefined, resize, draw: () => undefined }) },
      scope,
    );

    send(init({ tier: 99 }));
    await vi.advanceTimersByTimeAsync(0);

    expect(resize).toHaveBeenCalledWith(100, 50);
    expect(sent.find((event) => event.kind === "READY")).toBeDefined();
  });
});

describe("a surface reading shared state", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const drive = async (
    stateBuffer: SharedArrayBuffer | undefined,
    frames: RenderSurfaceFrame[],
  ) => {
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      {
        build: () => ({
          lane: "2d",
          dispose: () => undefined,
          resize: () => undefined,
          draw: (_state, frame) => {
            // Snapshot, because the descriptor is reused between frames.
            frames.push({ ...frame, shared: frame.shared ? Float32Array.from(frame.shared) : null });
          },
        }),
      },
      scope,
    );
    send(init({ stateBuffer }));
    await vi.advanceTimersByTimeAsync(0);
    return send;
  };

  it("draws from memory the host wrote, with no message in between", async () => {
    const channel = createRenderStateChannel(8)!;
    const frames: RenderSurfaceFrame[] = [];

    channel.write((slot) => {
      slot.set([0.25, 0.5, 0.75]);
      return 3;
    });
    await drive(channel.buffer, frames);

    expect(Array.from(frames[0]!.shared!)).toEqual([0.25, 0.5, 0.75]);
    expect(frames[0]!.sharedGeneration).toBe(1);
  });

  it("holds the last generation across frames the host did not update", async () => {
    const channel = createRenderStateChannel(8)!;
    const frames: RenderSurfaceFrame[] = [];
    channel.write((slot) => {
      slot[0] = 42;
      return 1;
    });

    await drive(channel.buffer, frames);
    await vi.advanceTimersByTimeAsync(100);

    // Several frames drawn, one generation published: every frame sees it, and
    // only the first one paid for a copy.
    expect(frames.length).toBeGreaterThan(2);
    for (const frame of frames) {
      expect(Array.from(frame.shared!)).toEqual([42]);
      expect(frame.sharedGeneration).toBe(1);
    }
  });

  it("picks up a new generation the host publishes mid-flight", async () => {
    const channel = createRenderStateChannel(8)!;
    const frames: RenderSurfaceFrame[] = [];
    channel.write((slot) => {
      slot[0] = 1;
      return 1;
    });
    await drive(channel.buffer, frames);

    channel.write((slot) => {
      slot[0] = 2;
      return 1;
    });
    await vi.advanceTimersByTimeAsync(100);

    expect(frames.at(-1)!.shared![0]).toBe(2);
    expect(frames.at(-1)!.sharedGeneration).toBe(2);
  });

  it("runs exactly as before when no channel was negotiated", async () => {
    const frames: RenderSurfaceFrame[] = [];
    await drive(undefined, frames);
    await vi.advanceTimersByTimeAsync(50);

    expect(frames.length).toBeGreaterThan(0);
    expect(frames[0]!.shared).toBeNull();
    expect(frames[0]!.sharedGeneration).toBe(0);
  });

  it("ignores a buffer that is not one of ours rather than failing the surface", async () => {
    const frames: RenderSurfaceFrame[] = [];
    await drive(new SharedArrayBuffer(256), frames);
    await vi.advanceTimersByTimeAsync(50);

    expect(frames.length).toBeGreaterThan(0);
    expect(frames[0]!.shared).toBeNull();
  });
});

/**
 * Disposal, counted on every path that retires a pass.
 *
 * A pass holds textures, pipelines and buffers, and every one of them lives in
 * GPU memory outside the JS heap — invisible to a heap snapshot, to an
 * allocation profile, and to every retained-byte figure this package reports. A
 * leak here is a missed call, so the only way to know is to count the calls.
 *
 * On an owned worker a missed call is survivable: the thread dies and the
 * browser reclaims. On a *shared* worker — the arrangement this lane recommends,
 * because a device per pass means three devices to draw three figures — the
 * worker outlives the pass and nothing is reclaimed at all.
 */
describe("retiring a pass", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const countingPass = (dispose: () => void) => ({
    build: () => ({ lane: "2d", resize: () => undefined, draw: () => undefined, dispose }),
  });

  it("disposes on STOP", async () => {
    const dispose = vi.fn();
    const { scope, send } = makeScope();
    serveRenderSurface("test", countingPass(dispose), scope);

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    expect(dispose).not.toHaveBeenCalled();

    send({ kind: "STOP", surface: "test" });
    expect(dispose).toHaveBeenCalledTimes(1);
  });

  it("disposes the old pass when a second INIT replaces it", async () => {
    const disposals: number[] = [];
    let built = 0;
    const { scope, send } = makeScope();
    serveRenderSurface(
      "test",
      {
        build: () => {
          const mine = ++built;
          return {
            lane: "2d",
            resize: () => undefined,
            draw: () => undefined,
            dispose: () => disposals.push(mine),
          };
        },
      },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    // React remounts in development StrictMode, so this is the ordinary path
    // rather than an exotic one.
    send(init());
    await vi.advanceTimersByTimeAsync(0);

    expect(disposals).toEqual([1]);
    expect(built).toBe(2);
  });

  it("disposes a pass that finished building after it was superseded", async () => {
    const disposals: number[] = [];
    let built = 0;
    let release: undefined | (() => void);
    const { scope, send } = makeScope();

    serveRenderSurface(
      "test",
      {
        build: async () => {
          const mine = ++built;
          if (mine === 1) {
            await new Promise<void>((resolve) => {
              release = resolve;
            });
          }
          return {
            lane: "2d",
            resize: () => undefined,
            draw: () => undefined,
            dispose: () => disposals.push(mine),
          };
        },
      },
      scope,
    );

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    send(init());
    await vi.advanceTimersByTimeAsync(0);
    release?.();
    await vi.advanceTimersByTimeAsync(50);

    // The stale pass compiled a pipeline before it learned it was stale. That
    // pipeline is still GPU memory, and it is released.
    expect(disposals).toEqual([1]);
  });

  it("disposes when the server itself is torn down", async () => {
    const dispose = vi.fn();
    const { scope, send } = makeScope();
    const stop = serveRenderSurface("test", countingPass(dispose), scope);

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    stop();

    expect(dispose).toHaveBeenCalledTimes(1);
  });

  it("disposes exactly once across a full mount, remount and teardown", async () => {
    const dispose = vi.fn();
    const { scope, send } = makeScope();
    const stop = serveRenderSurface("test", countingPass(dispose), scope);

    send(init());
    await vi.advanceTimersByTimeAsync(0);
    send({ kind: "STOP", surface: "test" });
    expect(dispose).toHaveBeenCalledTimes(1);

    // Revived, then torn down again: two passes, two disposals, never three.
    send(init());
    await vi.advanceTimersByTimeAsync(0);
    stop();
    expect(dispose).toHaveBeenCalledTimes(2);

    // And a disposer called twice does not retire a pass that is already gone.
    stop();
    expect(dispose).toHaveBeenCalledTimes(2);
  });
});

/**
 * Prewarming: doing the canvas-independent half before the canvas exists.
 *
 * Standing a WebGPU surface up is a chain — spawn the worker, load its module,
 * request an adapter, request a device, compile shaders, create pipelines,
 * configure the context, draw — and only the last two need a canvas. Everything
 * before them used to run after the transfer, which means after a DOM element
 * existed, which means after the component owning it had mounted.
 */
describe("a surface warmed before its canvas arrives", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const warmable = (log: string[]) => ({
    warm: async () => {
      log.push("warm:start");
      await new Promise<void>((resolve) => setTimeout(resolve, 100));
      log.push("warm:done");
      return { device: "ready" };
    },
    build: (_canvas: OffscreenCanvas, warmed: { device: string } | undefined) => {
      log.push(`build:${warmed?.device ?? "cold"}`);
      return { lane: "webgpu", resize: () => undefined, draw: () => undefined, dispose: () => undefined };
    },
  });

  it("starts the expensive half on WARM, before any canvas is transferred", async () => {
    const log: string[] = [];
    const { scope, send } = makeScope();
    serveRenderSurface("test", warmable(log), scope);

    send({ kind: "WARM", surface: "test" });
    await vi.advanceTimersByTimeAsync(100);

    // Warmed to completion with no canvas anywhere in sight.
    expect(log).toEqual(["warm:start", "warm:done"]);
  });

  it("hands the warm result to build, so the build does not repeat it", async () => {
    const log: string[] = [];
    const { scope, send } = makeScope();
    serveRenderSurface("test", warmable(log), scope);

    send({ kind: "WARM", surface: "test" });
    await vi.advanceTimersByTimeAsync(100);
    send(init());
    await vi.advanceTimersByTimeAsync(0);

    expect(log).toEqual(["warm:start", "warm:done", "build:ready"]);
  });

  /*
   * The order that actually happens: a host prewarms and mounts in the same
   * tick. INIT has to *wait* for the warm rather than find it unfinished and
   * start over, or prewarming would make a fast mount slower.
   */
  it("waits for an in-flight warm rather than building cold", async () => {
    const log: string[] = [];
    const { scope, send, sent } = makeScope();
    serveRenderSurface("test", warmable(log), scope);

    send({ kind: "WARM", surface: "test" });
    send(init());
    await vi.advanceTimersByTimeAsync(0);
    expect(log).toEqual(["warm:start"]); // build has not run

    await vi.advanceTimersByTimeAsync(100);
    expect(log).toEqual(["warm:start", "warm:done", "build:ready"]);
    expect(sent.find((event) => event.kind === "READY")).toBeDefined();
  });

  it("warms once however many times it is asked", async () => {
    const log: string[] = [];
    const { scope, send } = makeScope();
    serveRenderSurface("test", warmable(log), scope);

    send({ kind: "WARM", surface: "test" });
    send({ kind: "WARM", surface: "test" });
    send({ kind: "WARM", surface: "test" });
    await vi.advanceTimersByTimeAsync(100);

    // Warming twice would build a second device.
    expect(log.filter((entry) => entry === "warm:start")).toHaveLength(1);
  });

  it("builds cold when a surface was never warmed", async () => {
    const log: string[] = [];
    const { scope, send } = makeScope();
    serveRenderSurface("test", warmable(log), scope);

    send(init());
    await vi.advanceTimersByTimeAsync(0);

    // Prewarming is the caller's choice; a cold start stays correct.
    expect(log).toEqual(["build:cold"]);
  });

  /*
   * A failed prewarm costs the latency it was meant to save and nothing else.
   * Reporting it as a failure would show an error for a surface that is drawing
   * perfectly well, one frame later than it might have.
   */
  it("degrades to a cold build when warming fails, and says so without failing", async () => {
    const log: string[] = [];
    const { scope, send, sent } = makeScope();
    serveRenderSurface(
      "test",
      {
        warm: () => Promise.reject(new Error("no adapter")),
        build: (_canvas: OffscreenCanvas, warmed: unknown) => {
          log.push(`build:${warmed === undefined ? "cold" : "warm"}`);
          return { lane: "2d", resize: () => undefined, draw: () => undefined, dispose: () => undefined };
        },
      },
      scope,
    );

    send({ kind: "WARM", surface: "test" });
    await vi.advanceTimersByTimeAsync(0);
    send(init());
    await vi.advanceTimersByTimeAsync(10);

    expect(log).toEqual(["build:cold"]);
    expect(sent.find((event) => event.kind === "WARNING")).toMatchObject({
      reason: expect.stringContaining("no adapter"),
    });
    expect(sent.find((event) => event.kind === "FAILED")).toBeUndefined();
    expect(sent.find((event) => event.kind === "READY")).toBeDefined();
  });
});
