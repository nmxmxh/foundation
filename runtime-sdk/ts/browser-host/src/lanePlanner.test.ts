import { describe, expect, it } from "vitest";
import { planRuntimeLane, type RuntimeLanePlannerCapabilities } from "./lanePlanner";

const baseCaps: RuntimeLanePlannerCapabilities = {
  supportsSharedMemoryRuntime: true,
  supportsSharedWasmMemory: true,
  worker: true,
  sharedArrayBuffer: true,
  crossOriginIsolated: true,
  webGpu: true,
  nativeGpu: true,
  nativeGpuPlatforms: ["apple-iosurface"],
  nativeFfi: true,
  nativeSharedMemory: true,
  cpuSimd: true,
  webSocketTransport: true,
};

describe("runtime lane planner", () => {
  it("keeps trusted same-process control work on the direct lane", () => {
    const plan = planRuntimeLane({
      byteLength: 512,
      workload: "control",
      trust: "trusted",
      locality: "same-process",
      capabilities: baseCaps,
    });
    expect(plan.lane).toBe("go-direct");
    expect(plan.copyBudget).toBe("none");
    expect(plan.allocationBudget).toBe("zero-heap");
    expect(plan.expectedLatencyClass).toBe("nanoseconds");
    expect(plan.requiresCrossOriginIsolation).toBe(false);
  });

  it("routes wide browser vector batches to WebGPU when dispatch can be amortized", () => {
    const plan = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "vector",
      batchItems: 4096,
      trust: "sandboxed",
      locality: "browser",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
    });
    expect(plan.lane).toBe("webgpu");
    expect(plan.batchSize).toBeGreaterThanOrEqual(4096);
    expect(plan.expectedLatencyClass).toBe("milliseconds");
    expect(plan.requiresCrossOriginIsolation).toBe(true);
  });

  it("routes trusted same-host resident GPU descriptors to the native GPU lane", () => {
    const plan = planRuntimeLane({
      byteLength: 1920 * 1080,
      workload: "media",
      batchItems: 1,
      trust: "trusted",
      locality: "same-host",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
      nativeGpuDescriptor: {
        id: "camera.frame.42",
        kind: "native-gpu-texture",
        platform: "apple-iosurface",
        width: 1920,
        height: 1080,
        format: "bgra8",
        producer: "camera.plugin",
        fallback: "copy-to-webgpu",
      },
    });

    expect(plan.lane).toBe("native-gpu");
    expect(plan.copyBudget).toBe("none");
    expect(plan.expectedLatencyClass).toBe("microseconds");
    expect(plan.requiresCrossOriginIsolation).toBe(false);
    expect(plan.fallbacks).toContain("webgpu");
  });

  it("does not select native GPU for unsupported descriptor platforms", () => {
    const plan = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "media",
      batchItems: 1,
      trust: "trusted",
      locality: "same-host",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
      nativeGpuDescriptor: {
        id: "camera.frame.43",
        kind: "native-gpu-texture",
        platform: "linux-dmabuf",
        width: 1280,
        height: 720,
        format: "nv12",
        producer: "camera.plugin",
        fallback: "copy-to-webgpu",
      },
    });

    expect(plan.lane).not.toBe("native-gpu");
    expect(plan.fallbacks).toContain("native-gpu");
  });

  it("does not select native GPU when descriptor validation rejects raw handle leaks", () => {
    const plan = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "media",
      batchItems: 1,
      trust: "trusted",
      locality: "same-host",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
      nativeGpuDescriptor: {
        id: "camera.frame.44",
        kind: "native-gpu-texture",
        platform: "apple-iosurface",
        width: 1280,
        height: 720,
        format: "bgra8",
        producer: "camera.plugin",
        fallback: "copy-to-webgpu",
        IOSurface: 9,
      } as any,
    });

    expect(plan.lane).not.toBe("native-gpu");
  });

  it("does not list native GPU as fallback when the runtime lacks capability", () => {
    const plan = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "media",
      batchItems: 1,
      trust: "trusted",
      locality: "same-host",
      capabilities: {
        ...baseCaps,
        nativeGpu: false,
        nativeGpuPlatforms: [],
      },
      unit: { supportsGpu: true },
    });

    expect(plan.fallbacks).not.toContain("native-gpu");
  });

  it("does not select native GPU for sandboxed or GPU-disabled work", () => {
    const sandboxed = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "media",
      trust: "sandboxed",
      locality: "same-host",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
    });
    const disabled = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "media",
      trust: "trusted",
      locality: "same-host",
      capabilities: baseCaps,
      unit: { supportsGpu: false },
    });

    expect(sandboxed.lane).not.toBe("native-gpu");
    expect(disabled.lane).not.toBe("native-gpu");
  });

  it("keeps sub-millisecond browser deadlines away from WebGPU dispatch setup", () => {
    const plan = planRuntimeLane({
      byteLength: 512 * 1024,
      workload: "vector",
      batchItems: 4096,
      deadlineMs: 0.5,
      trust: "sandboxed",
      locality: "browser",
      capabilities: baseCaps,
      unit: { supportsGpu: true },
    });
    expect(plan.lane).toBe("wasm-sab");
    expect(plan.deadlineRisk).toBe("low");
  });

  it("prefers Rust FFI for trusted SIMD-sized same-host vector work", () => {
    const plan = planRuntimeLane({
      byteLength: 64 * 1024,
      workload: "vector",
      batchItems: 128,
      trust: "trusted",
      locality: "same-host",
      capabilities: baseCaps,
    });
    expect(plan.lane).toBe("rust-ffi");
    expect(plan.copyBudget).toBe("none");
    expect(plan.allocationBudget).toBe("zero-heap");
  });

  it("routes packet-like same-host batches to fixed descriptor rings when available", () => {
    const plan = planRuntimeLane({
      byteLength: 32 * 1024,
      workload: "stream",
      batchItems: 64,
      trust: "trusted",
      locality: "same-host",
      capabilities: {
        ...baseCaps,
        packetRing: true,
      },
    });
    expect(plan.lane).toBe("packet-ring");
    expect(plan.batchSize).toBe(64);
    expect(plan.copyBudget).toBe("none");
    expect(plan.allocationBudget).toBe("zero-heap");
  });

  it("falls back to transfer workers when SAB is unavailable in browser", () => {
    const plan = planRuntimeLane({
      byteLength: 64 * 1024,
      workload: "batch",
      batchItems: 16,
      trust: "sandboxed",
      locality: "browser",
      capabilities: {
        ...baseCaps,
        supportsSharedMemoryRuntime: false,
        supportsSharedWasmMemory: false,
        sharedArrayBuffer: false,
        crossOriginIsolated: false,
        webGpu: false,
      },
    });
    expect(plan.lane).toBe("wasm-transfer");
    expect(plan.fallbacks).toContain("http");
  });

  it("streams payloads that exceed bounded arena movement", () => {
    const plan = planRuntimeLane({
      byteLength: 2 * 1024 * 1024,
      workload: "stream",
      batchItems: 4,
      trust: "remote",
      locality: "cross-host",
      capabilities: baseCaps,
    });
    expect(plan.lane).toBe("stream");
    expect(plan.copyBudget).toBe("streaming");
  });
});
