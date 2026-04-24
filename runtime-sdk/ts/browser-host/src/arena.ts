import {
  ARENA_DEFAULT_BYTES,
  ARENA_DESCRIPTOR_COUNT,
  ARENA_DESCRIPTOR_SIZE,
  ARENA_DESCRIPTOR_STATE_ALLOCATED,
  ARENA_DESCRIPTOR_STATE_CONSUMED,
  ARENA_DESCRIPTOR_STATE_FREE,
  ARENA_DESCRIPTOR_STATE_READY,
  ARENA_DESCRIPTOR_TYPE_BYTES,
  ARENA_DIAGNOSTIC_BYTES,
  ARENA_HEADER_IDX_ALLOCATED_BYTES,
  ARENA_HEADER_IDX_CAPACITY_BYTES,
  ARENA_HEADER_IDX_DESCRIPTOR_COUNT,
  ARENA_HEADER_IDX_MAGIC,
  ARENA_HEADER_IDX_QUEUE_DROPPED,
  ARENA_HEADER_IDX_SCHEMA_VERSION,
  ARENA_HEADER_MAGIC,
  ARENA_HEAVY_BYTES,
  ARENA_IDX_ALLOC_HEAD,
  ARENA_IDX_BACKPRESSURE,
  ARENA_IDX_DESCRIPTOR_EPOCH,
  ARENA_IDX_DIAGNOSTICS_EPOCH,
  ARENA_IDX_QUEUE_EPOCH,
  ARENA_IDX_QUEUE_HEAD,
  ARENA_IDX_QUEUE_TAIL,
  ARENA_IDX_READY,
  ARENA_INTERACTIVE_BYTES,
  ARENA_MAX_BYTES,
  ARENA_MIN_BYTES,
  ARENA_OFFSET_DESCRIPTOR_TABLE,
  ARENA_OFFSET_DIAGNOSTICS,
  ARENA_OFFSET_EPOCHS,
  ARENA_OFFSET_PAGES,
  ARENA_OFFSET_QUEUE,
  ARENA_PAGE_BYTES,
  ARENA_QUEUE_OP_DESCRIPTOR_READY,
  ARENA_QUEUE_SLOT_COUNT,
  ARENA_QUEUE_SLOT_SIZE,
  ARENA_SCHEMA_VERSION,
  BUFFER_TOTAL_BYTES,
} from "./generated/runtimeBuffer";
import { getRuntimeCapabilities } from "./pulse/runtimeCaps";
import type { RuntimeCapabilities } from "./types";

export type RuntimeSharedMemoryMode = "off" | "auto" | "required";
export type RuntimeTransportLane = "postMessage" | "transferable" | "sab" | "ws" | "http";
export type RuntimeCompressionEncoding = "identity" | "gzip" | "br" | "deflate";
export type RuntimeArenaProfile = "minimal" | "default" | "interactive" | "heavy";
export type RuntimeDescriptorState =
  | typeof ARENA_DESCRIPTOR_STATE_FREE
  | typeof ARENA_DESCRIPTOR_STATE_ALLOCATED
  | typeof ARENA_DESCRIPTOR_STATE_READY
  | typeof ARENA_DESCRIPTOR_STATE_CONSUMED;
export type RuntimeDescriptorType =
  | typeof ARENA_DESCRIPTOR_TYPE_BYTES
  | 1
  | 2
  | 3
  | 4;

export type RuntimeMemoryOptions = {
  sharedMemory?: RuntimeSharedMemoryMode;
  arenaBytes?: number;
  arenaProfile?: RuntimeArenaProfile;
  transportOrder?: RuntimeTransportLane[];
  compression?: RuntimeCompressionEncoding[];
  requireSharedWasmMemory?: boolean;
};

export type RuntimeArenaDescriptor = {
  id: number;
  state: RuntimeDescriptorState;
  offset: number;
  length: number;
  capacity: number;
  type: RuntimeDescriptorType;
  flags: number;
  producerEpoch: number;
  consumerEpoch: number;
};

export type RuntimeArenaQueueEntry = {
  op: number;
  descriptorId: number;
  offset: number;
  length: number;
  flags: number;
  correlationHash: number;
  epoch: number;
};

export type RuntimeMemorySelection = {
  controlBuffer: SharedArrayBuffer | ArrayBuffer;
  arena: RuntimeSharedArena | null;
  capabilities: RuntimeCapabilities;
  transportOrder: RuntimeTransportLane[];
  compression: RuntimeCompressionEncoding[];
  degraded: boolean;
  issues: string[];
};

