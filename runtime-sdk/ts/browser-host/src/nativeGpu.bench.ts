import { bench, describe } from "vitest";

import { planRuntimeLane, type RuntimeLanePlannerCapabilities } from "./lanePlanner";
import { validateRuntimeNativeGpuDescriptor, type RuntimeNativeGpuDescriptor } from "./nativeGpu";

const descriptor: RuntimeNativeGpuDescriptor = {
  id: "camera.frame.42",
  kind: "native-gpu-texture",
  platform: "apple-iosurface",
  width: 1920,
  height: 1080,
  format: "bgra8",
  schemaName: "media/v1/frame.capnp",
  producer: "camera.plugin",
  fallback: "copy-to-webgpu",
};

const capabilities: RuntimeLanePlannerCapabilities = {
  supportsSharedMemoryRuntime: true,
  supportsSharedWasmMemory: true,
  worker: true,
  sharedArrayBuffer: true,
  crossOriginIsolated: true,
  nativeGpu: true,
  nativeGpuPlatforms: ["apple-iosurface"],
  webGpu: true,
  nativeFfi: true,
  nativeSharedMemory: true,
  cpuSimd: true,
  packetRing: true,
};

describe("runtime native GPU descriptor contract", () => {
  bench("validate native GPU descriptor", () => {
    validateRuntimeNativeGpuDescriptor(descriptor);
  });

  bench("plan native GPU lane", () => {
    planRuntimeLane({
      byteLength: 1920 * 1080 * 4,
      workload: "media",
      batchItems: 1,
      trust: "trusted",
      locality: "same-host",
      capabilities,
      unit: { supportsGpu: true },
      nativeGpuDescriptor: descriptor,
    });
  });
});
