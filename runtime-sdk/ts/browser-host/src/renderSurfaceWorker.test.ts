import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createRenderSurfaceWorker } from "./renderSurfaceWorker";
import type { RenderSurfaceCommand, RenderSurfaceEvent } from "./renderSurface";
import type { RenderSurfaceScope } from "./renderSurfaceClient";

const makeScope = () => {
  const listeners = new Set<(event: MessageEvent) => void>();
  const sent: RenderSurfaceEvent[] = [];
  const scope: RenderSurfaceScope = {
    postMessage: (message) => sent.push(message as RenderSurfaceEvent),
    addEventListener: (_type, handler) => listeners.add(handler),
    removeEventListener: (_type, handler) => listeners.delete(handler),
  };
  const send = (command: RenderSurfaceCommand<unknown>) => {
    for (const listener of [...listeners]) listener({ data: command } as MessageEvent);
  };
  return { scope, sent, send, listenerCount: () => listeners.size };
};

const init = (surface: string): RenderSurfaceCommand<unknown> => ({
  kind: "INIT",
  surface,
  canvas: {} as OffscreenCanvas,
  tiers: [{ scale: 1, cadenceMs: 25 }],
  ratio: 2,
  width: 100,
  height: 100,
});

const pass = () => ({
  lane: "webgpu",
  resize: () => undefined,
  draw: () => undefined,
  dispose: () => undefined,
});

