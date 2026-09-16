import { afterEach, describe, expect, it, vi } from "vitest";

/*
 * A worker that boots slowly must neither lose the clock its lane nor stall
 * frames. Every capability is present (mocked), so the only thing that varies
 * is when the worker's first tick arrives. Kept in its own file: the clock's
 * worker factory and tick count are module state.
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

const { _resetFrameClockForTesting, configureFrameClock, frameClockMode, onFrame } = await import("./frameClock");
const { PASS, clearRenderFacts, renderFacts } = await import("./renderMarks");
const { IDX_RUNTIME_TICK } = await import("./generated/runtimeBuffer");

class FakeWorker {
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  postMessage(): void {}
  terminate(): void {}
}

const install = () => {
  const box: { worker: FakeWorker | null } = { worker: null };
  configureFrameClock({
    createWorker: () => {
      box.worker = new FakeWorker();
      return box.worker as unknown as Worker;
    },
  });
  return box;
};

const tick = (worker: FakeWorker | null, value: number) =>
  worker?.onmessage?.({ data: { type: "EPOCH_CHANGE", payload: { index: IDX_RUNTIME_TICK, value } } });

describe("frame clock frames bridge", () => {
  afterEach(() => {
    _resetFrameClockForTesting();
    clearRenderFacts();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("bridges on frames when the worker is slow to tick, then hands the clock back", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("window", globalThis);
    const box = install();
    let ticks = 0;
    const subscription = onFrame(() => {
      ticks += 1;
    }, 16);

    await vi.advanceTimersByTimeAsync(150);
    // One assertion over the whole state, so a failure shows mode, fact and ticks together.
    const bridged = { mode: frameClockMode().mode, fact: renderFacts()[PASS.clock], ticks };
    expect(bridged).toMatchObject({
      mode: "frames",
      fact: { lane: "frames", fallback: true, reason: expect.stringContaining("bridging") },
    });
    expect(ticks, "frames kept flowing while the worker booted").toBeGreaterThan(0);

    tick(box.worker, 1);
    expect(frameClockMode().mode).toBe("worker");
    expect(renderFacts()[PASS.clock]?.lane).toBe("worker");
    expect(renderFacts()[PASS.clock]?.fallback).toBe(false);

    // The bridge loop has stopped: with no further worker ticks, frames stop.
    const afterHandBack = ticks;
    await vi.advanceTimersByTimeAsync(200);
    expect(ticks - afterHandBack, "the frames bridge kept running after hand-back").toBeLessThanOrEqual(1);
    subscription.release();
  });

  it("never bridges when the worker ticks inside the grace window", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("window", globalThis);
    const box = install();
    const subscription = onFrame(() => undefined, 16);
    tick(box.worker, 1);
    await vi.advanceTimersByTimeAsync(150);
    expect(frameClockMode().mode).toBe("worker");
    expect(renderFacts()[PASS.clock]?.lane).toBe("worker");
    expect(renderFacts()[PASS.clock]?.reason).not.toContain("bridging");
    subscription.release();
  });
});
