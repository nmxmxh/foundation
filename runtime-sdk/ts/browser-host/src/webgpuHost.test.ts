import { describe, expect, it } from "vitest";
import { RuntimeSharedArena } from "./arena";
import { planRuntimeGpuBatchLayout } from "./gpuLayout";
import {
  packArenaDescriptors,
  packArenaDescriptorsInto,
  PASSTHROUGH_U32_SHADER,
  RuntimeWebGpuHost,
  measureRuntimeWebGpuDeviceRoundTrip,
  writeGpuOutputToArena,
} from "./webgpuHost";

type FakeGpuBuffer = {
  data: Uint8Array;
  getMappedRange(): ArrayBuffer;
  mapAsync(): Promise<void>;
  unmap(): void;
  destroy(): void;
};

const fakeGpuBuffer = (size: number): FakeGpuBuffer => ({
  data: new Uint8Array(size),
  getMappedRange() {
    return this.data.buffer as ArrayBuffer;
  },
  async mapAsync() {},
  unmap() {},
  destroy() {},
});

const createFakeGpuDevice = () => {
  const stats = {
    buffersCreated: 0,
    buffersDestroyed: 0,
    queueDrains: 0,
    writes: [] as Array<{ dataOffset: number; size: number; sourceBytes: number }>,
  };
  return {
    stats,
    limits: {
      maxBufferSize: 16 * 1024 * 1024,
      maxStorageBufferBindingSize: 16 * 1024 * 1024,
      maxComputeWorkgroupsPerDimension: 65535,
    },
    queue: {
      writeBuffer(buffer: FakeGpuBuffer, offset: number, data: Uint8Array, dataOffset = 0, size?: number) {
        const byteLength = size ?? data.byteLength - dataOffset;
        stats.writes.push({ dataOffset, size: byteLength, sourceBytes: data.byteLength });
        buffer.data.set(data.subarray(dataOffset, dataOffset + byteLength), offset);
      },
      submit() {},
      async onSubmittedWorkDone() {
        stats.queueDrains += 1;
      },
    },
    createBuffer(descriptor: { size: number }) {
      stats.buffersCreated += 1;
      const buffer = fakeGpuBuffer(descriptor.size);
      const destroy = buffer.destroy;
      buffer.destroy = () => {
        stats.buffersDestroyed += 1;
        destroy.call(buffer);
      };
      return buffer;
    },
    createBindGroup(descriptor: unknown) {
      return descriptor;
    },
    createCommandEncoder() {
      return {
        beginComputePass() {
          let bindGroup: any;
          return {
            setPipeline() {},
            setBindGroup(_index: number, nextBindGroup: any) {
              bindGroup = nextBindGroup;
            },
            dispatchWorkgroups() {
              const input = bindGroup.entries[0].resource.buffer as FakeGpuBuffer;
              const output = bindGroup.entries[1].resource.buffer as FakeGpuBuffer;
              output.data.set(input.data.subarray(0, output.data.byteLength));
            },
            end() {},
          };
        },
        copyBufferToBuffer(
          source: FakeGpuBuffer,
          sourceOffset: number,
          destination: FakeGpuBuffer,
          destinationOffset: number,
          size: number
        ) {
          destination.data.set(source.data.subarray(sourceOffset, sourceOffset + size), destinationOffset);
        },
        finish() {
          return {};
        },
      };
    },
    createShaderModule() {
      return {};
    },
    async createComputePipelineAsync() {
      return {
        getBindGroupLayout() {
          return {};
        },
      };
    },
  };
};

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, resolve, reject };
};

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

  it("packs arena descriptors into caller-owned storage", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const first = arena.allocate(3);
    arena.writeSlabReady(first.id, new Uint8Array([1, 2, 3]));

    const descriptors = [arena.readDescriptor(first.id)];
    const layout = planRuntimeGpuBatchLayout(descriptors);
    const target = new Uint8Array(layout.totalBytes);

    packArenaDescriptorsInto(arena, descriptors, layout, target);

    expect([...target.subarray(0, 3)]).toEqual([1, 2, 3]);
    expect(() => packArenaDescriptorsInto(arena, descriptors, layout, new Uint8Array(1))).toThrow(
      /target too short/
    );
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

  it("rejects descriptor layouts that do not match the batch", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const first = arena.allocate(3);
    const second = arena.allocate(5);
    arena.writeSlabReady(first.id, new Uint8Array([1, 2, 3]));
    arena.writeSlabReady(second.id, new Uint8Array([4, 5, 6, 7, 8]));

    const descriptors = [arena.readDescriptor(first.id), arena.readDescriptor(second.id)];
    const layout = planRuntimeGpuBatchLayout([descriptors[0]]);

    expect(() => packArenaDescriptors(arena, descriptors, layout)).toThrow(/layout\/descriptors mismatch/);
  });

  it("rejects GPU output that cannot cover the planned regions", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const first = arena.allocate(3);
    arena.writeSlabReady(first.id, new Uint8Array(3));
    const descriptors = [arena.readDescriptor(first.id)];
    const layout = planRuntimeGpuBatchLayout(descriptors);

    expect(() => writeGpuOutputToArena(arena, descriptors, layout, new Uint8Array(1))).toThrow(
      /output too short/
    );
  });

  it("keeps arena dispatch GPU-resident by default", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const readyDescriptor = arena.readDescriptor(descriptor.id);
    const device = createFakeGpuDevice();
    const host = await RuntimeWebGpuHost.create({ device: device as any });

    const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });

    expect(device.stats.writes[0]).toMatchObject({
      dataOffset: readyDescriptor.offset,
      size: readyDescriptor.length,
      sourceBytes: arena.capacity(),
    });
    expect(dispatch.materialized).toBe(false);
    expect(dispatch.policy).toBe("gpu-resident");
    expect(dispatch.uploadMode).toBe("direct-arena");
    expect(dispatch.descriptors).toEqual([]);
    expect(dispatch.resources.output?.kind).toBe("gpu-buffer");
    expect(host.getResource(dispatch.resources.output?.id ?? -1)?.fallback).toBe("materialize-readback");
  });

  it("packs uploads when direct arena writeBuffer would violate byte alignment", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(3);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3]));
    const device = createFakeGpuDevice();
    const host = await RuntimeWebGpuHost.create({ device: device as any });

    const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });

    expect(dispatch.uploadMode).toBe("packed");
    expect(device.stats.writes[0]).toMatchObject({
      dataOffset: 0,
      size: 256,
      sourceBytes: 256,
    });
  });

  it("materializes a resident GPU resource only when requested", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const host = await RuntimeWebGpuHost.create({ device: createFakeGpuDevice() as any });
    const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    arena.writeSlabReady(descriptor.id, new Uint8Array([9, 9, 9, 9]));

    const materialized = await host.materializeResourceToArena(arena, dispatch.resources.output?.id ?? -1);

    expect(materialized.descriptors).toHaveLength(1);
    expect([...arena.readSlabView(descriptor.id)]).toEqual([1, 2, 3, 4]);
  });

  it("chains resident GPU resources without arena re-upload", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const host = await RuntimeWebGpuHost.create({ device: createFakeGpuDevice() as any });
    const first = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    arena.writeSlabReady(descriptor.id, new Uint8Array([9, 9, 9, 9]));

    const second = await host.dispatchResidentBatch(first.resources.output?.id ?? -1, {
      shader: PASSTHROUGH_U32_SHADER,
    });
    await host.materializeResourceToArena(arena, second.resources.output?.id ?? -1);

    expect(second.uploadMode).toBe("none");
    expect([...arena.readSlabView(descriptor.id)]).toEqual([1, 2, 3, 4]);
  });

  it("reuses transient and released GPU buffers within a bounded pool", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const device = createFakeGpuDevice();
    const host = await RuntimeWebGpuHost.create({ device: device as any });
    await host.prewarmKernel({ shader: PASSTHROUGH_U32_SHADER });

    const first = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    host.destroyResource(first.resources.output?.id ?? -1);
    const createdAfterFirstDispatch = device.stats.buffersCreated;

    const second = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    host.destroyResource(second.resources.output?.id ?? -1);

    expect(createdAfterFirstDispatch).toBe(2);
    expect(device.stats.buffersCreated).toBe(createdAfterFirstDispatch);
    expect(device.stats.buffersDestroyed).toBe(0);
  });

  it("keeps materialize-readback as an explicit compatibility policy", async () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const host = await RuntimeWebGpuHost.create({ device: createFakeGpuDevice() as any });

    const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
      dataPolicy: "materialize-readback",
    });

    expect(dispatch.materialized).toBe(true);
    expect(dispatch.uploadMode).toBe("packed");
    expect(dispatch.descriptors).toHaveLength(1);
    expect([...arena.readSlabView(descriptor.id)]).toEqual([1, 2, 3, 4]);
  });

  it("drops resident resources and rejects dispatch after device loss", async () => {
    const loss = deferred<{ reason: string; message: string }>();
    const device = createFakeGpuDevice() as any;
    device.lost = loss.promise;
    const arena = RuntimeSharedArena.create({ arenaBytes: 1024 * 1024 });
    const descriptor = arena.allocate(4);
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const host = await RuntimeWebGpuHost.create({ device });
    const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    const resourceId = dispatch.resources.output?.id ?? -1;

    loss.resolve({ reason: "destroyed", message: "test device loss" });
    await Promise.resolve();

    expect(host.getResource(resourceId)).toBeUndefined();
    expect(host.getDeviceLossState()).toMatchObject({
      lost: true,
      reason: "destroyed",
      message: "test device loss",
      resourcesDropped: 1,
    });
    await expect(host.dispatchArenaBatch(arena, [descriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    })).rejects.toThrow(/device is lost/);
  });

  it("measures optional real-device timing fields with submitted-work drain", async () => {
    const device = createFakeGpuDevice();

    const result = await measureRuntimeWebGpuDeviceRoundTrip({
      device: device as any,
      payloadBytes: 4 * 1024,
      waitForSubmittedWork: true,
    });

    expect(result.available).toBe(true);
    if (!result.available) {
      return;
    }
    expect(result.adapterNs).toBe(0);
    expect(result.deviceNs).toBe(0);
    expect(result.dispatchNs).toBeGreaterThanOrEqual(0);
    expect(result.dispatchTimingsNs.uploadNs).toBeGreaterThanOrEqual(0);
    expect(result.materializeNs).toBeGreaterThanOrEqual(0);
    expect(result.materialized).toBe(true);
    expect(result.uploadMode).toBe("direct-arena");
    expect(device.stats.queueDrains).toBe(1);
  });
});