describe("one worker serving several surfaces", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  /**
   * The whole point. Three figures on one page used to mean three adapters,
   * three devices and three sets of pipelines even when they shared a worker,
   * because every surface's `build` reached for its own.
   */
  it("acquires one device for however many surfaces it serves", async () => {
    const acquire = vi.fn(async () => ({ device: "shared" }));
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire, scope });

    const seen: unknown[] = [];
    for (const name of ["paper", "blackhole", "footer"]) {
      worker.serve(name, {
        build: (_canvas, shared) => {
          seen.push(shared);
          return pass();
        },
      });
    }

    for (const name of ["paper", "blackhole", "footer"]) send(init(name));
    await vi.advanceTimersByTimeAsync(0);

    expect(acquire).toHaveBeenCalledTimes(1);
    expect(seen).toHaveLength(3);
    // The same object, not three equal ones.
    expect(new Set(seen).size).toBe(1);
  });

  /*
   * Three surfaces on one page mount in the same tick, so their INITs arrive
   * back to back. A flag-guarded acquisition would find itself unset three
   * times and request three devices — the exact bug, reintroduced by the code
   * meant to fix it.
   */
  it("shares one in-flight acquisition rather than starting three", async () => {
    let resolve: ((value: { device: string }) => void) | undefined;
    const acquire = vi.fn(
      () =>
        new Promise<{ device: string }>((r) => {
          resolve = r;
        }),
    );
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire, scope });

    const built: unknown[] = [];
    for (const name of ["a", "b", "c"]) {
      worker.serve(name, {
        build: (_canvas, shared) => {
          built.push(shared);
          return pass();
        },
      });
      send(init(name));
    }
    await vi.advanceTimersByTimeAsync(0);

    expect(acquire).toHaveBeenCalledTimes(1);
    expect(built).toHaveLength(0); // all three waiting on the one device

    resolve?.({ device: "shared" });
    await vi.advanceTimersByTimeAsync(0);
    expect(built).toHaveLength(3);
    expect(new Set(built).size).toBe(1);
  });

  it("dispatches by lookup, with one listener however many surfaces there are", () => {
    const { scope, listenerCount } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire: () => ({ device: 1 }), scope });

    for (let i = 0; i < 8; i += 1) worker.serve(`surface-${i}`, { build: () => pass() });

    // Eight `serveRenderSurface` calls on a bare scope would be eight listeners,
    // seven of which decide every message was not for them.
    expect(listenerCount()).toBe(1);
    expect(worker.size).toBe(8);
  });

  it("does not acquire anything until a surface actually needs it", () => {
    const acquire = vi.fn(() => ({ device: 1 }));
    const { scope } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire, scope });

    worker.serve("paper", { build: () => pass() });
    // Served, but nothing has arrived: a page that renders no surfaces should
    // not have negotiated a GPU device.
    expect(acquire).not.toHaveBeenCalled();
    expect(worker.acquired).toBe(false);
  });

  /**
   * A single-surface worker can be terminated and the browser reclaims. A shared
   * worker outlives every surface in it, so something has to notice when the
   * last one goes — and that is the case where a leaked device is never
   * reclaimed at all.
   */
  it("releases the device when the last surface goes, and not before", async () => {
    const release = vi.fn();
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire: () => ({ device: "shared" }), release, scope });

    const stopA = worker.serve("a", { build: () => pass() });
    const stopB = worker.serve("b", { build: () => pass() });
    send(init("a"));
    send(init("b"));
    await vi.advanceTimersByTimeAsync(0);

    stopA();
    expect(release).not.toHaveBeenCalled();
    stopB();
    expect(release).toHaveBeenCalledTimes(1);
    expect(release).toHaveBeenCalledWith({ device: "shared" });
  });

  it("retires every surface and releases once when the worker is disposed", async () => {
    const release = vi.fn();
    const disposals: string[] = [];
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire: () => ({ device: 1 }), release, scope });

    for (const name of ["a", "b", "c"]) {
      worker.serve(name, {
        build: () => ({ ...pass(), dispose: () => disposals.push(name) }),
      });
      send(init(name));
    }
    await vi.advanceTimersByTimeAsync(0);

    worker.dispose();
    expect(disposals.sort()).toEqual(["a", "b", "c"]);
    expect(release).toHaveBeenCalledTimes(1);

    worker.dispose();
    expect(release).toHaveBeenCalledTimes(1);
  });

  /*
   * The path where a shared device is leaked without anybody being able to see
   * it: the last surface leaves while acquisition is still in flight. Dropping
   * the promise loses the device that is about to arrive.
   */
  it("releases a device that arrives after the last surface has gone", async () => {
    const release = vi.fn();
    let resolve: ((value: { device: string }) => void) | undefined;
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({
      acquire: () => new Promise<{ device: string }>((r) => (resolve = r)),
      release,
      scope,
    });

    const stop = worker.serve("a", { build: () => pass() });
    send(init("a"));
    await vi.advanceTimersByTimeAsync(0);
    stop();
    expect(release).not.toHaveBeenCalled();

    // The device the departed surface asked for turns up anyway.
    resolve?.({ device: "late" });
    await vi.advanceTimersByTimeAsync(0);
    expect(release).toHaveBeenCalledWith({ device: "late" });
  });

  it("warms each surface on the one shared device", async () => {
    const acquire = vi.fn(async () => ({ device: "shared" }));
    const warmed: unknown[] = [];
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire, scope });

    worker.serve("a", { warm: (shared) => void warmed.push(shared), build: () => pass() });
    worker.serve("b", { warm: (shared) => void warmed.push(shared), build: () => pass() });

    send({ kind: "WARM", surface: "a" });
    send({ kind: "WARM", surface: "b" });
    await vi.advanceTimersByTimeAsync(0);

    expect(acquire).toHaveBeenCalledTimes(1);
    expect(warmed).toHaveLength(2);
    expect(new Set(warmed).size).toBe(1);
  });

  it("builds on the shared device even for a surface that was never warmed", async () => {
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire: () => ({ device: "shared" }), scope });

    let seen: unknown;
    worker.serve("a", {
      build: (_canvas, shared) => {
        seen = shared;
        return pass();
      },
    });
    send(init("a"));
    await vi.advanceTimersByTimeAsync(0);

    expect(seen).toEqual({ device: "shared" });
  });

  it("keeps one surface's messages away from another's", async () => {
    const drawn: string[] = [];
    const { scope, send } = makeScope();
    const worker = createRenderSurfaceWorker({ acquire: () => ({ device: 1 }), scope });

    for (const name of ["a", "b"]) {
      worker.serve(name, {
        build: () => ({ ...pass(), draw: () => drawn.push(name) }),
      });
    }
    send(init("a"));
    await vi.advanceTimersByTimeAsync(60);

    // Only the surface that was initialised is drawing.
    expect(new Set(drawn)).toEqual(new Set(["a"]));
  });
});
