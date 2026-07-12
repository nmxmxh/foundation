import type { RuntimeExecutionLane } from "./lanePlanner";

export type RuntimePacketRingState = "free" | "rx-ready" | "processing" | "tx-ready";

export type RuntimeLaneTimestampSource = "monotonic" | "software" | "hardware";

export type RuntimeLaneTimestamps = {
  source: RuntimeLaneTimestampSource;
  ingressNs: bigint;
  dequeuedNs: bigint;
  processedNs: bigint;
  emittedNs: bigint;
};

export type RuntimePacketDescriptor = {
  id: number;
  state: RuntimePacketRingState;
  offset: number;
  length: number;
  capacity: number;
  flags: number;
  lane: RuntimeExecutionLane | "packet-ring";
  timestamps: RuntimeLaneTimestamps;
};

export type RuntimePacketRingCounters = {
  enqueued: number;
  dequeued: number;
  completed: number;
  released: number;
  dropped: number;
  highWaterDepth: number;
};

export type RuntimePacketRingDrain = {
  descriptors: RuntimePacketDescriptor[];
  count: number;
};

export type RuntimePacketRingIdDrain = {
  descriptorIds: number[];
  count: number;
};

export type RuntimePacketRingOptions = {
  slots: number;
  slotBytes: number;
  timestampSource?: RuntimeLaneTimestampSource;
  nowNs?: () => bigint;
};

const defaultNowNs = (): bigint => {
  if (typeof performance !== "undefined" && typeof performance.now === "function") {
    return BigInt(Math.floor(performance.now() * 1_000_000));
  }
  return BigInt(Date.now()) * 1_000_000n;
};

const zeroTimestamps = (source: RuntimeLaneTimestampSource): RuntimeLaneTimestamps => ({
  source,
  ingressNs: 0n,
  dequeuedNs: 0n,
  processedNs: 0n,
  emittedNs: 0n,
});

const resetTimestamps = (target: RuntimeLaneTimestamps, source: RuntimeLaneTimestampSource): void => {
  target.source = source;
  target.ingressNs = 0n;
  target.dequeuedNs = 0n;
  target.processedNs = 0n;
  target.emittedNs = 0n;
};

export class RuntimePacketRing {
  private readonly bytes: Uint8Array;
  private readonly descriptors: RuntimePacketDescriptor[];
  private readonly freeList: number[];
  private readonly queue: number[];
  private readonly timestampSource: RuntimeLaneTimestampSource;
  private readonly nowNs: () => bigint;
  private head = 0;
  private tail = 0;
  private readonly stats: RuntimePacketRingCounters = {
    enqueued: 0,
    dequeued: 0,
    completed: 0,
    released: 0,
    dropped: 0,
    highWaterDepth: 0,
  };

  constructor(options: RuntimePacketRingOptions) {
    if (!Number.isInteger(options.slots) || options.slots <= 0) {
      throw new Error(`invalid packet ring slots: ${options.slots}`);
    }
    if (!Number.isInteger(options.slotBytes) || options.slotBytes <= 0) {
      throw new Error(`invalid packet ring slot bytes: ${options.slotBytes}`);
    }
    this.timestampSource = options.timestampSource ?? "monotonic";
    this.nowNs = options.nowNs ?? defaultNowNs;
    this.bytes = new Uint8Array(options.slots * options.slotBytes);
    this.queue = new Array(options.slots);
    this.freeList = [];
    this.descriptors = Array.from({ length: options.slots }, (_, id) => {
      this.freeList.push(options.slots - id - 1);
      return {
        id,
        state: "free",
        offset: id * options.slotBytes,
        length: 0,
        capacity: options.slotBytes,
        flags: 0,
        lane: "packet-ring",
        timestamps: zeroTimestamps(this.timestampSource),
      };
    });
  }

  capacity(): number {
    return this.descriptors.length;
  }

  depth(): number {
    return this.tail - this.head;
  }

  counters(): RuntimePacketRingCounters {
    return { ...this.stats };
  }

  enqueue(payload: Uint8Array, flags = 0, lane: RuntimePacketDescriptor["lane"] = "packet-ring"): RuntimePacketDescriptor | null {
    if (payload.byteLength > this.slotBytes()) {
      this.stats.dropped += 1;
      return null;
    }
    const id = this.freeList.pop();
    if (id === undefined || this.depth() >= this.queue.length) {
      if (id !== undefined) {
        this.freeList.push(id);
      }
      this.stats.dropped += 1;
      return null;
    }
    const descriptor = this.descriptors[id];
    this.bytes.set(payload, descriptor.offset);
    descriptor.state = "rx-ready";
    descriptor.length = payload.byteLength;
    descriptor.flags = flags;
    descriptor.lane = lane;
    resetTimestamps(descriptor.timestamps, this.timestampSource);
    descriptor.timestamps.ingressNs = this.nowNs();
    this.queue[this.tail % this.queue.length] = id;
    this.tail += 1;
    this.stats.enqueued += 1;
    this.stats.highWaterDepth = Math.max(this.stats.highWaterDepth, this.depth());
    return descriptor;
  }

