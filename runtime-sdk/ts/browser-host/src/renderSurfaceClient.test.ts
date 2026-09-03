import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { serveRenderSurface, type RenderSurfaceScope } from "./renderSurfaceClient";
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
      { build: () => ({ lane: "webgpu", resize, draw: () => undefined }) },
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
      { build: () => ({ lane: "2d", resize: () => undefined, draw }) },
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
      { build: () => ({ lane: "2d", resize: () => undefined, draw }) },
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
      { build: () => ({ lane: "2d", resize, draw: () => undefined }) },
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
      { build: () => ({ lane: "2d", resize: () => undefined, draw }) },
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
      { build: () => ({ lane: "2d", resize: () => undefined, draw }) },
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
