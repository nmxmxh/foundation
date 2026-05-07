import { describe, expect, it } from "vitest";
import { planRuntimeGpuBatchLayout } from "./gpuLayout";

describe("runtime GPU batch layout", () => {
  it("packs batch regions on storage-buffer-friendly alignment", () => {
    const layout = planRuntimeGpuBatchLayout([{ byteLength: 12 }, { byteLength: 256 }, { byteLength: 257 }]);

    expect(layout.alignment).toBe(256);
    expect(layout.regions.map((region) => region.offset)).toEqual([0, 256, 512]);
    expect(layout.regions.map((region) => region.paddedByteLength)).toEqual([256, 256, 512]);
    expect(layout.totalBytes).toBe(1024);
  });

  it("computes workgroups independently from byte packing", () => {
    const layout = planRuntimeGpuBatchLayout(Array.from({ length: 130 }, () => ({ byteLength: 4 })), {
      workgroupSize: 64,
    });

    expect(layout.workgroupSize).toBe(64);
    expect(layout.workgroupCount).toBe(3);
  });

  it("rejects invalid alignment values", () => {
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: 1 }], { alignment: 192 })).toThrow(
      /positive power of two/
    );
  });
});
