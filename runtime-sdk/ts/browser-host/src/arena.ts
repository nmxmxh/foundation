import {
  ARENA_DEFAULT_BYTES,
  ARENA_DESCRIPTOR_COUNT,
  ARENA_DESCRIPTOR_SIZE,
  ARENA_DESCRIPTOR_STATE_ALLOCATED,
  ARENA_DESCRIPTOR_STATE_CONSUMED,
  ARENA_DESCRIPTOR_STATE_FREE,
  ARENA_DESCRIPTOR_STATE_READY,
  ARENA_DESCRIPTOR_TYPE_BYTES,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_FIELD,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_OFFSETS,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALIDITY,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES,
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
  COLUMNAR_BATCH_ALIGNMENT_BYTES,
  COLUMNAR_BATCH_HEADER_BYTES,
  COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT,
  COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID,
  COLUMNAR_BATCH_HEADER_IDX_FLAGS,
  COLUMNAR_BATCH_HEADER_IDX_MAGIC,
  COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID,
  COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT,
  COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION,
  COLUMNAR_BATCH_MAGIC,
  COLUMNAR_BATCH_MAX_COLUMNS,
  COLUMNAR_BATCH_SCHEMA_VERSION,
  COLUMNAR_DESCRIPTOR_ID_NONE,
  COLUMNAR_FIELD_DESCRIPTOR_BYTES,
  COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID,
  COLUMNAR_FIELD_IDX_BYTE_WIDTH,
  COLUMNAR_FIELD_IDX_DICTIONARY_ID,
  COLUMNAR_FIELD_IDX_FIELD_ID,
  COLUMNAR_FIELD_IDX_FLAGS,
  COLUMNAR_FIELD_IDX_LENGTH,
  COLUMNAR_FIELD_IDX_LOGICAL_TYPE,
  COLUMNAR_FIELD_IDX_NULL_COUNT,
  COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID,
  COLUMNAR_FIELD_IDX_PHYSICAL_TYPE,
  COLUMNAR_FIELD_IDX_PRECISION,
  COLUMNAR_FIELD_IDX_SCALE,
  COLUMNAR_FIELD_IDX_TIMEZONE_HASH,
  COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID,
  COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID,
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
  | 4
  | typeof ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH
  | typeof ARENA_DESCRIPTOR_TYPE_COLUMNAR_FIELD
  | typeof ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES
  | typeof ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALIDITY
  | typeof ARENA_DESCRIPTOR_TYPE_COLUMNAR_OFFSETS;

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

export type RuntimeArenaBatchItem = {
  data: Uint8Array;
  type?: RuntimeDescriptorType;
  flags?: number;
  correlationHash?: number;
};

export type RuntimeArenaBatchResult = {
  descriptors: RuntimeArenaDescriptor[];
  enqueued: number;
};

export type RuntimeArenaQueueDrain = {
  entries: RuntimeArenaQueueEntry[];
  count: number;
};

export type RuntimeArenaReleaseOptions = {
  force?: boolean;
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

export type RuntimeArenaInvariantSnapshot = {
  capacityBytes: number;
  allocatedBytes: number;
  queueDepth: number;
  queueDropped: number;
  backpressure: number;
  descriptorCount: number;
  invalidDescriptors: number;
};

export type RuntimeColumnarFieldDescriptor = {
  fieldId: number;
  logicalType: number;
  physicalType: number;
  flags?: number;
  length: number;
  nullCount?: number;
  validityDescriptorId?: number;
  offsetsDescriptorId?: number;
  valuesDescriptorId: number;
  auxiliaryDescriptorId?: number;
  byteWidth?: number;
  scale?: number;
  precision?: number;
  timezoneHash?: number;
  dictionaryId?: number;
};

export type RuntimeColumnarBatchDescriptor = {
  schemaVersion: number;
  rowCount: number;
  columnCount: number;
  flags: number;
  metadataDescriptorId: number;
  dictionaryDescriptorId: number;
  fields: RuntimeColumnarFieldDescriptor[];
};

const descriptorOffset = (id: number): number =>
  ARENA_OFFSET_DESCRIPTOR_TABLE + id * ARENA_DESCRIPTOR_SIZE;

const queueSlotOffset = (slot: number): number =>
  ARENA_OFFSET_QUEUE + slot * ARENA_QUEUE_SLOT_SIZE;

const alignToPage = (value: number): number =>
  Math.ceil(value / ARENA_PAGE_BYTES) * ARENA_PAGE_BYTES;

const alignToColumnar = (value: number): number =>
  Math.ceil(value / COLUMNAR_BATCH_ALIGNMENT_BYTES) * COLUMNAR_BATCH_ALIGNMENT_BYTES;

const columnarFieldOffset = (index: number): number =>
  COLUMNAR_BATCH_HEADER_BYTES + index * COLUMNAR_FIELD_DESCRIPTOR_BYTES;

const columnarPayloadBytes = (columnCount: number): number =>
  alignToColumnar(COLUMNAR_BATCH_HEADER_BYTES + columnCount * COLUMNAR_FIELD_DESCRIPTOR_BYTES);

const assertUint32 = (label: string, value: number): void => {
  if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) {
    throw new Error(`${label} must be a uint32: ${value}`);
  }
};

const optionalDescriptorId = (value: number | undefined): number => {
  if (value === undefined || value < 0) {
    return COLUMNAR_DESCRIPTOR_ID_NONE;
  }
  return value;
};

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

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

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
  private readonly descriptorFreeList: number[] = [];

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
    this.descriptorFreeList.length = 0;
    for (let id = ARENA_DESCRIPTOR_COUNT - 1; id >= 0; id -= 1) {
      this.descriptorFreeList.push(id);
    }
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
    const id = this.reserveDescriptor();
    let capacity = alignToPage(length);
    let offset = this.view.getUint32(descriptorOffset(id) + 4, true);
    const reusableCapacity = this.view.getUint32(descriptorOffset(id) + 12, true);
    const canReuseRegion =
      reusableCapacity >= capacity &&
      offset >= ARENA_OFFSET_PAGES &&
      offset + reusableCapacity <= this.buffer.byteLength;
    if (canReuseRegion) {
      capacity = reusableCapacity;
    } else {
      offset = Atomics.add(this.epochs, ARENA_IDX_ALLOC_HEAD, capacity);
      if (offset + capacity > this.buffer.byteLength) {
        Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
        this.descriptorFreeList.push(id);
        throw new Error(`runtime shared arena capacity exceeded: ${offset + capacity} > ${this.buffer.byteLength}`);
      }
      this.header[ARENA_HEADER_IDX_ALLOCATED_BYTES] = offset + capacity;
    }

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
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
    return descriptor;
  }

  writeSlab(descriptorId: number, data: Uint8Array): RuntimeArenaDescriptor {
    this.writeSlabReady(descriptorId, data);
    return this.readDescriptor(descriptorId);
  }

  writeSlabsReady(items: Array<{ descriptorId: number; data: Uint8Array }>): RuntimeArenaDescriptor[] {
    const descriptors: RuntimeArenaDescriptor[] = [];
    for (const item of items) {
      this.writeSlabReady(item.descriptorId, item.data);
      descriptors.push(this.readDescriptor(item.descriptorId));
    }
    return descriptors;
  }

  writeSlabReady(descriptorId: number, data: Uint8Array): void {
    this.assertDescriptorId(descriptorId);
    const descriptorTableOffset = descriptorOffset(descriptorId);
    const state = this.view.getUint32(descriptorTableOffset, true);
    if (state === ARENA_DESCRIPTOR_STATE_FREE) {
      throw new Error(`runtime arena descriptor ${descriptorId} is free`);
    }
    const offset = this.view.getUint32(descriptorTableOffset + 4, true);
    const capacity = this.view.getUint32(descriptorTableOffset + 12, true);
    if (data.byteLength > capacity) {
      throw new Error(`runtime arena slab too small: ${data.byteLength} > ${capacity}`);
    }
    if (offset + data.byteLength > this.buffer.byteLength) {
      throw new Error(`runtime arena descriptor ${descriptorId} exceeds arena capacity`);
    }
    this.bytes.set(data, offset);
    this.view.setUint32(descriptorTableOffset, ARENA_DESCRIPTOR_STATE_READY, true);
    this.view.setUint32(descriptorTableOffset + 8, data.byteLength, true);
    this.view.setUint32(
      descriptorTableOffset + 24,
      this.view.getUint32(descriptorTableOffset + 24, true) + 1,
      true
    );
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
  }

  readSlab(descriptorId: number): Uint8Array {
    return this.readSlabView(descriptorId).slice();
  }

  readSlabView(descriptorId: number): Uint8Array {
    this.assertDescriptorId(descriptorId);
    const descriptorTableOffset = descriptorOffset(descriptorId);
    const offset = this.view.getUint32(descriptorTableOffset + 4, true);
    const length = this.view.getUint32(descriptorTableOffset + 8, true);
    const capacity = this.view.getUint32(descriptorTableOffset + 12, true);
    if (length > capacity || offset + length > this.buffer.byteLength) {
      throw new Error(`runtime arena descriptor ${descriptorId} has invalid length ${length}`);
    }
    return this.bytes.subarray(offset, offset + length);
  }

  markConsumed(descriptorId: number): RuntimeArenaDescriptor {
    this.markConsumedById(descriptorId);
    return this.readDescriptor(descriptorId);
  }

  markConsumedById(descriptorId: number): void {
    this.assertDescriptorId(descriptorId);
    const descriptorTableOffset = descriptorOffset(descriptorId);
    this.view.setUint32(descriptorTableOffset, ARENA_DESCRIPTOR_STATE_CONSUMED, true);
    this.view.setUint32(
      descriptorTableOffset + 28,
      this.view.getUint32(descriptorTableOffset + 28, true) + 1,
      true
    );
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
  }

  releaseDescriptor(descriptorId: number, options: RuntimeArenaReleaseOptions = {}): RuntimeArenaDescriptor {
    this.releaseDescriptorById(descriptorId, options);
    return this.readDescriptor(descriptorId);
  }

  releaseDescriptorById(descriptorId: number, options: RuntimeArenaReleaseOptions = {}): void {
    this.assertDescriptorId(descriptorId);
    const descriptorTableOffset = descriptorOffset(descriptorId);
    const state = this.view.getUint32(descriptorTableOffset, true);
    if (state === ARENA_DESCRIPTOR_STATE_FREE) {
      return;
    }
    if (!options.force && state === ARENA_DESCRIPTOR_STATE_READY) {
      throw new Error(`runtime arena descriptor ${descriptorId} is ready and must be consumed before release`);
    }
    this.view.setUint32(descriptorTableOffset, ARENA_DESCRIPTOR_STATE_FREE, true);
    this.view.setUint32(descriptorTableOffset + 8, 0, true);
    this.view.setUint32(descriptorTableOffset + 20, 0, true);
    this.view.setUint32(
      descriptorTableOffset + 28,
      this.view.getUint32(descriptorTableOffset + 28, true) + 1,
      true
    );
    this.descriptorFreeList.push(descriptorId);
    Atomics.add(this.epochs, ARENA_IDX_DESCRIPTOR_EPOCH, 1);
  }

  releaseDescriptors(descriptorIds: readonly number[], options: RuntimeArenaReleaseOptions = {}): number {
    let released = 0;
    for (const descriptorId of descriptorIds) {
      const before = this.view.getUint32(descriptorOffset(descriptorId), true);
      this.releaseDescriptorById(descriptorId, options);
      if (before !== ARENA_DESCRIPTOR_STATE_FREE) {
        released += 1;
      }
    }
    return released;
  }

  enqueue(entry: Omit<RuntimeArenaQueueEntry, "epoch">): boolean {
    while (true) {
      const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
      const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
      if (tail - head >= ARENA_QUEUE_SLOT_COUNT) {
        Atomics.add(this.header, ARENA_HEADER_IDX_QUEUE_DROPPED, 1);
        Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
        return false;
      }
      if (Atomics.compareExchange(this.epochs, ARENA_IDX_QUEUE_TAIL, tail, tail + 1) !== tail) {
        continue;
      }
      const epoch = tail + 1;
      this.writeQueueSlot(tail % ARENA_QUEUE_SLOT_COUNT, { ...entry, epoch });
      Atomics.add(this.epochs, ARENA_IDX_QUEUE_EPOCH, 1);
      return true;
    }
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

  enqueueDescriptorReadyBatch(descriptorIds: readonly number[], correlationHash = 0): number {
    let enqueued = 0;
    for (const descriptorId of descriptorIds) {
      if (!this.enqueueDescriptorReady(descriptorId, correlationHash)) {
        return enqueued;
      }
      enqueued += 1;
    }
    return enqueued;
  }

  enqueueDescriptorReadyBatchFast(descriptorIds: readonly number[], correlationHash = 0): number {
    const count = descriptorIds.length;
    if (count === 0) {
      return 0;
    }
    const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
    const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
    const available = ARENA_QUEUE_SLOT_COUNT - (tail - head);
    if (available <= 0) {
      Atomics.add(this.header, ARENA_HEADER_IDX_QUEUE_DROPPED, count);
      Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
      return 0;
    }
    const accepted = Math.min(count, available);
    if (Atomics.compareExchange(this.epochs, ARENA_IDX_QUEUE_TAIL, tail, tail + accepted) !== tail) {
      return this.enqueueDescriptorReadyBatch(descriptorIds, correlationHash);
    }
    for (let index = 0; index < accepted; index += 1) {
      this.writeDescriptorReadyQueueSlot((tail + index) % ARENA_QUEUE_SLOT_COUNT, descriptorIds[index], correlationHash, tail + index + 1);
    }
    Atomics.add(this.epochs, ARENA_IDX_QUEUE_EPOCH, accepted);
    if (accepted < count) {
      Atomics.add(this.header, ARENA_HEADER_IDX_QUEUE_DROPPED, count - accepted);
      Atomics.add(this.epochs, ARENA_IDX_BACKPRESSURE, 1);
    }
    return accepted;
  }

  allocateWriteReadyBatch(items: readonly RuntimeArenaBatchItem[]): RuntimeArenaBatchResult {
    const descriptors: RuntimeArenaDescriptor[] = [];
    for (const item of items) {
      const descriptor = this.allocate(item.data.byteLength, item.type ?? ARENA_DESCRIPTOR_TYPE_BYTES, item.flags ?? 0);
      this.writeSlabReady(descriptor.id, item.data);
      descriptors.push(this.readDescriptor(descriptor.id));
    }
    let enqueued = 0;
    for (let index = 0; index < descriptors.length; index += 1) {
      const descriptor = descriptors[index];
      if (!this.enqueueDescriptorReady(descriptor.id, items[index]?.correlationHash ?? 0)) {
        return { descriptors, enqueued };
      }
      enqueued += 1;
    }
    return { descriptors, enqueued };
  }

  writeColumnarBatchDescriptor(
    fields: readonly RuntimeColumnarFieldDescriptor[],
    options: {
      rowCount: number;
      flags?: number;
      metadataDescriptorId?: number;
      dictionaryDescriptorId?: number;
      correlationHash?: number;
    }
  ): RuntimeArenaDescriptor {
    const payload = encodeRuntimeColumnarBatchDescriptor({
      schemaVersion: COLUMNAR_BATCH_SCHEMA_VERSION,
      rowCount: options.rowCount,
      columnCount: fields.length,
      flags: options.flags ?? 0,
      metadataDescriptorId: optionalDescriptorId(options.metadataDescriptorId),
      dictionaryDescriptorId: optionalDescriptorId(options.dictionaryDescriptorId),
      fields: [...fields],
    });
    const descriptor = this.allocate(payload.byteLength, ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH, options.flags ?? 0);
    this.writeSlabReady(descriptor.id, payload);
    this.enqueueDescriptorReady(descriptor.id, options.correlationHash ?? 0);
    return this.readDescriptor(descriptor.id);
  }

  readColumnarBatchDescriptor(descriptorId: number): RuntimeColumnarBatchDescriptor {
    const descriptor = this.readDescriptor(descriptorId);
    if (descriptor.type !== ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH) {
      throw new Error(`runtime arena descriptor ${descriptorId} is not a columnar batch`);
    }
    return decodeRuntimeColumnarBatchDescriptor(this.readSlabView(descriptorId));
  }

  dequeue(): RuntimeArenaQueueEntry | null {
    while (true) {
      const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
      const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
      if (head >= tail) {
        return null;
      }
      if (Atomics.compareExchange(this.epochs, ARENA_IDX_QUEUE_HEAD, head, head + 1) !== head) {
        continue;
      }
      return this.readQueueSlot(head % ARENA_QUEUE_SLOT_COUNT);
    }
  }

  dequeueBatch(limit: number): RuntimeArenaQueueEntry[] {
    const max = Math.max(0, Math.floor(limit));
    if (max === 0) {
      return [];
    }
    const entries: RuntimeArenaQueueEntry[] = [];
    while (entries.length < max) {
      const entry = this.dequeue();
      if (!entry) {
        break;
      }
      entries.push(entry);
    }
    return entries;
  }

  dequeueBatchInto(limit: number, target: RuntimeArenaQueueEntry[]): RuntimeArenaQueueDrain {
    const max = Math.max(0, Math.floor(limit));
    target.length = 0;
    if (max === 0) {
      return { entries: target, count: 0 };
    }
    while (target.length < max) {
      const entry = this.dequeue();
      if (!entry) {
        break;
      }
      target.push(entry);
    }
    return { entries: target, count: target.length };
  }

  dequeueBatchFast(limit: number, target: RuntimeArenaQueueEntry[] = []): RuntimeArenaQueueDrain {
    const max = Math.max(0, Math.floor(limit));
    target.length = 0;
    if (max === 0) {
      return { entries: target, count: 0 };
    }
    const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
    const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
    const available = tail - head;
    if (available <= 0) {
      return { entries: target, count: 0 };
    }
    const accepted = Math.min(max, available);
    if (Atomics.compareExchange(this.epochs, ARENA_IDX_QUEUE_HEAD, head, head + accepted) !== head) {
      return this.dequeueBatchInto(max, target);
    }
    for (let index = 0; index < accepted; index += 1) {
      target.push(this.readQueueSlot((head + index) % ARENA_QUEUE_SLOT_COUNT));
    }
    return { entries: target, count: accepted };
  }

  invariantSnapshot(): RuntimeArenaInvariantSnapshot {
    const head = Atomics.load(this.epochs, ARENA_IDX_QUEUE_HEAD);
    const tail = Atomics.load(this.epochs, ARENA_IDX_QUEUE_TAIL);
    let invalidDescriptors = 0;
    for (let id = 0; id < ARENA_DESCRIPTOR_COUNT; id += 1) {
      const descriptor = this.readDescriptor(id);
      const validBounds = descriptor.offset + descriptor.capacity <= this.buffer.byteLength &&
        descriptor.length <= descriptor.capacity;
      const validState =
        descriptor.state === ARENA_DESCRIPTOR_STATE_FREE ||
        descriptor.state === ARENA_DESCRIPTOR_STATE_ALLOCATED ||
        descriptor.state === ARENA_DESCRIPTOR_STATE_READY ||
        descriptor.state === ARENA_DESCRIPTOR_STATE_CONSUMED;
      if (!validBounds || !validState) {
        invalidDescriptors += 1;
      }
    }
    return {
      capacityBytes: this.buffer.byteLength,
      allocatedBytes: Atomics.load(this.header, ARENA_HEADER_IDX_ALLOCATED_BYTES),
      queueDepth: Math.max(0, tail - head),
      queueDropped: Atomics.load(this.header, ARENA_HEADER_IDX_QUEUE_DROPPED),
      backpressure: Atomics.load(this.epochs, ARENA_IDX_BACKPRESSURE),
      descriptorCount: ARENA_DESCRIPTOR_COUNT,
      invalidDescriptors,
    };
  }

  writeDiagnostics(message: string): void {
    const encoded = textEncoder.encode(message);
    const view = new Uint8Array(this.buffer, ARENA_OFFSET_DIAGNOSTICS, ARENA_DIAGNOSTIC_BYTES);
    view.fill(0);
    view.set(encoded.slice(0, ARENA_DIAGNOSTIC_BYTES));
    Atomics.add(this.epochs, ARENA_IDX_DIAGNOSTICS_EPOCH, 1);
  }

  readDiagnostics(): string {
    const view = new Uint8Array(this.buffer, ARENA_OFFSET_DIAGNOSTICS, ARENA_DIAGNOSTIC_BYTES);
    const end = view.findIndex((value) => value === 0);
    return textDecoder.decode(end >= 0 ? view.subarray(0, end) : view);
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
    while (this.descriptorFreeList.length > 0) {
      const id = this.descriptorFreeList.pop() as number;
      if (this.view.getUint32(descriptorOffset(id), true) === ARENA_DESCRIPTOR_STATE_FREE) {
        return id;
      }
    }
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

  private writeDescriptorReadyQueueSlot(slot: number, descriptorId: number, correlationHash: number, epoch: number): void {
    this.assertDescriptorId(descriptorId);
    const descriptorTableOffset = descriptorOffset(descriptorId);
    const offset = queueSlotOffset(slot);
    this.view.setUint32(offset, ARENA_QUEUE_OP_DESCRIPTOR_READY, true);
    this.view.setUint32(offset + 4, descriptorId, true);
    this.view.setUint32(offset + 8, this.view.getUint32(descriptorTableOffset + 4, true), true);
    this.view.setUint32(offset + 12, this.view.getUint32(descriptorTableOffset + 8, true), true);
    this.view.setUint32(offset + 16, this.view.getUint32(descriptorTableOffset + 20, true), true);
    this.view.setUint32(offset + 20, correlationHash, true);
    this.view.setUint32(offset + 24, epoch, true);
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

export const encodeRuntimeColumnarBatchDescriptor = (
  batch: RuntimeColumnarBatchDescriptor
): Uint8Array => {
  if (batch.schemaVersion !== COLUMNAR_BATCH_SCHEMA_VERSION) {
    throw new Error(`unsupported columnar batch schema version: ${batch.schemaVersion}`);
  }
  assertUint32("rowCount", batch.rowCount);
  assertUint32("flags", batch.flags);
  assertUint32("metadataDescriptorId", batch.metadataDescriptorId);
  assertUint32("dictionaryDescriptorId", batch.dictionaryDescriptorId);
  if (batch.columnCount !== batch.fields.length) {
    throw new Error(`columnCount ${batch.columnCount} does not match fields ${batch.fields.length}`);
  }
  if (batch.columnCount <= 0 || batch.columnCount > COLUMNAR_BATCH_MAX_COLUMNS) {
    throw new Error(`invalid columnar batch column count: ${batch.columnCount}`);
  }
  const payload = new Uint8Array(columnarPayloadBytes(batch.columnCount));
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_MAGIC * 4, COLUMNAR_BATCH_MAGIC, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION * 4, batch.schemaVersion, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT * 4, batch.rowCount, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT * 4, batch.columnCount, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_FLAGS * 4, batch.flags, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID * 4, batch.metadataDescriptorId, true);
  view.setUint32(COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID * 4, batch.dictionaryDescriptorId, true);
  batch.fields.forEach((field, index) => {
    writeColumnarField(view, index, field, batch.rowCount);
  });
  return payload;
};

export const decodeRuntimeColumnarBatchDescriptor = (
  payload: Uint8Array
): RuntimeColumnarBatchDescriptor => {
  if (payload.byteLength < COLUMNAR_BATCH_HEADER_BYTES) {
    throw new Error(`columnar batch descriptor too small: ${payload.byteLength}`);
  }
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  const magic = view.getUint32(COLUMNAR_BATCH_HEADER_IDX_MAGIC * 4, true);
  if (magic !== COLUMNAR_BATCH_MAGIC) {
    throw new Error(`invalid columnar batch magic: ${magic}`);
  }
  const schemaVersion = view.getUint32(COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION * 4, true);
  if (schemaVersion !== COLUMNAR_BATCH_SCHEMA_VERSION) {
    throw new Error(`unsupported columnar batch schema version: ${schemaVersion}`);
  }
  const rowCount = view.getUint32(COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT * 4, true);
  const columnCount = view.getUint32(COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT * 4, true);
  if (columnCount <= 0 || columnCount > COLUMNAR_BATCH_MAX_COLUMNS) {
    throw new Error(`invalid columnar batch column count: ${columnCount}`);
  }
  const requiredBytes = columnarPayloadBytes(columnCount);
  if (payload.byteLength < requiredBytes) {
    throw new Error(`columnar batch descriptor truncated: ${payload.byteLength} < ${requiredBytes}`);
  }
  const fields: RuntimeColumnarFieldDescriptor[] = [];
  for (let index = 0; index < columnCount; index += 1) {
    fields.push(readColumnarField(view, index, rowCount));
  }
  return {
    schemaVersion,
    rowCount,
    columnCount,
    flags: view.getUint32(COLUMNAR_BATCH_HEADER_IDX_FLAGS * 4, true),
    metadataDescriptorId: view.getUint32(COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID * 4, true),
    dictionaryDescriptorId: view.getUint32(COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID * 4, true),
    fields,
  };
};

const writeColumnarField = (
  view: DataView,
  index: number,
  field: RuntimeColumnarFieldDescriptor,
  rowCount: number
): void => {
  const values = [
    ["fieldId", field.fieldId],
    ["logicalType", field.logicalType],
    ["physicalType", field.physicalType],
    ["flags", field.flags ?? 0],
    ["length", field.length],
    ["nullCount", field.nullCount ?? 0],
    ["validityDescriptorId", optionalDescriptorId(field.validityDescriptorId)],
    ["offsetsDescriptorId", optionalDescriptorId(field.offsetsDescriptorId)],
    ["valuesDescriptorId", field.valuesDescriptorId],
    ["auxiliaryDescriptorId", optionalDescriptorId(field.auxiliaryDescriptorId)],
    ["byteWidth", field.byteWidth ?? 0],
    ["scale", field.scale ?? 0],
    ["precision", field.precision ?? 0],
    ["timezoneHash", field.timezoneHash ?? 0],
    ["dictionaryId", field.dictionaryId ?? COLUMNAR_DESCRIPTOR_ID_NONE],
  ] as const;
  for (const [label, value] of values) {
    assertUint32(label, value);
  }
  if (field.length !== rowCount) {
    throw new Error(`columnar field ${field.fieldId} length ${field.length} does not match row count ${rowCount}`);
  }
  if ((field.nullCount ?? 0) > field.length) {
    throw new Error(`columnar field ${field.fieldId} null count exceeds length`);
  }
  const offset = columnarFieldOffset(index);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_FIELD_ID * 4, field.fieldId, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_LOGICAL_TYPE * 4, field.logicalType, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_PHYSICAL_TYPE * 4, field.physicalType, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_FLAGS * 4, field.flags ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_LENGTH * 4, field.length, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_NULL_COUNT * 4, field.nullCount ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID * 4, optionalDescriptorId(field.validityDescriptorId), true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID * 4, optionalDescriptorId(field.offsetsDescriptorId), true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID * 4, field.valuesDescriptorId, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID * 4, optionalDescriptorId(field.auxiliaryDescriptorId), true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_BYTE_WIDTH * 4, field.byteWidth ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_SCALE * 4, field.scale ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_PRECISION * 4, field.precision ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_TIMEZONE_HASH * 4, field.timezoneHash ?? 0, true);
  view.setUint32(offset + COLUMNAR_FIELD_IDX_DICTIONARY_ID * 4, field.dictionaryId ?? COLUMNAR_DESCRIPTOR_ID_NONE, true);
};

const readColumnarField = (
  view: DataView,
  index: number,
  rowCount: number
): RuntimeColumnarFieldDescriptor => {
  const offset = columnarFieldOffset(index);
  const field: RuntimeColumnarFieldDescriptor = {
    fieldId: view.getUint32(offset + COLUMNAR_FIELD_IDX_FIELD_ID * 4, true),
    logicalType: view.getUint32(offset + COLUMNAR_FIELD_IDX_LOGICAL_TYPE * 4, true),
    physicalType: view.getUint32(offset + COLUMNAR_FIELD_IDX_PHYSICAL_TYPE * 4, true),
    flags: view.getUint32(offset + COLUMNAR_FIELD_IDX_FLAGS * 4, true),
    length: view.getUint32(offset + COLUMNAR_FIELD_IDX_LENGTH * 4, true),
    nullCount: view.getUint32(offset + COLUMNAR_FIELD_IDX_NULL_COUNT * 4, true),
    validityDescriptorId: view.getUint32(offset + COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID * 4, true),
    offsetsDescriptorId: view.getUint32(offset + COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID * 4, true),
    valuesDescriptorId: view.getUint32(offset + COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID * 4, true),
    auxiliaryDescriptorId: view.getUint32(offset + COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID * 4, true),
    byteWidth: view.getUint32(offset + COLUMNAR_FIELD_IDX_BYTE_WIDTH * 4, true),
    scale: view.getUint32(offset + COLUMNAR_FIELD_IDX_SCALE * 4, true),
    precision: view.getUint32(offset + COLUMNAR_FIELD_IDX_PRECISION * 4, true),
    timezoneHash: view.getUint32(offset + COLUMNAR_FIELD_IDX_TIMEZONE_HASH * 4, true),
    dictionaryId: view.getUint32(offset + COLUMNAR_FIELD_IDX_DICTIONARY_ID * 4, true),
  };
  if (field.length !== rowCount) {
    throw new Error(`columnar field ${field.fieldId} length ${field.length} does not match row count ${rowCount}`);
  }
  if ((field.nullCount ?? 0) > field.length) {
    throw new Error(`columnar field ${field.fieldId} null count exceeds length`);
  }
  return field;
};

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
