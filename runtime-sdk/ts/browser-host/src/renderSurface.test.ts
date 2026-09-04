import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createRenderSurfaceHost,
  prewarmRenderSurface,
  probeRenderSurface,
  type RenderSurfaceCommand,
  type RenderSurfaceEvent,
} from "./renderSurface";

describe("render surface host", () => {
  const originalOffscreen = globalThis.OffscreenCanvas;
  const originalWorker = globalThis.Worker;
  const originalCanvas = globalThis.HTMLCanvasElement;

  beforeEach(() => {
    vi.useFakeTimers();

    // Stub browser capabilities in Node test environment
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = class MockOffscreen {};
    (globalThis as unknown as { Worker: unknown }).Worker = class MockWorker {};
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = class MockCanvas {
      transferControlToOffscreen() {
        return new (globalThis as unknown as { OffscreenCanvas: new () => OffscreenCanvas }).OffscreenCanvas();
      }
    };
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();

    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = originalOffscreen;
    (globalThis as unknown as { Worker: unknown }).Worker = originalWorker;
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = originalCanvas;
  });

  it("probes render surface capabilities", () => {
    const probe = probeRenderSurface();
    expect(probe.offscreenCanvas).toBe(true);
    expect(probe.worker).toBe(true);
    expect(probe.transferControl).toBe(true);
  });

  it("degrades gracefully to main-thread mode when worker construction fails", () => {
    const canvas = {
      clientWidth: 300,
      clientHeight: 200,
      transferControlToOffscreen: vi.fn(),
    } as unknown as HTMLCanvasElement;

    const host = createRenderSurfaceHost({
      canvas,
      surface: "test-failure",
      tiers: [{ scale: 1, cadenceMs: 16 }],
      createWorker: () => {
        throw new Error("Worker blocked by CSP");
      },
    });

    expect(host.mode).toBe("main-thread");
    expect(host.issues).toContain("worker construction failed");
    expect(canvas.transferControlToOffscreen).not.toHaveBeenCalled();
  });

  it("degrades gracefully to main-thread mode when transferControlToOffscreen throws", () => {
    const mockWorker = {
      postMessage: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;

    const canvas = {
      clientWidth: 300,
      clientHeight: 200,
      transferControlToOffscreen: () => {
        throw new Error("Cannot transfer control from a canvas that has already transferred control");
      },
    } as unknown as HTMLCanvasElement;

    const host = createRenderSurfaceHost({
      canvas,
      surface: "test-transfer-fail",
      tiers: [{ scale: 1, cadenceMs: 16 }],
      createWorker: () => mockWorker,
    });

    expect(host.mode).toBe("main-thread");
    expect(host.issues.some((i) => i.includes("canvas transfer failed"))).toBe(true);
    expect(mockWorker.terminate).toHaveBeenCalled();
  });

  it("initializes offscreen canvas and dispatches INIT command", () => {
    const sentCommands: RenderSurfaceCommand<unknown>[] = [];
    const mockWorker = {
      postMessage: vi.fn((cmd) => sentCommands.push(cmd)),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;

    const mockOffscreen = {} as OffscreenCanvas;
    const canvas = {
      clientWidth: 400,
      clientHeight: 300,
      transferControlToOffscreen: () => mockOffscreen,
    } as unknown as HTMLCanvasElement;

    const host = createRenderSurfaceHost({
      canvas,
      surface: "visual-main",
      tiers: [{ scale: 1, cadenceMs: 25, detail: 50 }],
      maxRatio: 2,
      initialState: { color: "amber" },
      createWorker: () => mockWorker,
      cullOffscreen: false,
    });

    expect(host.mode).toBe("worker");
    expect(sentCommands).toHaveLength(1);
    expect(sentCommands[0]).toMatchObject({
      kind: "INIT",
      surface: "visual-main",
      canvas: mockOffscreen,
      width: 400,
      height: 300,
      state: { color: "amber" },
    });
  });

  it("coalesces rapid setState calls into a single post per frame", async () => {
    const sentCommands: RenderSurfaceCommand<unknown>[] = [];
    const mockWorker = {
      postMessage: vi.fn((cmd) => sentCommands.push(cmd)),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;

    const canvas = {
      clientWidth: 200,
      clientHeight: 100,
      transferControlToOffscreen: () => ({} as OffscreenCanvas),
    } as unknown as HTMLCanvasElement;

    const host = createRenderSurfaceHost({
      canvas,
      surface: "coalesce-test",
      tiers: [{ scale: 1, cadenceMs: 16 }],
      createWorker: () => mockWorker,
      cullOffscreen: false,
    });

    host.setState({ frame: 1 });
    host.setState({ frame: 2 });
    host.setState({ frame: 3 });

    // INIT was sent synchronously
    expect(sentCommands.filter((c) => c.kind === "STATE")).toHaveLength(0);

    // Advance timers so rAF/timer flush triggers
    await vi.advanceTimersByTimeAsync(30);

    const stateCommands = sentCommands.filter((c) => c.kind === "STATE");
    expect(stateCommands).toHaveLength(1);
    expect(stateCommands[0]).toMatchObject({
      kind: "STATE",
      surface: "coalesce-test",
      state: { frame: 3 },
    });
  });

  it("handles visibility, tier floor and disposal with shared worker preservation", () => {
    const sentCommands: RenderSurfaceCommand<unknown>[] = [];
    let workerListener: ((event: MessageEvent<RenderSurfaceEvent>) => void) | null = null;

    const mockWorker = {
      postMessage: vi.fn((cmd) => sentCommands.push(cmd)),
      addEventListener: vi.fn((_type: string, handler: unknown) => {
        workerListener = handler as (event: MessageEvent<RenderSurfaceEvent>) => void;
      }),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;

    const canvas = {
      clientWidth: 100,
      clientHeight: 100,
      transferControlToOffscreen: () => ({} as OffscreenCanvas),
    } as unknown as HTMLCanvasElement;

    const onDiagnostics = vi.fn();
    const onFailed = vi.fn();

    const host = createRenderSurfaceHost({
      canvas,
      surface: "lifecycle-test",
      tiers: [{ scale: 1, cadenceMs: 25 }],
      createWorker: () => mockWorker,
      ownsWorker: false,
      cullOffscreen: false,
      onDiagnostics,
      onFailed,
    });

    host.setVisible(false);
    expect(sentCommands).toContainEqual({ kind: "VISIBILITY", surface: "lifecycle-test", visible: false });

    host.setTierFloor(2);
    expect(sentCommands).toContainEqual({ kind: "TIER_FLOOR", surface: "lifecycle-test", tier: 2 });

    // Simulate worker events
    if (workerListener) {
      (workerListener as (event: MessageEvent<RenderSurfaceEvent>) => void)({
        data: { kind: "FAILED", surface: "lifecycle-test", reason: "Device lost" },
      } as MessageEvent<RenderSurfaceEvent>);
    }
    expect(onFailed).toHaveBeenCalledWith("Device lost");

    host.dispose();

    expect(sentCommands).toContainEqual({ kind: "STOP", surface: "lifecycle-test" });
    expect(mockWorker.removeEventListener).toHaveBeenCalled();
    // ownsWorker is false: worker must NOT be terminated
    expect(mockWorker.terminate).not.toHaveBeenCalled();
  });
});

/**
 * The host half of the device prior.
 *
 * It has to run here and not in the worker: `matchMedia` does not exist in a
 * worker, so the pointer, viewport and reduced-motion signals are unreadable
 * from the side that owns the ladder. The host reads them once, before the
 * canvas is transferred, and sends the rung with the transfer.
 */
describe("render surface opening rung", () => {
  const originalOffscreen = globalThis.OffscreenCanvas;
  const originalWorker = globalThis.Worker;
  const originalCanvas = globalThis.HTMLCanvasElement;

  const ladder = [
    { scale: 1, cadenceMs: 25, detail: 100 },
    { scale: 0.75, cadenceMs: 30, detail: 70 },
    { scale: 0.5, cadenceMs: 40, detail: 40 },
    { scale: 0.35, cadenceMs: 50, detail: 20 },
    { scale: 0.25, cadenceMs: 66, detail: 10 },
  ];

  const stand = (options: { startingTier?: number } = {}) => {
    const sent: RenderSurfaceCommand<unknown>[] = [];
    const listeners: ((event: MessageEvent<RenderSurfaceEvent>) => void)[] = [];
    const worker = {
      postMessage: vi.fn((command) => sent.push(command)),
      addEventListener: vi.fn((_type, handler) => listeners.push(handler)),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;
    const canvas = {
      clientWidth: 390,
      clientHeight: 844,
      transferControlToOffscreen: () => ({}) as OffscreenCanvas,
    } as unknown as HTMLCanvasElement;

    const diagnostics: unknown[] = [];
    const host = createRenderSurfaceHost({
      canvas,
      surface: "figure",
      tiers: ladder,
      createWorker: () => worker,
      cullOffscreen: false,
      onDiagnostics: (report) => diagnostics.push(report),
      ...options,
    });
    return { host, sent, listeners, diagnostics };
  };

  beforeEach(() => {
    vi.useFakeTimers();
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = class {};
    (globalThis as unknown as { Worker: unknown }).Worker = class {};
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = class {
      transferControlToOffscreen() {
        return {};
      }
    };
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = originalOffscreen;
    (globalThis as unknown as { Worker: unknown }).Worker = originalWorker;
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = originalCanvas;
  });

  it("opens a desktop at the best rung, exactly as it did before the prior existed", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 16, deviceMemory: 32 });
    vi.stubGlobal("devicePixelRatio", 1);

    const { sent } = stand();
    expect(sent[0]).toMatchObject({ kind: "INIT", tier: 0 });
  });

  it("opens a phone-shaped device below the top rung without pinning it to the bottom", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("coarse") || query.includes("48rem"),
    }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 8, deviceMemory: 8 });
    vi.stubGlobal("devicePixelRatio", 3);

    const { sent } = stand();
    const init = sent[0] as Extract<RenderSurfaceCommand<unknown>, { kind: "INIT" }>;
    // Display constraints alone cost at most half the ladder. A flagship phone
    // trips every one of them and still outruns a laptop.
    expect(init.tier).toBe(2);
  });

  it("lets a caller override the prior with something it knows and the browser does not", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 2, deviceMemory: 2 });

    const { sent } = stand({ startingTier: 0 });
    expect(sent[0]).toMatchObject({ tier: 0 });
  });

  it("clamps an out-of-range override rather than indexing off the ladder", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("navigator", {});

    expect((stand({ startingTier: 99 }).sent[0] as { tier: number }).tier).toBe(ladder.length - 1);
    expect((stand({ startingTier: -5 }).sent[0] as { tier: number }).tier).toBe(0);
  });

  /*
   * The diagnostic used to report `tier: 0` on READY whatever the surface did,
   * which made every sink believe every device started at the top — precisely
   * the claim the prior exists to stop being true.
   */
  it("reports the rung it actually opened on, not a constant", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 2, deviceMemory: 2 });

    const { listeners, diagnostics } = stand();
    listeners[0]?.({
      data: { kind: "READY", surface: "figure", lane: "webgl2" },
    } as MessageEvent<RenderSurfaceEvent>);

    expect(diagnostics[0]).toMatchObject({
      tier: ladder.length - 1,
      scale: ladder[ladder.length - 1]!.scale,
      cadenceMs: ladder[ladder.length - 1]!.cadenceMs,
      lane: "webgl2",
    });
  });

  it("passes a failed lane to the caller instead of leaving a blank rectangle", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("navigator", {});

    const failures: string[] = [];
    const worker = {
      postMessage: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;
    let handler: ((event: MessageEvent<RenderSurfaceEvent>) => void) | undefined;
    (worker.addEventListener as ReturnType<typeof vi.fn>).mockImplementation((_t, h) => {
      handler = h;
    });

    createRenderSurfaceHost({
      canvas: {
        clientWidth: 100,
        clientHeight: 100,
        transferControlToOffscreen: () => ({}) as OffscreenCanvas,
      } as unknown as HTMLCanvasElement,
      surface: "figure",
      tiers: ladder,
      createWorker: () => worker,
      cullOffscreen: false,
      onFailed: (reason) => failures.push(reason),
    });

    handler?.({
      data: { kind: "FAILED", surface: "figure", reason: "no lane available" },
    } as MessageEvent<RenderSurfaceEvent>);
    expect(failures).toEqual(["no lane available"]);

    // A message for another surface on a shared worker is not this one's.
    handler?.({
      data: { kind: "FAILED", surface: "somebody-else", reason: "ignored" },
    } as MessageEvent<RenderSurfaceEvent>);
    expect(failures).toHaveLength(1);
  });
});

