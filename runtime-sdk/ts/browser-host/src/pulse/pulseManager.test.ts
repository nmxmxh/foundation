import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./runtimeCaps", () => ({
  getRuntimeCapabilities: () => ({
    waitAsync: true, crossOriginIsolated: true, sharedArrayBuffer: true, webAssemblySharedMemory: true,
    worker: true, supportsWorkerPulse: true, supportsSharedMemoryRuntime: true, supportsSharedWasmMemory: true, issues: [],
  }),
}));

import { IDX_RUNTIME_TICK } from "../generated/runtimeBuffer";
import { createPulseManager } from "./pulseManager";

class PulseWorker {
  onmessage: ((event: MessageEvent<{ type: string; payload: { index: number; value: number } }>) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: unknown[] = [];
  terminated = false;
  postMessage(message: unknown): void { this.sent.push(message); }
  terminate(): void { this.terminated = true; }
}

describe("createPulseManager", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("window", { setTimeout, clearTimeout, addEventListener: vi.fn(), removeEventListener: vi.fn() });
    vi.stubGlobal("document", { visibilityState: "visible", addEventListener: vi.fn(), removeEventListener: vi.fn() });
  });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

  it("owns worker pulse messages and epoch watchers", () => {
    const worker = new PulseWorker();
    const diagnostics = vi.fn();
    const manager = createPulseManager({ createWorker: () => worker as unknown as Worker, onDiagnostics: diagnostics });
    const handler = vi.fn();
    manager.watchEpochs([IDX_RUNTIME_TICK], handler);
    manager.watchEpochs([IDX_RUNTIME_TICK], handler);
    const buffer = new SharedArrayBuffer(4096);
    manager.start(buffer);
    manager.start(buffer);
	 expect(document.addEventListener).toHaveBeenCalledTimes(1);
	 expect(window.addEventListener).toHaveBeenCalledTimes(2);
    expect(manager.getMode()).toBe("worker");
    expect(worker.sent).toEqual(expect.arrayContaining([
      expect.objectContaining({ type: "INIT" }), expect.objectContaining({ type: "SET_VISIBILITY" }),
    ]));
    worker.onmessage?.(new MessageEvent("message", { data: { type: "EPOCH_CHANGE", payload: { index: IDX_RUNTIME_TICK, value: 4 } } }));
    worker.onmessage?.(new MessageEvent("message", { data: { type: "IGNORED", payload: { index: IDX_RUNTIME_TICK, value: 5 } } }));
    expect(handler).toHaveBeenCalledWith(4, IDX_RUNTIME_TICK);
    manager.setTPS(0);
    expect(manager.getDiagnostics()).toMatchObject({ targetTPS: 1, watcherCount: 1 });
    manager.unwatchEpoch(IDX_RUNTIME_TICK, handler);
    manager.unwatchEpoch(99, handler);
    manager.stop();
	 expect(document.removeEventListener).toHaveBeenCalledTimes(1);
	 expect(window.removeEventListener).toHaveBeenCalledTimes(2);
    manager.shutdown();
    expect(worker.terminated).toBe(true);
    expect(diagnostics).toHaveBeenCalled();
  });

  it("falls back to the bounded main-thread loop after worker failure", async () => {
    const worker = new PulseWorker();
    const manager = createPulseManager({ createWorker: () => worker as unknown as Worker, defaultTPS: 20 });
    const buffer = new SharedArrayBuffer(4096);
    manager.start(buffer);
    worker.onerror?.();
    expect(manager.getDiagnostics()).toMatchObject({ mode: "main-thread", degraded: true });
    await vi.advanceTimersByTimeAsync(60);
    expect(new Int32Array(buffer)[IDX_RUNTIME_TICK]).toBeGreaterThan(0);
    manager.stop();
    expect(manager.getMode()).toBe("stopped");
  });

  it("uses main-thread fallback when worker construction fails", () => {
    const manager = createPulseManager({ createWorker: () => { throw new Error("worker unavailable"); } });
    manager.start(new SharedArrayBuffer(4096));
    expect(manager.getDiagnostics()).toMatchObject({ mode: "main-thread", degraded: true });
    manager.shutdown();
  });

  it("runs without DOM visibility hooks", () => {
    vi.unstubAllGlobals();
    const worker = new PulseWorker();
    const manager = createPulseManager({ createWorker: () => worker as unknown as Worker });
    manager.start(new SharedArrayBuffer(4096));
    expect(manager.getMode()).toBe("worker");
    manager.shutdown();
  });
});
