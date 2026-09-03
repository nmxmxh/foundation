import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRenderSurfaceHost, probeRenderSurface, type RenderSurfaceCommand, type RenderSurfaceEvent } from "./renderSurface";

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
