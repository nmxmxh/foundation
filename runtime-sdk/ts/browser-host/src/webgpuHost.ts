import { RuntimeSharedArena, type RuntimeArenaDescriptor } from "./arena";
import { planRuntimeGpuBatchLayout, type RuntimeGpuBatchLayout } from "./gpuLayout";

type RuntimeGpuBuffer = {
  getMappedRange(): ArrayBuffer;
  unmap(): void;
  mapAsync(mode: number): Promise<void>;
};

type RuntimeGpuComputePipeline = {
  getBindGroupLayout(index: number): unknown;
};

type RuntimeGpuDevice = {
  queue: {
    writeBuffer(buffer: RuntimeGpuBuffer, offset: number, data: Uint8Array): void;
    submit(commandBuffers: unknown[]): void;
  };
  createBuffer(descriptor: { label?: string; size: number; usage: number }): RuntimeGpuBuffer;
  createBindGroup(descriptor: { label?: string; layout: unknown; entries: Array<{ binding: number; resource: { buffer: RuntimeGpuBuffer } }> }): unknown;
  createCommandEncoder(descriptor?: { label?: string }): {
    beginComputePass(descriptor?: { label?: string }): {
      setPipeline(pipeline: RuntimeGpuComputePipeline): void;
      setBindGroup(index: number, bindGroup: unknown): void;
      dispatchWorkgroups(workgroupCountX: number, workgroupCountY?: number, workgroupCountZ?: number): void;
      end(): void;
    };
    copyBufferToBuffer(source: RuntimeGpuBuffer, sourceOffset: number, destination: RuntimeGpuBuffer, destinationOffset: number, size: number): void;
    finish(): unknown;
  };
  createShaderModule(descriptor: { code: string }): unknown;
  createComputePipelineAsync(descriptor: {
    label?: string;
    layout: "auto";
    compute: { module: unknown; entryPoint: string };
  }): Promise<RuntimeGpuComputePipeline>;
};

type RuntimeGpuAdapter = {
  requestDevice(): Promise<RuntimeGpuDevice>;
};

type RuntimeGpuNavigator = Navigator & {
  gpu?: {
    requestAdapter(options?: {
      powerPreference?: "low-power" | "high-performance";
      forceFallbackAdapter?: boolean;
    }): Promise<RuntimeGpuAdapter | null>;
  };
};

const GPU_BUFFER_USAGE = {
  MAP_READ: 1,
  COPY_DST: 8,
  COPY_SRC: 4,
  STORAGE: 128,
} as const;

const GPU_MAP_MODE_READ = 1;

export type RuntimeWebGpuKernel = {
  shader: string;
  entryPoint?: string;
  workgroupSize?: number;
};

export type RuntimeWebGpuDispatch = {
  descriptors: RuntimeArenaDescriptor[];
  layout: RuntimeGpuBatchLayout;
  elapsedMs: number;
};

export type RuntimeWebGpuHostOptions = {
  adapter?: RuntimeGpuAdapter;
  device?: RuntimeGpuDevice;
  powerPreference?: "low-power" | "high-performance";
  forceFallbackAdapter?: boolean;
};

export class RuntimeWebGpuHost {
  readonly device: RuntimeGpuDevice;
  private readonly pipelineCache = new Map<string, Promise<RuntimeGpuComputePipeline>>();

  private constructor(device: RuntimeGpuDevice) {
    this.device = device;
  }

  static async create(options: RuntimeWebGpuHostOptions = {}): Promise<RuntimeWebGpuHost> {
    if (options.device) {
      return new RuntimeWebGpuHost(options.device);
    }
    const gpu = (globalThis.navigator as RuntimeGpuNavigator | undefined)?.gpu;
    if (!gpu) {
      throw new Error("WebGPU is unavailable in this runtime");
    }
    const adapter = options.adapter ?? await gpu.requestAdapter({
      powerPreference: options.powerPreference ?? "high-performance",
      forceFallbackAdapter: options.forceFallbackAdapter,
    });
    if (!adapter) {
      throw new Error("WebGPU adapter is unavailable");
    }
    return new RuntimeWebGpuHost(await adapter.requestDevice());
  }