/**
 * The host's own observer callbacks, which the node environment skips whole.
 *
 * `ResizeObserver`, `IntersectionObserver` and `document` are all absent under
 * vitest's node environment, so the three paths that keep the worker's idea of
 * the canvas in step with the page's — size, interest, and tab visibility —
 * were constructed nowhere and run never.
 */
describe("render surface host observers", () => {
  const originalOffscreen = globalThis.OffscreenCanvas;
  const originalWorker = globalThis.Worker;
  const originalCanvas = globalThis.HTMLCanvasElement;

  type Observed = { callback: (entries: unknown[]) => void; disconnect: ReturnType<typeof vi.fn> };
  let sizeObservers: Observed[];
  let visibilityObservers: Observed[];
  let documentListeners: Map<string, () => void>;

  beforeEach(() => {
    vi.useFakeTimers();
    sizeObservers = [];
    visibilityObservers = [];
    documentListeners = new Map();

    const record = (into: Observed[]) =>
      class {
        disconnect = vi.fn();
        constructor(public callback: (entries: unknown[]) => void) {
          into.push(this as unknown as Observed);
        }
        observe() {}
      };

    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = class {};
    (globalThis as unknown as { Worker: unknown }).Worker = class {};
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = class {
      transferControlToOffscreen() {
        return {};
      }
    };
    vi.stubGlobal("ResizeObserver", record(sizeObservers));
    vi.stubGlobal("IntersectionObserver", record(visibilityObservers));
    vi.stubGlobal("document", {
      hidden: false,
      addEventListener: (type: string, handler: () => void) => documentListeners.set(type, handler),
      removeEventListener: (type: string) => documentListeners.delete(type),
    });
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 16, deviceMemory: 16 });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = originalOffscreen;
    (globalThis as unknown as { Worker: unknown }).Worker = originalWorker;
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = originalCanvas;
  });

  const stand = (ownsWorker = true) => {
    const sent: RenderSurfaceCommand<unknown>[] = [];
    const worker = {
      postMessage: vi.fn((command) => sent.push(command)),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;
    const host = createRenderSurfaceHost({
      canvas: {
        clientWidth: 800,
        clientHeight: 600,
        transferControlToOffscreen: () => ({}) as OffscreenCanvas,
      } as unknown as HTMLCanvasElement,
      surface: "figure",
      tiers: [{ scale: 1, cadenceMs: 25 }, { scale: 0.5, cadenceMs: 50 }],
      createWorker: () => worker,
      ownsWorker,
    });
    return { host, sent, worker };
  };

  it("forwards a new size from the observer rather than measuring the canvas", () => {
    const { sent } = stand();
    sent.length = 0;

    sizeObservers[0]!.callback([{ contentBoxSize: [{ inlineSize: 1024, blockSize: 768 }] }]);
    expect(sent.at(-1)).toMatchObject({ kind: "RESIZE", width: 1024, height: 768 });

    sizeObservers[0]!.callback([{ contentRect: { width: 512, height: 384 } }]);
    expect(sent.at(-1)).toMatchObject({ kind: "RESIZE", width: 512, height: 384 });

    // A zero-sized canvas is a canvas mid-layout, not a resize worth sending.
    sent.length = 0;
    sizeObservers[0]!.callback([{ contentRect: { width: 0, height: 0 } }]);
    expect(sent).toHaveLength(0);
  });

  it("tells the worker when nothing can see the surface, and when the tab hides", () => {
    const { sent } = stand();
    sent.length = 0;

    visibilityObservers[0]!.callback([{ isIntersecting: false }]);
    expect(sent.at(-1)).toMatchObject({ kind: "VISIBILITY", visible: false });

    visibilityObservers[0]!.callback([{ isIntersecting: true }]);
    expect(sent.at(-1)).toMatchObject({ kind: "VISIBILITY", visible: true });

    /*
     * A hidden tab is not a slower tab. The worker's clock is a timer and
     * timers keep running in the background, so a surface that never heard
     * about this would keep submitting GPU work for a tab nobody is looking at.
     */
    (globalThis as unknown as { document: { hidden: boolean } }).document.hidden = true;
    documentListeners.get("visibilitychange")?.();
    expect(sent.at(-1)).toMatchObject({ kind: "VISIBILITY", visible: false });
  });

  it("releases every observer on dispose, and spares a shared worker", () => {
    const shared = stand(false);
    shared.host.dispose();
    expect(sizeObservers[0]!.disconnect).toHaveBeenCalled();
    expect(visibilityObservers[0]!.disconnect).toHaveBeenCalled();
    expect(documentListeners.has("visibilitychange")).toBe(false);
    expect(shared.worker.terminate).not.toHaveBeenCalled();
    // Idempotent: a second dispose is a no-op, not a second STOP.
    const posts = (shared.worker.postMessage as ReturnType<typeof vi.fn>).mock.calls.length;
    shared.host.dispose();
    expect((shared.worker.postMessage as ReturnType<typeof vi.fn>).mock.calls.length).toBe(posts);

    const owned = stand(true);
    owned.host.dispose();
    expect(owned.worker.terminate).toHaveBeenCalled();
  });

  it("stops coalescing state once disposed", async () => {
    const { host, sent, worker } = stand();
    host.setState({ pointer: 1 });
    host.dispose();
    sent.length = 0;
    await vi.advanceTimersByTimeAsync(50);
    expect(sent.filter((command) => command.kind === "STATE")).toHaveLength(0);
    expect(worker.terminate).toHaveBeenCalled();
  });
});

