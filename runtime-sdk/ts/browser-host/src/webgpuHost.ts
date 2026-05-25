import { RuntimeSharedArena, type RuntimeArenaDescriptor } from "./arena";
import {
  planRuntimeGpuBatchLayout,
  type RuntimeGpuBatchLayout,
  type RuntimeGpuBatchLayoutOptions,
} from "./gpuLayout";

type RuntimeGpuBuffer = {
  getMappedRange(): ArrayBuffer;
  unmap(): void;
  mapAsync(mode: number): Promise<void>;
  destroy?: () => void;
};

type RuntimeGpuComputePipeline = {
  getBindGroupLayout(index: number): unknown;
};

type RuntimeGpuDeviceLostInfo = {
  reason?: string;
  message?: string;
};

type RuntimeGpuDevice = {
  limits?: {
    maxBufferSize?: number;
    maxStorageBufferBindingSize?: number;
    maxComputeWorkgroupsPerDimension?: number;
  };
  lost?: Promise<RuntimeGpuDeviceLostInfo>;
  queue: {
    writeBuffer(
      buffer: any,
      offset: number,
      data: Uint8Array,
      dataOffset?: number,
      size?: number
    ): void;
    submit(commandBuffers: any[]): void;
    onSubmittedWorkDone?: () => Promise<void>;
  };
  createBuffer(descriptor: any): RuntimeGpuBuffer;
  createBindGroup(descriptor: any): unknown;
  createCommandEncoder(descriptor?: any): {
    beginComputePass(descriptor?: any): {
      setPipeline(pipeline: any): void;
      setBindGroup(index: number, bindGroup: any): void;
      dispatchWorkgroups(workgroupCountX: number, workgroupCountY?: number, workgroupCountZ?: number): void;
      end(): void;
    };
    copyBufferToBuffer(source: any, sourceOffset: number, destination: any, destinationOffset: number, size: number): void;
    finish(): any;
  };
  createShaderModule(descriptor: any): unknown;
  createComputePipelineAsync(descriptor: any): Promise<RuntimeGpuComputePipeline>;
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
const DIRECT_ARENA_UPLOAD_MAX_REGIONS = 8;
const DEFAULT_MAX_POOLED_GPU_BUFFERS = 32;

export type RuntimeWebGpuDataPolicy =
  | "gpu-resident"
  | "direct-arena-upload"
  | "pack-then-upload"
  | "materialize-readback";

export type RuntimeWebGpuUploadMode = "none" | "direct-arena" | "packed";

export type RuntimeWebGpuResourceKind = "gpu-buffer" | "gpu-texture";

export type RuntimeWebGpuResourceDescriptor = {
  id: number;
  kind: RuntimeWebGpuResourceKind;
  label: string;
  byteLength: number;
  usage: number;
  schemaName?: string;
  format?: string;
  stride?: number;
  descriptorIds: number[];
  layout: RuntimeGpuBatchLayout;
  fallback: "materialize-readback";
};

type RuntimeWebGpuBufferRecord = RuntimeWebGpuResourceDescriptor & {
  kind: "gpu-buffer";
  buffer: RuntimeGpuBuffer;
};

export type RuntimeWebGpuKernel = {
  shader: string;
  entryPoint?: string;
  workgroupSize?: number;
  alignment?: number;
  dispatchItems?: number;
  dispatchItemsFrom?: RuntimeGpuBatchLayoutOptions["dispatchItemsFrom"];
  allowZeroByteRegions?: boolean;
  maxTotalBytes?: number;
  dataPolicy?: RuntimeWebGpuDataPolicy;
  schemaName?: string;
  format?: string;
  stride?: number;
};

export type RuntimeWebGpuDispatchTimings = {
  packMs: number;
  pipelineMs: number;
  uploadMs: number;
  dispatchMs: number;
  readbackMs: number;
  dispatchReadbackMs: number;
  writebackMs: number;
  totalMs: number;
};

export type RuntimeWebGpuDispatch = {
  descriptors: RuntimeArenaDescriptor[];
  layout: RuntimeGpuBatchLayout;
  elapsedMs: number;
  timings: RuntimeWebGpuDispatchTimings;
  policy: RuntimeWebGpuDataPolicy;
  uploadMode: RuntimeWebGpuUploadMode;
  materialized: boolean;
  resources: {
    output?: RuntimeWebGpuResourceDescriptor;
  };
};

export type RuntimeWebGpuMaterializeResult = {
  descriptors: RuntimeArenaDescriptor[];
  elapsedMs: number;
  timings: Pick<RuntimeWebGpuDispatchTimings, "readbackMs" | "writebackMs" | "totalMs">;
};

export type RuntimeWebGpuDispatchTimingsNs = {
  packNs: number;
  pipelineNs: number;
  uploadNs: number;
  dispatchNs: number;
  readbackNs: number;
  dispatchReadbackNs: number;
  writebackNs: number;
  totalNs: number;
};

export type RuntimeWebGpuDeviceTimingProbeOptions = RuntimeWebGpuHostOptions & {
  payloadBytes?: number;
  shader?: string;
  dataPolicy?: RuntimeWebGpuDataPolicy;
  materialize?: boolean;
  waitForSubmittedWork?: boolean;
};

export type RuntimeWebGpuDeviceTimingProbeResult =
  | {
      available: true;
      adapterNs: number;
      deviceNs: number;
      prewarmNs: number;
      dispatchNs: number;
      queueDrainNs: number;
      materializeNs: number;
      totalNs: number;
      uploadMode: RuntimeWebGpuUploadMode;
      materialized: boolean;
      dispatchTimingsNs: RuntimeWebGpuDispatchTimingsNs;
      materializeTimingsNs?: Pick<RuntimeWebGpuDispatchTimingsNs, "readbackNs" | "writebackNs" | "totalNs">;
      resource?: RuntimeWebGpuResourceDescriptor;
    }
  | {
      available: false;
      reason: string;
    };

export type RuntimeWebGpuDeviceLossState = {
  lost: boolean;
  reason?: string;
  message?: string;
  resourcesDropped: number;
  pooledBuffersDropped: number;
  pipelinesDropped: number;
};

export type RuntimeWebGpuHostOptions = {
  adapter?: RuntimeGpuAdapter;
  device?: RuntimeGpuDevice;
  powerPreference?: "low-power" | "high-performance";
  forceFallbackAdapter?: boolean;
  maxPooledBuffers?: number;
};

export class RuntimeWebGpuHost {
  readonly device: RuntimeGpuDevice;
  private readonly pipelineCache = new Map<string, Promise<RuntimeGpuComputePipeline>>();
  private readonly bufferResources = new Map<number, RuntimeWebGpuBufferRecord>();
  private readonly bufferPool = new Map<string, RuntimeGpuBuffer[]>();
  private readonly maxPooledBuffers: number;
  private deviceLossStateValue: RuntimeWebGpuDeviceLossState = {
    lost: false,
    resourcesDropped: 0,
    pooledBuffersDropped: 0,
    pipelinesDropped: 0,
  };
  private nextResourceId = 1;
  private pooledBufferCount = 0;

  private constructor(device: RuntimeGpuDevice, options: Pick<RuntimeWebGpuHostOptions, "maxPooledBuffers"> = {}) {
    this.device = device;
    this.maxPooledBuffers = normalizePooledBufferLimit(options.maxPooledBuffers);
    this.observeDeviceLoss(device);
  }

  static async create(options: RuntimeWebGpuHostOptions = {}): Promise<RuntimeWebGpuHost> {
    if (options.device) {
      return new RuntimeWebGpuHost(options.device, options);
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
    return new RuntimeWebGpuHost(await adapter.requestDevice(), options);
  }

  async prewarmKernel(kernel: RuntimeWebGpuKernel): Promise<void> {
    this.assertDeviceAvailable();
    await this.pipelineFor(kernel);
  }

  async waitForSubmittedWork(): Promise<boolean> {
    const wait = this.device.queue.onSubmittedWorkDone;
    if (!wait) {
      return false;
    }
    await wait.call(this.device.queue);
    return true;
  }

  async dispatchArenaBatch(
    arena: RuntimeSharedArena,
    descriptorIds: readonly number[],
    kernel: RuntimeWebGpuKernel
  ): Promise<RuntimeWebGpuDispatch> {
    this.assertDeviceAvailable();
    const totalStarted = nowMs();
    if (descriptorIds.length === 0) {
      const layout = planRuntimeGpuBatchLayout([], {
        alignment: kernel.alignment,
        allowZeroByteRegions: true,
        dispatchItems: 1,
        maxTotalBytes: kernel.maxTotalBytes ?? maxGpuBufferBytes(this.device),
        strict: true,
        workgroupSize: kernel.workgroupSize,
      });
      const timings = emptyTimings(nowMs() - totalStarted);
      return {
        descriptors: [],
        layout,
        elapsedMs: timings.totalMs,
        timings,
        policy: kernel.dataPolicy ?? "gpu-resident",
        uploadMode: "none",
        materialized: false,
        resources: {},
      };
    }

    const policy = kernel.dataPolicy ?? "gpu-resident";
    const descriptors = descriptorIds.map((id) => arena.readDescriptor(id));
    const layout = planRuntimeGpuBatchLayout(descriptors, {
      alignment: kernel.alignment,
      allowZeroByteRegions: kernel.allowZeroByteRegions,
      dispatchItems: kernel.dispatchItems,
      dispatchItemsFrom: kernel.dispatchItemsFrom ?? "u32",
      maxTotalBytes: kernel.maxTotalBytes ?? maxGpuBufferBytes(this.device),
      strict: true,
      workgroupSize: kernel.workgroupSize,
    });
    validateGpuLayoutForDescriptors(descriptors, layout);
    validateGpuDispatchLimits(this.device, layout);

    const uploadMode = chooseUploadMode(policy, layout);
    const packStarted = nowMs();
    const input = uploadMode === "packed" ? new Uint8Array(layout.totalBytes) : null;
    if (input) {
      packArenaDescriptorsIntoUnchecked(arena, descriptors, layout, input);
    }
    const packMs = nowMs() - packStarted;
    const bufferSize = Math.max(4, layout.totalBytes);
    validateGpuBufferSize(this.device, bufferSize);

    const inputUsage = GPU_BUFFER_USAGE.STORAGE | GPU_BUFFER_USAGE.COPY_DST;
    const outputUsage = GPU_BUFFER_USAGE.STORAGE | GPU_BUFFER_USAGE.COPY_SRC | GPU_BUFFER_USAGE.COPY_DST;
    const inputBuffer = this.acquireBuffer({
      label: "ovasabi-runtime-webgpu-input",
      size: bufferSize,
      usage: inputUsage,
    });
    const outputBuffer = this.acquireBuffer({
      label: "ovasabi-runtime-webgpu-output",
      size: bufferSize,
      usage: outputUsage,
    });

    const uploadStarted = nowMs();
    if (input) {
      this.device.queue.writeBuffer(inputBuffer, 0, input);
    } else {
      uploadArenaDescriptorsDirect(this.device, arena, descriptors, layout, inputBuffer);
    }
    const uploadMs = nowMs() - uploadStarted;

    const pipelineStarted = nowMs();
    const pipeline = await this.pipelineFor(kernel);
    const pipelineMs = nowMs() - pipelineStarted;
    const bindGroup = this.device.createBindGroup({
      label: "ovasabi-runtime-webgpu-bind-group",
      layout: pipeline.getBindGroupLayout(0),
      entries: [
        { binding: 0, resource: { buffer: inputBuffer } },
        { binding: 1, resource: { buffer: outputBuffer } },
      ],
    });

    const dispatchStarted = nowMs();
    const encoder = this.device.createCommandEncoder({ label: "ovasabi-runtime-webgpu-dispatch" });
    const pass = encoder.beginComputePass({ label: "ovasabi-runtime-webgpu-compute" });
    pass.setPipeline(pipeline);
    pass.setBindGroup(0, bindGroup);
    pass.dispatchWorkgroups(layout.workgroupCount);
    pass.end();
    this.device.queue.submit([encoder.finish()]);
    this.releaseBuffer(inputBuffer, bufferSize, inputUsage);
    const dispatchMs = nowMs() - dispatchStarted;

    const outputResource = this.registerBufferResource(outputBuffer, {
      label: "ovasabi-runtime-webgpu-output",
      byteLength: layout.totalBytes,
      usage: outputUsage,
      layout,
      descriptorIds: [...descriptorIds],
      schemaName: kernel.schemaName,
      format: kernel.format,
      stride: kernel.stride,
    });

    if (policy !== "materialize-readback") {
      const totalMs = nowMs() - totalStarted;
      const timings = {
        packMs,
        pipelineMs,
        uploadMs,
        dispatchMs,
        readbackMs: 0,
        dispatchReadbackMs: dispatchMs,
        writebackMs: 0,
        totalMs,
      };
      return {
        descriptors: [],
        layout,
        elapsedMs: totalMs,
        timings,
        policy,
        uploadMode,
        materialized: false,
        resources: { output: outputResource },
      };
    }

    const materialized = await this.materializeResourceToArena(arena, outputResource.id, descriptorIds);
    const readbackMs = materialized.timings.readbackMs;
    const dispatchReadbackMs = dispatchMs + readbackMs;
    const writebackMs = materialized.timings.writebackMs;
    const totalMs = nowMs() - totalStarted;
    const timings = { packMs, pipelineMs, uploadMs, dispatchMs, readbackMs, dispatchReadbackMs, writebackMs, totalMs };
    return {
      descriptors: materialized.descriptors,
      layout,
      elapsedMs: totalMs,
      timings,
      policy,
      uploadMode,
      materialized: true,
      resources: { output: outputResource },
    };
  }

  async dispatchResidentBatch(
    inputResourceId: number,
    kernel: RuntimeWebGpuKernel
  ): Promise<RuntimeWebGpuDispatch> {
    this.assertDeviceAvailable();
    const totalStarted = nowMs();
    const inputRecord = this.getBufferRecord(inputResourceId);
    const layout = cloneGpuLayout(inputRecord.layout);
    validateGpuDispatchLimits(this.device, layout);
    validateGpuBufferSize(this.device, Math.max(4, layout.totalBytes));

    const outputUsage = GPU_BUFFER_USAGE.STORAGE | GPU_BUFFER_USAGE.COPY_SRC | GPU_BUFFER_USAGE.COPY_DST;
    const outputBuffer = this.acquireBuffer({
      label: "ovasabi-runtime-webgpu-output",
      size: Math.max(4, layout.totalBytes),
      usage: outputUsage,
    });
    const pipelineStarted = nowMs();
    const pipeline = await this.pipelineFor(kernel);
    const pipelineMs = nowMs() - pipelineStarted;
    const bindGroup = this.device.createBindGroup({
      label: "ovasabi-runtime-webgpu-resident-bind-group",
      layout: pipeline.getBindGroupLayout(0),
      entries: [
        { binding: 0, resource: { buffer: inputRecord.buffer } },
        { binding: 1, resource: { buffer: outputBuffer } },
      ],
    });

    const dispatchStarted = nowMs();
    const encoder = this.device.createCommandEncoder({ label: "ovasabi-runtime-webgpu-resident-dispatch" });
    const pass = encoder.beginComputePass({ label: "ovasabi-runtime-webgpu-resident-compute" });
    pass.setPipeline(pipeline);
    pass.setBindGroup(0, bindGroup);
    pass.dispatchWorkgroups(layout.workgroupCount);
    pass.end();
    this.device.queue.submit([encoder.finish()]);
    const dispatchMs = nowMs() - dispatchStarted;

    const outputResource = this.registerBufferResource(outputBuffer, {
      label: "ovasabi-runtime-webgpu-output",
      byteLength: layout.totalBytes,
      usage: outputUsage,
      layout,
      descriptorIds: [...inputRecord.descriptorIds],
      schemaName: kernel.schemaName ?? inputRecord.schemaName,
      format: kernel.format ?? inputRecord.format,
      stride: kernel.stride ?? inputRecord.stride,
    });
    const totalMs = nowMs() - totalStarted;
    const timings = {
      packMs: 0,
      pipelineMs,
      uploadMs: 0,
      dispatchMs,
      readbackMs: 0,
      dispatchReadbackMs: dispatchMs,
      writebackMs: 0,
      totalMs,
    };
    return {
      descriptors: [],
      layout,
      elapsedMs: totalMs,
      timings,
      policy: "gpu-resident",
      uploadMode: "none",
      materialized: false,
      resources: { output: outputResource },
    };
  }

  async materializeResourceToArena(
    arena: RuntimeSharedArena,
    resourceId: number,
    descriptorIds?: readonly number[]
  ): Promise<RuntimeWebGpuMaterializeResult> {
    this.assertDeviceAvailable();
    const started = nowMs();
    const record = this.getBufferRecord(resourceId);
    const targetIds = descriptorIds ?? record.descriptorIds;
    const descriptors = targetIds.map((id) => arena.readDescriptor(id));
    validateGpuLayoutForDescriptors(descriptors, record.layout);
    const bufferSize = Math.max(4, record.byteLength);
    const readbackUsage = GPU_BUFFER_USAGE.MAP_READ | GPU_BUFFER_USAGE.COPY_DST;
    const readbackBuffer = this.acquireBuffer({
      label: "ovasabi-runtime-webgpu-readback",
      size: bufferSize,
      usage: readbackUsage,
    });

    const readbackStarted = nowMs();
    const encoder = this.device.createCommandEncoder({ label: "ovasabi-runtime-webgpu-materialize" });
    encoder.copyBufferToBuffer(record.buffer, 0, readbackBuffer, 0, bufferSize);
    this.device.queue.submit([encoder.finish()]);
    await readbackBuffer.mapAsync(GPU_MAP_MODE_READ);
    const output = new Uint8Array(readbackBuffer.getMappedRange(), 0, record.layout.totalBytes);
    const readbackMs = nowMs() - readbackStarted;

    const writebackStarted = nowMs();
    writeGpuOutputToArenaUnchecked(arena, descriptors, record.layout, output);
    readbackBuffer.unmap();
    this.releaseBuffer(readbackBuffer, bufferSize, readbackUsage);
    const writebackMs = nowMs() - writebackStarted;
    const totalMs = nowMs() - started;
    return {
      descriptors: targetIds.map((id) => arena.readDescriptor(id)),
      elapsedMs: totalMs,
      timings: { readbackMs, writebackMs, totalMs },
    };
  }

  getResource(resourceId: number): RuntimeWebGpuResourceDescriptor | undefined {
    const record = this.bufferResources.get(resourceId);
    return record ? publicResourceDescriptor(record) : undefined;
  }

  getDeviceLossState(): RuntimeWebGpuDeviceLossState {
    return { ...this.deviceLossStateValue };
  }

  destroyResource(resourceId: number): boolean {
    const record = this.bufferResources.get(resourceId);
    if (!record) {
      return false;
    }
    this.bufferResources.delete(resourceId);
    this.releaseBuffer(record.buffer, Math.max(4, record.byteLength), record.usage);
    return true;
  }

  private observeDeviceLoss(device: RuntimeGpuDevice): void {
    device.lost?.then((info) => {
      const dropped = this.dropDeviceResources();
      this.deviceLossStateValue = {
        lost: true,
        reason: info.reason,
        message: info.message,
        ...dropped,
      };
    }).catch((error) => {
      const dropped = this.dropDeviceResources();
      this.deviceLossStateValue = {
        lost: true,
        reason: "unknown",
        message: error instanceof Error ? error.message : String(error),
        ...dropped,
      };
    });
  }

  private assertDeviceAvailable(): void {
    if (!this.deviceLossStateValue.lost) {
      return;
    }
    const reason = this.deviceLossStateValue.reason ? `: ${this.deviceLossStateValue.reason}` : "";
    throw new Error(`runtime WebGPU device is lost${reason}`);
  }

  private dropDeviceResources(): Pick<
    RuntimeWebGpuDeviceLossState,
    "resourcesDropped" | "pooledBuffersDropped" | "pipelinesDropped"
  > {
    let resourcesDropped = 0;
    for (const record of this.bufferResources.values()) {
      record.buffer.destroy?.();
      resourcesDropped += 1;
    }
    this.bufferResources.clear();

    let pooledBuffersDropped = 0;
    for (const buffers of this.bufferPool.values()) {
      for (const buffer of buffers) {
        buffer.destroy?.();
        pooledBuffersDropped += 1;
      }
    }
    this.bufferPool.clear();
    this.pooledBufferCount = 0;

    const pipelinesDropped = this.pipelineCache.size;
    this.pipelineCache.clear();
    return { resourcesDropped, pooledBuffersDropped, pipelinesDropped };
  }

  private pipelineFor(kernel: RuntimeWebGpuKernel): Promise<RuntimeGpuComputePipeline> {
    this.assertDeviceAvailable();
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

  private registerBufferResource(
    buffer: RuntimeGpuBuffer,
    options: Omit<RuntimeWebGpuBufferRecord, "id" | "kind" | "buffer" | "fallback" | "layout"> & {
      layout: RuntimeGpuBatchLayout;
    }
  ): RuntimeWebGpuResourceDescriptor {
    const record: RuntimeWebGpuBufferRecord = {
      id: this.nextResourceId,
      kind: "gpu-buffer",
      buffer,
      fallback: "materialize-readback",
      ...options,
      descriptorIds: [...options.descriptorIds],
      layout: cloneGpuLayout(options.layout),
    };
    this.nextResourceId += 1;
    this.bufferResources.set(record.id, record);
    return publicResourceDescriptor(record);
  }

  private getBufferRecord(resourceId: number): RuntimeWebGpuBufferRecord {
    const record = this.bufferResources.get(resourceId);
    if (!record) {
      throw new Error(`runtime WebGPU resource ${resourceId} is unavailable`);
    }
    return record;
  }

  private acquireBuffer(descriptor: { label: string; size: number; usage: number }): RuntimeGpuBuffer {
    const size = Math.max(4, descriptor.size);
    const key = gpuBufferPoolKey(size, descriptor.usage);
    const buffers = this.bufferPool.get(key);
    const pooled = buffers?.pop();
    if (pooled) {
      this.pooledBufferCount -= 1;
      return pooled;
    }
    return this.device.createBuffer({ ...descriptor, size });
  }

  private releaseBuffer(buffer: RuntimeGpuBuffer, size: number, usage: number): void {
    if (this.maxPooledBuffers <= 0 || this.pooledBufferCount >= this.maxPooledBuffers) {
      buffer.destroy?.();
      return;
    }
    const key = gpuBufferPoolKey(Math.max(4, size), usage);
    const buffers = this.bufferPool.get(key);
    if (buffers) {
      buffers.push(buffer);
    } else {
      this.bufferPool.set(key, [buffer]);
    }
    this.pooledBufferCount += 1;
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
  validateGpuLayoutForDescriptors(descriptors, layout);
  const packed = new Uint8Array(layout.totalBytes);
  packArenaDescriptorsIntoUnchecked(arena, descriptors, layout, packed);
  return packed;
};

export const packArenaDescriptorsInto = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  target: Uint8Array
): void => {
  validateGpuLayoutForDescriptors(descriptors, layout);
  if (target.byteLength < layout.totalBytes) {
    throw new Error(`runtime GPU pack target too short: ${target.byteLength} < ${layout.totalBytes}`);
  }
  packArenaDescriptorsIntoUnchecked(arena, descriptors, layout, target);
};

const packArenaDescriptorsIntoUnchecked = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  target: Uint8Array
): void => {
  for (const region of layout.regions) {
    const descriptor = descriptors[region.index];
    arena.copyDescriptorBytesTo(descriptor, target, region.offset, region.byteLength);
  }
};

export const writeGpuOutputToArena = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  output: Uint8Array
): void => {
  validateGpuLayoutForDescriptors(descriptors, layout);
  validateGpuOutputLength(layout, output);
  writeGpuOutputToArenaUnchecked(arena, descriptors, layout, output);
};

export const measureRuntimeWebGpuDeviceRoundTrip = async (
  options: RuntimeWebGpuDeviceTimingProbeOptions = {}
): Promise<RuntimeWebGpuDeviceTimingProbeResult> => {
  const payloadBytes = Math.max(4, Math.floor(options.payloadBytes ?? 4 * 1024));
  const totalStarted = nowMs();
  let adapterNs = 0;
  let deviceNs = 0;
  let device = options.device;

  if (!device) {
    let adapter: RuntimeGpuAdapter | null | undefined = options.adapter;
    if (!adapter) {
      const gpu = (globalThis.navigator as RuntimeGpuNavigator | undefined)?.gpu;
      if (!gpu) {
        return { available: false, reason: "WebGPU is unavailable in this runtime" };
      }
      const adapterStarted = nowMs();
      adapter = await gpu.requestAdapter({
        powerPreference: options.powerPreference ?? "high-performance",
        forceFallbackAdapter: options.forceFallbackAdapter,
      });
      adapterNs = msToNs(nowMs() - adapterStarted);
    }
    if (!adapter) {
      return { available: false, reason: "WebGPU adapter is unavailable" };
    }
    const deviceStarted = nowMs();
    device = await adapter.requestDevice();
    deviceNs = msToNs(nowMs() - deviceStarted);
  }

  const host = await RuntimeWebGpuHost.create({
    device,
    maxPooledBuffers: options.maxPooledBuffers,
  });
  const arena = RuntimeSharedArena.create({
    arenaBytes: Math.max(1024 * 1024, payloadBytes + 256 * 1024),
  });
  const payload = new Uint8Array(payloadBytes);
  for (let index = 0; index < payload.byteLength; index += 1) {
    payload[index] = index & 0xff;
  }
  const descriptor = arena.allocate(payload.byteLength);
  arena.writeSlabReady(descriptor.id, payload);

  const shader = options.shader ?? PASSTHROUGH_U32_SHADER;
  const prewarmStarted = nowMs();
  await host.prewarmKernel({ shader });
  const prewarmNs = msToNs(nowMs() - prewarmStarted);

  const dispatchStarted = nowMs();
  const dispatch = await host.dispatchArenaBatch(arena, [descriptor.id], {
    shader,
    dataPolicy: options.dataPolicy ?? "gpu-resident",
  });
  const dispatchNs = msToNs(nowMs() - dispatchStarted);

  let queueDrainNs = 0;
  if (options.waitForSubmittedWork === true) {
    const drainStarted = nowMs();
    await host.waitForSubmittedWork();
    queueDrainNs = msToNs(nowMs() - drainStarted);
  }

  let materializeNs = 0;
  let materializeTimingsNs: Pick<RuntimeWebGpuDispatchTimingsNs, "readbackNs" | "writebackNs" | "totalNs"> | undefined;
  const resource = dispatch.resources.output;
  if (resource && options.materialize !== false && !dispatch.materialized) {
    const materializeStarted = nowMs();
    const materialized = await host.materializeResourceToArena(arena, resource.id);
    materializeNs = msToNs(nowMs() - materializeStarted);
    materializeTimingsNs = {
      readbackNs: msToNs(materialized.timings.readbackMs),
      writebackNs: msToNs(materialized.timings.writebackMs),
      totalNs: msToNs(materialized.timings.totalMs),
    };
  }
  if (resource) {
    host.destroyResource(resource.id);
  }

  return {
    available: true,
    adapterNs,
    deviceNs,
    prewarmNs,
    dispatchNs,
    queueDrainNs,
    materializeNs,
    totalNs: msToNs(nowMs() - totalStarted),
    uploadMode: dispatch.uploadMode,
    materialized: dispatch.materialized || materializeTimingsNs !== undefined,
    dispatchTimingsNs: dispatchTimingsToNs(dispatch.timings),
    materializeTimingsNs,
    resource,
  };
};

const writeGpuOutputToArenaUnchecked = (
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  output: Uint8Array
): void => {
  for (const region of layout.regions) {
    const descriptor = descriptors[region.index];
    arena.writeDescriptorBytesReady(descriptor, output, region.offset, region.byteLength);
  }
};

const nowMs = (): number => {
  const perf = globalThis.performance;
  return perf?.now ? perf.now() : Date.now();
};

const msToNs = (value: number): number => Math.round(value * 1_000_000);

const dispatchTimingsToNs = (timings: RuntimeWebGpuDispatchTimings): RuntimeWebGpuDispatchTimingsNs => ({
  packNs: msToNs(timings.packMs),
  pipelineNs: msToNs(timings.pipelineMs),
  uploadNs: msToNs(timings.uploadMs),
  dispatchNs: msToNs(timings.dispatchMs),
  readbackNs: msToNs(timings.readbackMs),
  dispatchReadbackNs: msToNs(timings.dispatchReadbackMs),
  writebackNs: msToNs(timings.writebackMs),
  totalNs: msToNs(timings.totalMs),
});

const emptyTimings = (totalMs: number): RuntimeWebGpuDispatchTimings => ({
  packMs: 0,
  pipelineMs: 0,
  uploadMs: 0,
  dispatchMs: 0,
  readbackMs: 0,
  dispatchReadbackMs: 0,
  writebackMs: 0,
  totalMs,
});

const chooseUploadMode = (
  policy: RuntimeWebGpuDataPolicy,
  layout: RuntimeGpuBatchLayout
): RuntimeWebGpuUploadMode => {
  switch (policy) {
    case "direct-arena-upload":
      return canUseDirectArenaUpload(layout) ? "direct-arena" : "packed";
    case "pack-then-upload":
    case "materialize-readback":
      return "packed";
    case "gpu-resident":
      return canUseDirectArenaUpload(layout) ? "direct-arena" : "packed";
  }
};

const canUseDirectArenaUpload = (layout: RuntimeGpuBatchLayout): boolean => {
  if (layout.regions.length > DIRECT_ARENA_UPLOAD_MAX_REGIONS) {
    return false;
  }
  for (const region of layout.regions) {
    if (region.offset % 4 !== 0 || region.byteLength % 4 !== 0) {
      return false;
    }
  }
  return true;
};

const normalizePooledBufferLimit = (value: number | undefined): number => {
  if (value === undefined) {
    return DEFAULT_MAX_POOLED_GPU_BUFFERS;
  }
  if (!Number.isFinite(value) || value < 0) {
    throw new Error("runtime WebGPU max pooled buffers must be a non-negative finite number");
  }
  return Math.floor(value);
};

const gpuBufferPoolKey = (size: number, usage: number): string => `${size}:${usage}`;

const uploadArenaDescriptorsDirect = (
  device: RuntimeGpuDevice,
  arena: RuntimeSharedArena,
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout,
  inputBuffer: RuntimeGpuBuffer
): void => {
  const arenaBytes = arena.readArenaBytesView();
  for (const region of layout.regions) {
    const descriptor = descriptors[region.index];
    device.queue.writeBuffer(
      inputBuffer,
      region.offset,
      arenaBytes,
      arena.descriptorByteOffset(descriptor, region.byteLength),
      region.byteLength
    );
  }
};

const cloneGpuLayout = (layout: RuntimeGpuBatchLayout): RuntimeGpuBatchLayout => ({
  alignment: layout.alignment,
  itemCount: layout.itemCount,
  payloadBytes: layout.payloadBytes,
  paddingBytes: layout.paddingBytes,
  maxRegionBytes: layout.maxRegionBytes,
  totalBytes: layout.totalBytes,
  regions: layout.regions.map((region) => ({ ...region })),
  workgroupSize: layout.workgroupSize,
  workgroupCount: layout.workgroupCount,
  dispatchItems: layout.dispatchItems,
  dispatchWaste: layout.dispatchWaste,
});

const publicResourceDescriptor = (
  record: RuntimeWebGpuBufferRecord
): RuntimeWebGpuResourceDescriptor => ({
  id: record.id,
  kind: record.kind,
  label: record.label,
  byteLength: record.byteLength,
  usage: record.usage,
  schemaName: record.schemaName,
  format: record.format,
  stride: record.stride,
  descriptorIds: [...record.descriptorIds],
  layout: cloneGpuLayout(record.layout),
  fallback: record.fallback,
});

const maxGpuBufferBytes = (device: RuntimeGpuDevice): number | undefined => {
  const candidates = [
    device.limits?.maxBufferSize,
    device.limits?.maxStorageBufferBindingSize,
  ].filter(
    (value): value is number => typeof value === "number" && Number.isFinite(value) && value > 0
  );
  if (candidates.length === 0) {
    return undefined;
  }
  return Math.min(...candidates);
};

const validateGpuBufferSize = (device: RuntimeGpuDevice, bufferSize: number): void => {
  const maxBufferBytes = maxGpuBufferBytes(device);
  if (maxBufferBytes !== undefined && bufferSize > maxBufferBytes) {
    throw new Error(`runtime WebGPU buffer exceeds device limit: ${bufferSize} > ${maxBufferBytes}`);
  }
};

const validateGpuDispatchLimits = (device: RuntimeGpuDevice, layout: RuntimeGpuBatchLayout): void => {
  const maxWorkgroups = device.limits?.maxComputeWorkgroupsPerDimension;
  if (
    Number.isFinite(maxWorkgroups) &&
    maxWorkgroups !== undefined &&
    layout.workgroupCount > maxWorkgroups
  ) {
    throw new Error(`runtime WebGPU dispatch exceeds workgroup limit: ${layout.workgroupCount} > ${maxWorkgroups}`);
  }
};

const validateGpuLayoutForDescriptors = (
  descriptors: readonly RuntimeArenaDescriptor[],
  layout: RuntimeGpuBatchLayout
): void => {
  if (layout.regions.length !== descriptors.length) {
    throw new Error(
      `runtime GPU layout/descriptors mismatch: ${layout.regions.length} regions for ${descriptors.length} descriptors`
    );
  }
  for (const region of layout.regions) {
    const descriptor = descriptors[region.index];
    if (!descriptor) {
      throw new Error(`runtime GPU layout references missing descriptor index ${region.index}`);
    }
    if (region.byteLength > descriptor.length) {
      throw new Error(`runtime GPU region exceeds descriptor length: ${region.byteLength} > ${descriptor.length}`);
    }
    if (region.offset + region.byteLength > layout.totalBytes) {
      throw new Error(
        `runtime GPU region exceeds layout byte length: ${region.offset + region.byteLength} > ${layout.totalBytes}`
      );
    }
  }
};

const validateGpuOutputLength = (layout: RuntimeGpuBatchLayout, output: Uint8Array): void => {
  if (output.byteLength < layout.totalBytes) {
    throw new Error(`runtime GPU output too short: ${output.byteLength} < ${layout.totalBytes}`);
  }
};
