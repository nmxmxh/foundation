import { afterEach, describe, expect, it, vi } from "vitest";
import { describeRuntimeCapabilityGaps, getRuntimeCapabilities } from "./runtimeCaps";

describe("runtime capabilities", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reports the fully eligible worker/shared-memory profile", () => {
    vi.stubGlobal("crossOriginIsolated", true);
    vi.stubGlobal("Worker", class {});
    const capabilities = getRuntimeCapabilities();
    expect(capabilities).toMatchObject({
      crossOriginIsolated: true,
      sharedArrayBuffer: true,
      worker: true,
      supportsWorkerPulse: true,
      supportsSharedMemoryRuntime: true,
      supportsSharedWasmMemory: true,
      issues: [],
    });
    expect(describeRuntimeCapabilityGaps(capabilities)).toEqual([]);
  });

  it("reports every controlled fallback when isolation primitives are absent", () => {
    vi.stubGlobal("crossOriginIsolated", false);
    vi.stubGlobal("SharedArrayBuffer", undefined);
    vi.stubGlobal("Worker", undefined);
    const capabilities = getRuntimeCapabilities();
    expect(capabilities.supportsWorkerPulse).toBe(false);
    expect(capabilities.supportsSharedWasmMemory).toBe(false);
    expect(capabilities.issues.map((issue) => issue.capability)).toEqual([
      "crossOriginIsolated", "sharedArrayBuffer", "worker", "webAssemblySharedMemory",
    ]);
    expect(describeRuntimeCapabilityGaps(capabilities)).toHaveLength(4);
  });
});
