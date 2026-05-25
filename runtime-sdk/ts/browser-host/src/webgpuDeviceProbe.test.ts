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
});
