import { describe, expect, it } from "vitest";
import { RuntimeSharedArena } from "./arena";
import { planRuntimeGpuBatchLayout } from "./gpuLayout";
import { packArenaDescriptors, writeGpuOutputToArena } from "./webgpuHost";

describe("runtime WebGPU host helpers", () => {
  it("packs arena descriptors into GPU-aligned regions", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const first = arena.allocate(3);
    const second = arena.allocate(5);
    arena.writeSlabReady(first.id, new Uint8Array([1, 2, 3]));
    arena.writeSlabReady(second.id, new Uint8Array([4, 5, 6, 7, 8]));

    const descriptors = [arena.readDescriptor(first.id), arena.readDescriptor(second.id)];
    const layout = planRuntimeGpuBatchLayout(descriptors);
    const packed = packArenaDescriptors(arena, descriptors, layout);

    expect([...packed.subarray(0, 3)]).toEqual([1, 2, 3]);
    expect([...packed.subarray(256, 261)]).toEqual([4, 5, 6, 7, 8]);
  });

  it("writes GPU output slices back to their arena descriptors", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const first = arena.allocate(3);
    const second = arena.allocate(5);
    arena.writeSlabReady(first.id, new Uint8Array(3));
    arena.writeSlabReady(second.id, new Uint8Array(5));
    const descriptors = [arena.readDescriptor(first.id), arena.readDescriptor(second.id)];
    const layout = planRuntimeGpuBatchLayout(descriptors);
    const output = new Uint8Array(layout.totalBytes);
    output.set([9, 8, 7], 0);
    output.set([6, 5, 4, 3, 2], 256);

    writeGpuOutputToArena(arena, descriptors, layout, output);

    expect([...arena.readSlabView(first.id)]).toEqual([9, 8, 7]);
    expect([...arena.readSlabView(second.id)]).toEqual([6, 5, 4, 3, 2]);
  });
});