const descriptorOffset = (id: number): number =>
  ARENA_OFFSET_DESCRIPTOR_TABLE + id * ARENA_DESCRIPTOR_SIZE;

const queueSlotOffset = (slot: number): number =>
  ARENA_OFFSET_QUEUE + slot * ARENA_QUEUE_SLOT_SIZE;

const alignToPage = (value: number): number =>
  Math.ceil(value / ARENA_PAGE_BYTES) * ARENA_PAGE_BYTES;

const arenaBytesForProfile = (profile: RuntimeArenaProfile | undefined): number => {
  switch (profile) {
    case "minimal":
      return ARENA_MIN_BYTES;
    case "interactive":
      return ARENA_INTERACTIVE_BYTES;
    case "heavy":
      return ARENA_HEAVY_BYTES;
    default:
      return ARENA_DEFAULT_BYTES;
  }
};

const createRuntimeControlBuffer = (capabilities: RuntimeCapabilities): SharedArrayBuffer | ArrayBuffer => {
  if (capabilities.sharedArrayBuffer && typeof SharedArrayBuffer !== "undefined") {
    return new SharedArrayBuffer(BUFFER_TOTAL_BYTES);
  }
  return new ArrayBuffer(BUFFER_TOTAL_BYTES);
};

export const clampRuntimeArenaBytes = (bytes: number): number => {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return ARENA_DEFAULT_BYTES;
  }
  return Math.min(ARENA_MAX_BYTES, Math.max(ARENA_MIN_BYTES, alignToPage(Math.floor(bytes))));
};

export class RuntimeSharedArena {
  readonly buffer: SharedArrayBuffer;
  private readonly header: Int32Array;
  private readonly epochs: Int32Array;
  private readonly bytes: Uint8Array;
  private readonly view: DataView;

  static create(options: Pick<RuntimeMemoryOptions, "arenaBytes" | "arenaProfile"> = {}): RuntimeSharedArena {
    if (typeof SharedArrayBuffer === "undefined") {
      throw new Error("SharedArrayBuffer is unavailable; RuntimeSharedArena cannot be created");
    }
    const size = clampRuntimeArenaBytes(options.arenaBytes ?? arenaBytesForProfile(options.arenaProfile));
    return new RuntimeSharedArena(new SharedArrayBuffer(size));
  }

  constructor(buffer: SharedArrayBuffer) {
    if (buffer.byteLength < ARENA_MIN_BYTES) {
      throw new Error(`runtime shared arena too small: ${buffer.byteLength} < ${ARENA_MIN_BYTES}`);
    }
    this.buffer = buffer;
    this.header = new Int32Array(buffer, 0, 8);
    this.epochs = new Int32Array(buffer, ARENA_OFFSET_EPOCHS, 64);
    this.bytes = new Uint8Array(buffer);
    this.view = new DataView(buffer);
    this.initialize();
  }

  initialize(): void {
    this.header[ARENA_HEADER_IDX_MAGIC] = ARENA_HEADER_MAGIC;
    this.header[ARENA_HEADER_IDX_SCHEMA_VERSION] = ARENA_SCHEMA_VERSION;
    this.header[ARENA_HEADER_IDX_CAPACITY_BYTES] = this.buffer.byteLength;
    this.header[ARENA_HEADER_IDX_ALLOCATED_BYTES] = ARENA_OFFSET_PAGES;
    this.header[ARENA_HEADER_IDX_DESCRIPTOR_COUNT] = ARENA_DESCRIPTOR_COUNT;
    this.header[ARENA_HEADER_IDX_QUEUE_DROPPED] = 0;
    Atomics.store(this.epochs, ARENA_IDX_ALLOC_HEAD, ARENA_OFFSET_PAGES);
    Atomics.store(this.epochs, ARENA_IDX_QUEUE_HEAD, 0);
    Atomics.store(this.epochs, ARENA_IDX_QUEUE_TAIL, 0);
    Atomics.store(this.epochs, ARENA_IDX_READY, 1);
  }

  capacity(): number {
    return this.buffer.byteLength;
  }

  epochView(): Int32Array {
    return this.epochs;
  }

