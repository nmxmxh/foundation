import { describe, expect, it } from "vitest";
import {
  formatRuntimeWebGpuPhysicalProbe,
  runRuntimeWebGpuPhysicalProbe,
} from "./webgpuDeviceProbe";

describe("runtime WebGPU physical probe", () => {
  it("reports unavailable instead of throwing when the runtime has no WebGPU adapter", async () => {
    const report = await runRuntimeWebGpuPhysicalProbe({ payloadBytes: 4 * 1024 });

    expect(report.measuredAtUnixNs).toBeGreaterThan(0);
    expect(report.result.available).toBe(false);
    if (!report.result.available) {
      expect(report.result.reason).toMatch(/WebGPU/);
    }
    expect(formatRuntimeWebGpuPhysicalProbe(report)).toContain("measuredAtUnixNs");
  });

  it("formats available physical timing, materialization, and resource evidence", () => {
    const text = formatRuntimeWebGpuPhysicalProbe({
      measuredAtUnixNs: 1,
      runtime: "browser",
      userAgent: "test-agent",
      result: {
        available: true,
        adapterNs: 1, deviceNs: 2, prewarmNs: 3, dispatchNs: 4, queueDrainNs: 5,
        materializeNs: 6, totalNs: 21, uploadMode: "packed", materialized: true,
        dispatchTimingsNs: { packNs: 1, uploadNs: 2, pipelineNs: 3, dispatchNs: 4, readbackNs: 5, dispatchReadbackNs: 9, writebackNs: 6, totalNs: 21 },
        materializeTimingsNs: { readbackNs: 2, writebackNs: 3, totalNs: 5 },
        resource: { id: "gpu_1", kind: "buffer", byteLength: 64, descriptorIds: [1, 2] },
      },
    } as never);
    expect(text).toContain("runtime=browser");
    expect(text).toContain("materialize.totalNs=5");
    expect(text).toContain("resource.descriptorCount=2");
  });

  it("omits optional physical evidence when it was not collected", () => {
    const text = formatRuntimeWebGpuPhysicalProbe({
      measuredAtUnixNs: 1,
      runtime: "unknown",
      result: {
        available: true,
        adapterNs: 1, deviceNs: 1, prewarmNs: 1, dispatchNs: 1, queueDrainNs: 1,
        materializeNs: 0, totalNs: 5, uploadMode: "direct", materialized: false,
        dispatchTimingsNs: { packNs: 0, uploadNs: 1, pipelineNs: 1, dispatchNs: 1, readbackNs: 1, dispatchReadbackNs: 2, writebackNs: 0, totalNs: 4 },
      },
    } as never);
    expect(text).not.toContain("userAgent=");
    expect(text).not.toContain("materialize.readbackNs=");
    expect(text).not.toContain("resource.id=");
  });
});
