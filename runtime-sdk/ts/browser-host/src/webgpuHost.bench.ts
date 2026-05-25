import { bench, describe } from "vitest";
import { RuntimeSharedArena, type RuntimeArenaDescriptor } from "./arena";
import { ARENA_HEAVY_BYTES } from "./generated/runtimeBuffer";
import {
  createRuntimeGpuBatchLayoutScratch,
  planRuntimeGpuBatchLayout,
  planRuntimeGpuBatchLayoutInto,
} from "./gpuLayout";
import {
  packArenaDescriptors,
  packArenaDescriptorsInto,
  PASSTHROUGH_U32_SHADER,
  RuntimeWebGpuHost,
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

const createFakeGpuDevice = () => ({
  limits: {
    maxBufferSize: 16 * 1024 * 1024,
    maxStorageBufferBindingSize: 16 * 1024 * 1024,
    maxComputeWorkgroupsPerDimension: 65535,
  },
  queue: {
    writeBuffer(buffer: FakeGpuBuffer, offset: number, data: Uint8Array, dataOffset = 0, size?: number) {
      const byteLength = size ?? data.byteLength - dataOffset;
      buffer.data.set(data.subarray(dataOffset, dataOffset + byteLength), offset);
    },
    submit() {},
  },
  createBuffer(descriptor: { size: number }) {
    return fakeGpuBuffer(descriptor.size);
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
});

const payload = (bytes: number): Uint8Array => {
  const out = new Uint8Array(bytes);
  for (let index = 0; index < out.byteLength; index += 1) {
    out[index] = index % 251;
  }
  return out;
};

const allocateReadyDescriptors = (
  arena: RuntimeSharedArena,
  batchSize: number,
  bytesPerDescriptor: number
): RuntimeArenaDescriptor[] => {
  const data = payload(bytesPerDescriptor);
  return Array.from({ length: batchSize }, () => {
    const descriptor = arena.allocate(data.byteLength);
    arena.writeSlabReady(descriptor.id, data);
    return arena.readDescriptor(descriptor.id);
  });
};

describe("RuntimeWebGpuHost CPU-side helpers", () => {
  for (const size of [4 * 1024, 64 * 1024, 1024 * 1024]) {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const [descriptor] = allocateReadyDescriptors(arena, 1, size);
    const descriptors = [descriptor];
    const layout = planRuntimeGpuBatchLayout(descriptors, {
      dispatchItemsFrom: "u32",
      strict: true,
    });
    const output = payload(layout.totalBytes);
    const packTarget = new Uint8Array(layout.totalBytes);
    const layoutScratch = createRuntimeGpuBatchLayoutScratch();

    bench(`gpu layout strict u32 ${size / 1024}KB x1`, () => {
      planRuntimeGpuBatchLayout(descriptors, {
        dispatchItemsFrom: "u32",
        strict: true,
      });
    });

    bench(`gpu layout strict u32 into ${size / 1024}KB x1`, () => {
      planRuntimeGpuBatchLayoutInto(descriptors, layoutScratch, {
        dispatchItemsFrom: "u32",
        strict: true,
      });
    });

    bench(`gpu pack arena descriptors ${size / 1024}KB x1`, () => {
      packArenaDescriptors(arena, descriptors, layout);
    });

    bench(`gpu pack arena descriptors into ${size / 1024}KB x1`, () => {
      packArenaDescriptorsInto(arena, descriptors, layout, packTarget);
    });

    bench(`gpu writeback arena descriptors ${size / 1024}KB x1`, () => {
      writeGpuOutputToArena(arena, descriptors, layout, output);
    });
  }

  for (const batchSize of [8, 32, 128]) {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const descriptors = allocateReadyDescriptors(arena, batchSize, 1024);
    const layout = planRuntimeGpuBatchLayout(descriptors, {
      dispatchItemsFrom: "u32",
      strict: true,
    });
    const output = payload(layout.totalBytes);
    const packTarget = new Uint8Array(layout.totalBytes);
    const layoutScratch = createRuntimeGpuBatchLayoutScratch();

    bench(`gpu layout strict u32 1KB x${batchSize}`, () => {
      planRuntimeGpuBatchLayout(descriptors, {
        dispatchItemsFrom: "u32",
        strict: true,
      });
    });

    bench(`gpu layout strict u32 into 1KB x${batchSize}`, () => {
      planRuntimeGpuBatchLayoutInto(descriptors, layoutScratch, {
        dispatchItemsFrom: "u32",
        strict: true,
      });
    });

    bench(`gpu pack arena descriptors 1KB x${batchSize}`, () => {
      packArenaDescriptors(arena, descriptors, layout);
    });

    bench(`gpu pack arena descriptors into 1KB x${batchSize}`, () => {
      packArenaDescriptorsInto(arena, descriptors, layout, packTarget);
    });

    bench(`gpu writeback arena descriptors 1KB x${batchSize}`, () => {
      writeGpuOutputToArena(arena, descriptors, layout, output);
    });
  }
});

const residentHost = await RuntimeWebGpuHost.create({ device: createFakeGpuDevice() as any });
await residentHost.prewarmKernel({ shader: PASSTHROUGH_U32_SHADER });
const residentArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
const residentDescriptor = residentArena.allocate(4 * 1024);
residentArena.writeSlabReady(residentDescriptor.id, payload(4 * 1024));
const seedResidentDispatch = await residentHost.dispatchArenaBatch(residentArena, [residentDescriptor.id], {
  shader: PASSTHROUGH_U32_SHADER,
});
const seedResidentResourceId = seedResidentDispatch.resources.output?.id ?? -1;

describe("RuntimeWebGpuHost resident dispatch policies", () => {
  bench("webgpu fake dispatch gpu-resident 4KB x1", async () => {
    const dispatch = await residentHost.dispatchArenaBatch(residentArena, [residentDescriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
    });
    residentHost.destroyResource(dispatch.resources.output?.id ?? -1);
  });

  bench("webgpu fake dispatch materialize-readback 4KB x1", async () => {
    const dispatch = await residentHost.dispatchArenaBatch(residentArena, [residentDescriptor.id], {
      shader: PASSTHROUGH_U32_SHADER,
      dataPolicy: "materialize-readback",
    });
    residentHost.destroyResource(dispatch.resources.output?.id ?? -1);
  });

  bench("webgpu fake dispatch resident-to-resident 4KB x1", async () => {
    const dispatch = await residentHost.dispatchResidentBatch(seedResidentResourceId, {
      shader: PASSTHROUGH_U32_SHADER,
    });
    residentHost.destroyResource(dispatch.resources.output?.id ?? -1);
  });
});
