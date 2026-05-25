import { describe, expect, it } from "vitest";
import {
  createRuntimeGpuBatchLayoutScratch,
  planRuntimeGpuBatchLayout,
  planRuntimeGpuBatchLayoutInto,
} from "./gpuLayout";

describe("runtime GPU batch layout", () => {
  it("packs batch regions on storage-buffer-friendly alignment", () => {
    const layout = planRuntimeGpuBatchLayout([{ byteLength: 12 }, { byteLength: 256 }, { byteLength: 257 }]);

    expect(layout.alignment).toBe(256);
    expect(layout.itemCount).toBe(3);
    expect(layout.payloadBytes).toBe(525);
    expect(layout.paddingBytes).toBe(499);
    expect(layout.maxRegionBytes).toBe(257);
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
    expect(layout.dispatchItems).toBe(130);
    expect(layout.dispatchWaste).toBe(62);
  });

  it("can size dispatch by logical packed u32 elements instead of descriptor count", () => {
    const layout = planRuntimeGpuBatchLayout([{ byteLength: 4096 }], {
      dispatchItemsFrom: "u32",
      workgroupSize: 64,
    });

    expect(layout.regions.length).toBe(1);
    expect(layout.dispatchItems).toBe(1024);
    expect(layout.workgroupCount).toBe(16);
  });

  it("can reuse caller-owned layout storage", () => {
    const scratch = createRuntimeGpuBatchLayoutScratch();
    const first = planRuntimeGpuBatchLayoutInto([{ byteLength: 4 }, { byteLength: 8 }], scratch);
    const firstRegion = first.regions[1];
    const second = planRuntimeGpuBatchLayoutInto([{ byteLength: 12 }, { byteLength: 16 }], scratch);

    expect(second).toBe(scratch);
    expect(second.regions[1]).toBe(firstRegion);
    expect(second.payloadBytes).toBe(28);
  });

  it("rejects invalid alignment values", () => {
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: 1 }], { alignment: 192 })).toThrow(
      /positive power of two/
    );
  });

  it("rejects invalid strict dispatch layouts", () => {
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: Number.NaN }], { strict: true })).toThrow(/finite/);
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: -1 }], { strict: true })).toThrow(/non-negative/);
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: 0 }], { strict: true })).toThrow(/non-zero/);
    expect(() => planRuntimeGpuBatchLayout([{ byteLength: 512 }], { maxTotalBytes: 256 })).toThrow(
      /exceeds max total bytes/
    );
    expect(() =>
      planRuntimeGpuBatchLayout([{ byteLength: 3 }], { alignment: 1, dispatchItemsFrom: "u32", strict: true })
    ).toThrow(/4-byte aligned/);
  });
});
