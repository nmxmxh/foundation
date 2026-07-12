import { describe, expect, it, vi } from "vitest";
import { BrowserRuntimeHost } from "./host";
import { createRuntimeOrchestrator } from "./orchestrator";
import type { PulseManager } from "./pulse/pulseManager";
import type { RuntimeWorkerRequest, RuntimeWorkerResponse } from "./types";

class OrchestratorWorker {
  onmessage: ((event: MessageEvent<RuntimeWorkerResponse>) => void) | null = null;
  onerror: ((event: ErrorEvent) => void) | null = null;
  sent: RuntimeWorkerRequest[] = [];
  terminated = false;
  postMessage(message: RuntimeWorkerRequest): void { this.sent.push(message); }
  terminate(): void { this.terminated = true; }
  resolve(index = 0): void {
    const request = this.sent[index];
    this.onmessage?.(new MessageEvent("message", { data: { kind: "RUN_RESULT", requestId: request.requestId, unitId: request.unitId, output: { value: "done" }, runtimeSource: "wasm", epoch: 2 } }));
  }
}

const pulse = (): PulseManager => ({
  start: vi.fn(), watchEpochs: vi.fn(), unwatchEpoch: vi.fn(), setTPS: vi.fn(), stop: vi.fn(), shutdown: vi.fn(),
  getMode: () => "stopped", getDiagnostics: () => ({ mode: "stopped", waitAsync: false, crossOriginIsolated: false, degraded: false, targetTPS: 60, watcherCount: 0, visible: true, issues: [] }),
});

const descriptor = (requiresSharedMemory: boolean) => ({
  unitId: "echo", role: "compute" as const, inputSchema: "echo/input", outputSchema: "echo/output",
  supportsWasm: true, supportsNative: false, requiresSharedMemory, supportsGpu: false, maxConcurrency: 1,
});

describe("createRuntimeOrchestrator", () => {
  it("runs registered units and publishes diagnostics", async () => {
    const worker = new OrchestratorWorker();
    const pulseManager = pulse();
    const diagnostics = vi.fn();
    const orchestrator = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => worker as unknown as Worker, pulseManager, onDiagnostics: diagnostics });
    orchestrator.registerUnit(descriptor(true));
    const buffer = new SharedArrayBuffer(4096);
    const pending = orchestrator.runUnit({ unitId: "echo", input: { value: 1 }, buffer });
    expect(pulseManager.start).toHaveBeenCalledWith(buffer);
    worker.resolve();
    await expect(pending).resolves.toMatchObject({ output: { value: "done" } });
    expect(orchestrator.getDiagnostics()).toMatchObject({ activeUnits: 1, inFlight: 0, lastRuntimeSource: "wasm", lastEpoch: 2 });
    expect(diagnostics).toHaveBeenCalled();
  });

  it("rejects missing units, worker failures, and timeouts", async () => {
    vi.useFakeTimers();
    const worker = new OrchestratorWorker();
    const orchestrator = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => worker as unknown as Worker, pulseManager: pulse(), defaultTimeoutMs: 5 });
    await expect(orchestrator.runUnit({ unitId: "missing", input: null, buffer: new SharedArrayBuffer(4096) })).rejects.toThrow("not registered");
    orchestrator.registerUnit(descriptor(false));
    const timed = orchestrator.runUnit({ unitId: "echo", input: null, buffer: new SharedArrayBuffer(4096) });
    const rejected = expect(timed).rejects.toThrow("timed out");
    await vi.advanceTimersByTimeAsync(6);
    await rejected;
    worker.resolve();
    expect(worker.terminated).toBe(true);
    vi.useRealTimers();
  });

  it("shuts down workers, pending requests, and pulse ownership", async () => {
    const worker = new OrchestratorWorker();
    const pulseManager = pulse();
    const orchestrator = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => worker as unknown as Worker, pulseManager });
    orchestrator.registerUnit(descriptor(false));
    const pending = orchestrator.runUnit({ unitId: "echo", input: null, buffer: new SharedArrayBuffer(4096), timeoutMs: 1000 });
    const rejected = expect(pending).rejects.toThrow("shut down");
    orchestrator.shutdown();
    await rejected;
    expect(worker.terminated).toBe(true);
    expect(pulseManager.shutdown).toHaveBeenCalled();
    expect(orchestrator.getDiagnostics().mode).toBe("stopped");
  });

  it("handles unavailable factories and worker errors without restart", async () => {
    const unavailable = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => null, pulseManager: pulse() });
    unavailable.registerUnit(descriptor(false));
    await expect(unavailable.runUnit({ unitId: "echo", input: null, buffer: new SharedArrayBuffer(4096) })).rejects.toThrow("no worker factory");

    const worker = new OrchestratorWorker();
    const orchestrator = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => worker as unknown as Worker, pulseManager: pulse(), restartOnWorkerFailure: false });
    orchestrator.registerUnit(descriptor(false));
    const pending = orchestrator.runUnit({ unitId: "echo", input: null, buffer: new SharedArrayBuffer(4096), timeoutMs: 1000 });
    const rejected = expect(pending).rejects.toThrow("worker exploded");
    worker.onmessage?.(new MessageEvent("message", { data: { kind: "IGNORED" } }) as never);
    worker.onerror?.({ message: "worker exploded" } as ErrorEvent);
    await rejected;
    expect(worker.terminated).toBe(false);
    expect(orchestrator.getDiagnostics()).toMatchObject({ degraded: true, lastError: "worker exploded" });
    orchestrator.shutdown();
  });

  it("keeps workers alive when timeout restart is disabled", async () => {
    vi.useFakeTimers();
    const worker = new OrchestratorWorker();
    const orchestrator = createRuntimeOrchestrator({ host: new BrowserRuntimeHost(), createWorker: () => worker as unknown as Worker, pulseManager: pulse(), restartOnTimeout: false, defaultTimeoutMs: 2 });
    orchestrator.registerUnit(descriptor(false));
    const pending = orchestrator.runUnit({ unitId: "echo", input: null, buffer: new SharedArrayBuffer(4096) });
    const rejected = expect(pending).rejects.toThrow("timed out");
    await vi.advanceTimersByTimeAsync(3);
    await rejected;
    expect(worker.terminated).toBe(false);
    orchestrator.shutdown();
    vi.useRealTimers();
  });
});