describe("prewarming a surface", () => {
  const originalOffscreen = globalThis.OffscreenCanvas;
  const originalWorker = globalThis.Worker;
  const originalCanvas = globalThis.HTMLCanvasElement;

  const makeWorker = () => {
    const sent: RenderSurfaceCommand<unknown>[] = [];
    const worker = {
      postMessage: vi.fn((command) => sent.push(command)),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      terminate: vi.fn(),
    } as unknown as Worker;
    return { worker, sent };
  };

  beforeEach(() => {
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = class {};
    (globalThis as unknown as { Worker: unknown }).Worker = class {};
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = class {
      transferControlToOffscreen() {
        return {};
      }
    };
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 8 });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = originalOffscreen;
    (globalThis as unknown as { Worker: unknown }).Worker = originalWorker;
    (globalThis as unknown as { HTMLCanvasElement: unknown }).HTMLCanvasElement = originalCanvas;
  });

  it("spawns the worker and asks it to warm, with no canvas involved", () => {
    const { worker, sent } = makeWorker();
    const handle = prewarmRenderSurface({ surface: "figure", createWorker: () => worker })!;

    expect(handle.worker).toBe(worker);
    expect(sent).toEqual([{ kind: "WARM", surface: "figure" }]);
  });

  it("adopts the warmed worker instead of spawning a second one", () => {
    const { worker, sent } = makeWorker();
    const handle = prewarmRenderSurface({ surface: "figure", createWorker: () => worker })!;
    const spawnAgain = vi.fn(() => makeWorker().worker);

    const host = createRenderSurfaceHost({
      canvas: {
        clientWidth: 390,
        clientHeight: 844,
        transferControlToOffscreen: () => ({}) as OffscreenCanvas,
      } as unknown as HTMLCanvasElement,
      surface: "figure",
      tiers: [{ scale: 1, cadenceMs: 25 }],
      createWorker: spawnAgain,
      warmed: handle,
      cullOffscreen: false,
    });

    expect(spawnAgain).not.toHaveBeenCalled();
    expect(host.mode).toBe("worker");
    // WARM first, then INIT on the same worker.
    expect(sent.map((command) => command.kind)).toEqual(["WARM", "INIT"]);
    host.dispose();
  });

  /*
   * Two references to one worker, and only one of them may terminate it. A
   * caller that prewarms, mounts, and then tidies up its handle would otherwise
   * kill a surface that is drawing.
   */
  it("stops the handle from terminating a worker a host has adopted", () => {
    const { worker } = makeWorker();
    const handle = prewarmRenderSurface({ surface: "figure", createWorker: () => worker })!;

    createRenderSurfaceHost({
      canvas: {
        clientWidth: 100,
        clientHeight: 100,
        transferControlToOffscreen: () => ({}) as OffscreenCanvas,
      } as unknown as HTMLCanvasElement,
      surface: "figure",
      tiers: [{ scale: 1, cadenceMs: 25 }],
      createWorker: () => worker,
      warmed: handle,
      cullOffscreen: false,
    });

    handle.dispose();
    expect(worker.terminate).not.toHaveBeenCalled();
  });

  it("releases a warm nobody ever adopted", () => {
    const { worker } = makeWorker();
    const handle = prewarmRenderSurface({ surface: "figure", createWorker: () => worker })!;

    // The route the viewer never visited, the hover that went nowhere.
    handle.dispose();
    expect(worker.terminate).toHaveBeenCalledTimes(1);
    handle.dispose();
    expect(worker.terminate).toHaveBeenCalledTimes(1);
  });

  it("declines rather than throwing where a worker lane is unavailable", () => {
    (globalThis as unknown as { OffscreenCanvas: unknown }).OffscreenCanvas = undefined;
    expect(
      prewarmRenderSurface({ surface: "figure", createWorker: () => makeWorker().worker }),
    ).toBeNull();
  });
});