  async dispatchArenaBatch(
    arena: RuntimeSharedArena,
    descriptorIds: readonly number[],
    kernel: RuntimeWebGpuKernel
  ): Promise<RuntimeWebGpuDispatch> {
    if (descriptorIds.length === 0) {
      const layout = planRuntimeGpuBatchLayout([]);
      return { descriptors: [], layout, elapsedMs: 0 };
    }

    const descriptors = descriptorIds.map((id) => arena.readDescriptor(id));
    const layout = planRuntimeGpuBatchLayout(descriptors, { workgroupSize: kernel.workgroupSize });
    const input = packArenaDescriptors(arena, descriptors, layout);
    const inputBuffer = this.device.createBuffer({
      label: "ovasabi-runtime-webgpu-input",
      size: Math.max(4, input.byteLength),
      usage: GPU_BUFFER_USAGE.STORAGE | GPU_BUFFER_USAGE.COPY_DST,
    });
    const outputBuffer = this.device.createBuffer({
      label: "ovasabi-runtime-webgpu-output",
      size: Math.max(4, input.byteLength),
      usage: GPU_BUFFER_USAGE.STORAGE | GPU_BUFFER_USAGE.COPY_SRC,
    });
    const readbackBuffer = this.device.createBuffer({
      label: "ovasabi-runtime-webgpu-readback",
      size: Math.max(4, input.byteLength),
      usage: GPU_BUFFER_USAGE.MAP_READ | GPU_BUFFER_USAGE.COPY_DST,
    });

    this.device.queue.writeBuffer(inputBuffer, 0, input);
    const pipeline = await this.pipelineFor(kernel);
    const bindGroup = this.device.createBindGroup({
      label: "ovasabi-runtime-webgpu-bind-group",
      layout: pipeline.getBindGroupLayout(0),
      entries: [
        { binding: 0, resource: { buffer: inputBuffer } },
        { binding: 1, resource: { buffer: outputBuffer } },
      ],
    });

    const started = performance.now();
    const encoder = this.device.createCommandEncoder({ label: "ovasabi-runtime-webgpu-dispatch" });
    const pass = encoder.beginComputePass({ label: "ovasabi-runtime-webgpu-compute" });
    pass.setPipeline(pipeline);
    pass.setBindGroup(0, bindGroup);
    pass.dispatchWorkgroups(layout.workgroupCount);
    pass.end();
    encoder.copyBufferToBuffer(outputBuffer, 0, readbackBuffer, 0, Math.max(4, input.byteLength));
    this.device.queue.submit([encoder.finish()]);

    await readbackBuffer.mapAsync(GPU_MAP_MODE_READ);
    const output = new Uint8Array(readbackBuffer.getMappedRange()).slice(0, input.byteLength);
    readbackBuffer.unmap();
    writeGpuOutputToArena(arena, descriptors, layout, output);
    return { descriptors: descriptors.map((descriptor) => arena.readDescriptor(descriptor.id)), layout, elapsedMs: performance.now() - started };
  }

  private pipelineFor(kernel: RuntimeWebGpuKernel): Promise<RuntimeGpuComputePipeline> {
    const entryPoint = kernel.entryPoint ?? "main";
    const key = `${entryPoint}\n${kernel.shader}`;
    const cached = this.pipelineCache.get(key);
    if (cached) {
      return cached;
    }
    const pipeline = this.device.createComputePipelineAsync({
      label: "ovasabi-runtime-webgpu-pipeline",
      layout: "auto",
      compute: {
        module: this.device.createShaderModule({ code: kernel.shader }),
        entryPoint,
      },
    });
    this.pipelineCache.set(key, pipeline);
    return pipeline;
  }
}

export const PASSTHROUGH_U32_SHADER = `
@group(0) @binding(0) var<storage, read> input: array<u32>;
@group(0) @binding(1) var<storage, read_write> output: array<u32>;

@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) global_id: vec3<u32>) {
  let index = global_id.x;
  if (index < arrayLength(&input)) {
    output[index] = input[index];
  }
}
`;

export const packArenaDescriptors = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout = planRuntimeGpuBatchLayout(descriptors)
): Uint8Array => {
  const packed = new Uint8Array(layout.totalBytes);
  for (const region of layout.regions) {
    const descriptor = descriptors[region.index];
    if (!descriptor) {
      continue;
    }
    packed.set(arena.readSlabView(descriptor.id).subarray(0, region.byteLength), region.offset);
  }
  return packed;
};

export const writeGpuOutputToArena = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  output: Uint8Array
): void => {
  const writes = layout.regions.flatMap((region) => {
    const descriptor = descriptors[region.index];
    if (!descriptor) {
      return [];
    }
    return [{
      descriptorId: descriptor.id,
      data: output.subarray(region.offset, region.offset + region.byteLength),
    }];
  });
  arena.writeSlabsReady(writes);
};