  allocate(length: number, type: RuntimeDescriptorType = ARENA_DESCRIPTOR_TYPE_BYTES, flags = 0): RuntimeArenaDescriptor {
    if (!Number.isFinite(length) || length <= 0) {
      throw new Error(`invalid runtime arena allocation length: ${length}`);
    }
    const capacity = alignToPage(length);
    const offset = Atomics.add(this.epochs, ARENA_IDX_ALLOC_HEAD, capacity);
    if (offset + capacity > this.buffer.byteLength) {
      Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
      throw new Error(`runtime shared arena capacity exceeded: ${offset + capacity} > ${this.buffer.byteLength}`);
    }

    const id = this.reserveDescriptor();
    const descriptor: RuntimeArenaDescriptor = {
      id,
      state: ARENA_DESCRIPTOR_STATE_ALLOCATED,
      offset,
      length: 0,
      capacity,
      type,
      flags,
      producerEpoch: 0,
      consumerEpoch: 0,
    };
    this.writeDescriptor(descriptor);
    this.header[ARENA_HEADER_IDX_ALLOCATED_BYTES] = offset + capacity;
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
    return descriptor;
  }

  writeSlab(descriptorId: number, data: Uint8Array): RuntimeArenaDescriptor {
    const descriptor = this.readDescriptor(descriptorId);
    if (descriptor.state === ARENA_DESCRIPTOR_STATE_FREE) {
      throw new Error(`runtime arena descriptor ${descriptorId} is free`);
    }
    if (data.byteLength > descriptor.capacity) {
      throw new Error(`runtime arena slab too small: ${data.byteLength} > ${descriptor.capacity}`);
    }
    this.bytes.set(data, descriptor.offset);
    const next = {
      ...descriptor,
      state: ARENA_DESCRIPTOR_STATE_READY as RuntimeDescriptorState,
      length: data.byteLength,
      producerEpoch: descriptor.producerEpoch + 1,
    };
    this.writeDescriptor(next);
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
    return next;
  }

  readSlab(descriptorId: number): Uint8Array {
    const descriptor = this.readDescriptor(descriptorId);
    if (descriptor.length < 0 || descriptor.length > descriptor.capacity) {
      throw new Error(`runtime arena descriptor ${descriptorId} has invalid length ${descriptor.length}`);
    }
    return this.bytes.slice(descriptor.offset, descriptor.offset + descriptor.length);
  }

  markConsumed(descriptorId: number): RuntimeArenaDescriptor {
    const descriptor = this.readDescriptor(descriptorId);
    const next = {
      ...descriptor,
      state: ARENA_DESCRIPTOR_STATE_CONSUMED as RuntimeDescriptorState,
      consumerEpoch: descriptor.consumerEpoch + 1,
    };
    this.writeDescriptor(next);
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
    return next;
  }

  enqueue(entry: Omit<RuntimeArenaQueueEntry, "epoch">): boolean {
    const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
    const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
    if (tail - head >= ARENA_QUEUE_SLOT_COUNT) {
      this.header[ARENA_HEADER_IDX_QUEUE_DROPPED] += 1;
      Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
      return false;
    }
    const epoch = Atomics.add(this.epochs, ARENA_IDX_QUEUE_TAIL, 1) + 1;
    this.writeQueueSlot(tail % ARENA_QUEUE_SLOT_COUNT, { ...entry, epoch });
    Atomics.add(this.epochs, ARENA_IDX_QUEUE_EPOCH, 1);
    return true;
  }

  enqueueDescriptorReady(descriptorId: number, correlationHash = 0): boolean {
    const descriptor = this.readDescriptor(descriptorId);
    return this.enqueue({
      op: ARENA_QUEUE_OP_DESCRIPTOR_READY,
      descriptorId,
      offset: descriptor.offset,
      length: descriptor.length,
      flags: descriptor.flags,
      correlationHash,
    });
  }

  dequeue(): RuntimeArenaQueueEntry | null {
    const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
    const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
    if (head >= tail) {
      return null;
    }
    Atomics.add(this.epochs, ARENA_IDX_QUEUE_HEAD, 1);
    return this.readQueueSlot(head % ARENA_QUEUE_SLOT_COUNT);
  }

  writeDiagnostics(message: string): void {
    const encoded = new TextEncoder().encode(message);
    const view = new Uint8Array(this.buffer, ARENA_OFFSET_DIAGNOSTICS, ARENA_DIAGNOSTIC_BYTES);
    view.fill(0);
    view.set(encoded.slice(0, ARENA_DIAGNOSTIC_BYTES));
    Atomics.add(this.epochs, ARENA_IDX_DIAGNOSTICS_EPOCH, 1);
  }

  readDiagnostics(): string {
    const view = new Uint8Array(this.buffer, ARENA_OFFSET_DIAGNOSTICS, ARENA_DIAGNOSTIC_BYTES);
    const end = view.findIndex((value) => value === 0);
    return new TextDecoder().decode(end >= 0 ? view.slice(0, end) : view);
  }

