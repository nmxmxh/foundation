import { afterEach, describe, expect, it, vi } from "vitest";

/*
 * Every capability present: the only way this pulse can fall back is its
 * worker failing to load, which is the consumer-bundling case pulseManager.ts
 * describes. Kept in its own file because the clock's worker factory is module
 * state that the other clock tests must not inherit.
 */
vi.mock("./pulse/runtimeCaps", () => ({
  getRuntimeCapabilities: () => ({
    crossOriginIsolated: true,
    sharedArrayBuffer: true,
    webAssemblySharedMemory: true,
    worker: true,
    waitAsync: false,
    issues: [],
    supportsWorkerPulse: true,
    supportsSharedMemoryRuntime: true,
    supportsSharedWasmMemory: true,
  }),
  describeRuntimeCapabilityGaps: () => [],
}));

const { _resetFrameClockForTesting, configureFrameClock, onFrame } = await import("./frameClock");
const { PASS, clearRenderFacts, renderFacts } = await import("./renderMarks");

class FakeWorker {
  onmessage: ((event: unknown) => void) | null = null;
  onerror: (() => void) | null = null;
  postMessage(): void {}
  terminate(): void {}
}

describe("frame clock diagnostics", () => {
  afterEach(() => {
    _resetFrameClockForTesting();
    clearRenderFacts();
    vi.unstubAllGlobals();
  });

  it("does not report a pulse whose worker failed to load as healthy", () => {
    // The main-thread fallback schedules with window.setTimeout. `document`
    // stays undefined, so the pulse skips its visibility handlers.
    vi.stubGlobal("window", globalThis);
    let created: FakeWorker | null = null;
    configureFrameClock({
      createWorker: () => {
        created = new FakeWorker();
        return created as unknown as Worker;
      },
    });

    const subscription = onFrame(() => undefined, 16);
    expect(created).not.toBeNull();

    // The browser reports the worker script failing to load.
    (created as unknown as FakeWorker).onerror?.();

    const fact = renderFacts()[PASS.clock];
    expect(fact?.fallback).toBe(true);
    expect(fact?.lane).toBe("main-thread");
    expect(fact?.reason).not.toBe("ok");
    expect(fact?.reason).toContain("worker unavailable");

    subscription.release();
  });
});