  enqueueBurst(payloads: readonly Uint8Array[], flags = 0, lane: RuntimePacketDescriptor["lane"] = "packet-ring"): number {
    const available = Math.min(this.freeList.length, this.queue.length - this.depth());
    let accepted = Math.min(payloads.length, available);
    for (let index = 0; index < accepted; index += 1) {
      if (payloads[index].byteLength > this.slotBytes()) {
        accepted = index;
        break;
      }
    }
    for (let index = 0; index < accepted; index += 1) {
      const id = this.freeList.pop();
      if (id === undefined) {
        throw new Error("packet ring free-list reservation drift");
      }
      const descriptor = this.descriptors[id];
      this.bytes.set(payloads[index], descriptor.offset);
      descriptor.state = "rx-ready";
      descriptor.length = payloads[index].byteLength;
      descriptor.flags = flags;
      descriptor.lane = lane;
      resetTimestamps(descriptor.timestamps, this.timestampSource);
      descriptor.timestamps.ingressNs = this.nowNs();
      this.queue[(this.tail + index) % this.queue.length] = id;
    }
    this.tail += accepted;
    this.stats.enqueued += accepted;
    if (accepted < payloads.length) {
      this.stats.dropped += 1;
    }
    if (accepted > 0) {
      this.stats.highWaterDepth = Math.max(this.stats.highWaterDepth, this.depth());
    }
    return accepted;
  }

  dequeue(): RuntimePacketDescriptor | null {
    if (this.head >= this.tail) {
      return null;
    }
    const id = this.queue[this.head % this.queue.length];
    this.head += 1;
    const descriptor = this.descriptors[id];
    descriptor.state = "processing";
    descriptor.timestamps.dequeuedNs = this.nowNs();
    this.stats.dequeued += 1;
    return descriptor;
  }

  dequeueBurst(limit: number, target: RuntimePacketDescriptor[] = []): RuntimePacketRingDrain {
    const max = Math.max(0, Math.floor(limit));
    target.length = 0;
    while (target.length < max) {
      const descriptor = this.dequeue();
      if (!descriptor) {
        break;
      }
      target.push(descriptor);
    }
    return { descriptors: target, count: target.length };
  }

  dequeueIdsBurst(limit: number, target: number[] = []): RuntimePacketRingIdDrain {
    const count = this.dequeueIdsBurstInto(limit, target);
    return { descriptorIds: target, count };
  }

  dequeueIdsBurstInto(limit: number, target: number[]): number {
    const max = Math.max(0, Math.floor(limit));
    target.length = 0;
    while (target.length < max && this.head < this.tail) {
      const id = this.queue[this.head % this.queue.length];
      this.head += 1;
      const descriptor = this.descriptors[id];
      descriptor.state = "processing";
      descriptor.timestamps.dequeuedNs = this.nowNs();
      this.stats.dequeued += 1;
      target.push(id);
    }
    return target.length;
  }

  view(descriptorId: number): Uint8Array {
    const descriptor = this.descriptor(descriptorId);
    return this.bytes.subarray(descriptor.offset, descriptor.offset + descriptor.length);
  }

  complete(descriptorId: number): RuntimePacketDescriptor {
    const descriptor = this.descriptor(descriptorId);
    if (descriptor.state !== "processing") {
      throw new Error(`packet descriptor ${descriptorId} is not processing`);
    }
    descriptor.state = "tx-ready";
    descriptor.timestamps.processedNs = this.nowNs();
    this.stats.completed += 1;
    return descriptor;
  }

  release(descriptorId: number): void {
    const descriptor = this.descriptor(descriptorId);
    if (descriptor.state === "free") {
      return;
    }
    descriptor.timestamps.emittedNs = this.nowNs();
    descriptor.state = "free";
    descriptor.length = 0;
    descriptor.flags = 0;
    this.freeList.push(descriptorId);
    this.stats.released += 1;
  }

  private descriptor(id: number): RuntimePacketDescriptor {
    if (!Number.isInteger(id) || id < 0 || id >= this.descriptors.length) {
      throw new Error(`invalid packet descriptor id: ${id}`);
    }
    return this.descriptors[id];
  }

  private slotBytes(): number {
    return this.bytes.byteLength / this.descriptors.length;
  }
}