  readDescriptor(id: number): RuntimeArenaDescriptor {
    this.assertDescriptorId(id);
    const offset = descriptorOffset(id);
    return {
      id,
      state: this.view.getUint32(offset, true) as RuntimeDescriptorState,
      offset: this.view.getUint32(offset + 4, true),
      length: this.view.getUint32(offset + 8, true),
      capacity: this.view.getUint32(offset + 12, true),
      type: this.view.getUint32(offset + 16, true) as RuntimeDescriptorType,
      flags: this.view.getUint32(offset + 20, true),
      producerEpoch: this.view.getUint32(offset + 24, true),
      consumerEpoch: this.view.getUint32(offset + 28, true),
    };
  }

  private reserveDescriptor(): number {
    for (let id = 0; id < ARENA_DESCRIPTOR_COUNT; id += 1) {
      if (this.readDescriptor(id).state === ARENA_DESCRIPTOR_STATE_FREE) {
        return id;
      }
    }
    Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
    throw new Error("runtime shared arena descriptor table is full");
  }

  private writeDescriptor(descriptor: RuntimeArenaDescriptor): void {
    this.assertDescriptorId(descriptor.id);
    const offset = descriptorOffset(descriptor.id);
    this.view.setUint32(offset, descriptor.state, true);
    this.view.setUint32(offset + 4, descriptor.offset, true);
    this.view.setUint32(offset + 8, descriptor.length, true);
    this.view.setUint32(offset + 12, descriptor.capacity, true);
    this.view.setUint32(offset + 16, descriptor.type, true);
    this.view.setUint32(offset + 20, descriptor.flags, true);
    this.view.setUint32(offset + 24, descriptor.producerEpoch, true);
    this.view.setUint32(offset + 28, descriptor.consumerEpoch, true);
  }

  private writeQueueSlot(slot: number, entry: RuntimeArenaQueueEntry): void {
    const offset = queueSlotOffset(slot);
    this.view.setUint32(offset, entry.op, true);
    this.view.setUint32(offset + 4, entry.descriptorId, true);
    this.view.setUint32(offset + 8, entry.offset, true);
    this.view.setUint32(offset + 12, entry.length, true);
    this.view.setUint32(offset + 16, entry.flags, true);
    this.view.setUint32(offset + 20, entry.correlationHash, true);
    this.view.setUint32(offset + 24, entry.epoch, true);
    this.view.setUint32(offset + 28, 0, true);
  }

  private readQueueSlot(slot: number): RuntimeArenaQueueEntry {
    const offset = queueSlotOffset(slot);
    return {
      op: this.view.getUint32(offset, true),
      descriptorId: this.view.getUint32(offset + 4, true),
      offset: this.view.getUint32(offset + 8, true),
      length: this.view.getUint32(offset + 12, true),
      flags: this.view.getUint32(offset + 16, true),
      correlationHash: this.view.getUint32(offset + 20, true),
      epoch: this.view.getUint32(offset + 24, true),
    };
  }

  private assertDescriptorId(id: number): void {
    if (!Number.isInteger(id) || id < 0 || id >= ARENA_DESCRIPTOR_COUNT) {
      throw new Error(`invalid runtime arena descriptor id: ${id}`);
    }
  }
}

export const negotiateRuntimeMemory = (options: RuntimeMemoryOptions = {}): RuntimeMemorySelection => {
  const capabilities = getRuntimeCapabilities();
  const mode = options.sharedMemory ?? "auto";
  const issues: string[] = [];
  const compression = options.compression ?? ["br", "gzip", "deflate", "identity"];

  if (mode === "off") {
    return {
      controlBuffer: createRuntimeControlBuffer(capabilities),
      arena: null,
      capabilities,
      transportOrder: options.transportOrder ?? ["transferable", "postMessage"],
      compression,
      degraded: false,
      issues,
    };
  }

  const canUseArena = capabilities.supportsSharedMemoryRuntime &&
    (!options.requireSharedWasmMemory || capabilities.supportsSharedWasmMemory);

  if (!canUseArena) {
    issues.push(...capabilities.issues.map((issue) => issue.reason));
    if (mode === "required") {
      throw new Error(`runtime shared arena is required but unavailable: ${issues.join("; ")}`);
    }
    return {
      controlBuffer: createRuntimeControlBuffer(capabilities),
      arena: null,
      capabilities,
      transportOrder: options.transportOrder ?? ["transferable", "postMessage"],
      compression,
      degraded: true,
      issues,
    };
  }

  return {
    controlBuffer: createRuntimeControlBuffer(capabilities),
    arena: RuntimeSharedArena.create(options),
    capabilities,
    transportOrder: options.transportOrder ?? ["sab", "transferable", "postMessage"],
    compression,
    degraded: false,
    issues,
  };
};
