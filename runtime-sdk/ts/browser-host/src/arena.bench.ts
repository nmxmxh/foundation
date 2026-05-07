import { bench, describe } from "vitest";
import { RuntimeSharedArena, type RuntimeArenaQueueEntry } from "./arena";
import { ARENA_HEAVY_BYTES } from "./generated/runtimeBuffer";
import { RuntimePacketRing, type RuntimePacketDescriptor } from "./packetRing";

const payload = (bytes: number): Uint8Array => {
  const out = new Uint8Array(bytes);
  for (let index = 0; index < out.byteLength; index += 1) {
    out[index] = index % 251;
  }
  return out;
};

describe("RuntimeSharedArena payload movement", () => {
  for (const size of [4 * 1024, 64 * 1024, 1024 * 1024]) {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const slab = payload(size);
    const descriptor = arena.allocate(slab.byteLength);

    bench(`${size / 1024}KB slab write/read`, () => {
      arena.writeSlab(descriptor.id, slab);
      arena.readSlab(descriptor.id);
      arena.markConsumed(descriptor.id);
    });

    bench(`${size / 1024}KB slab write/read view`, () => {
      arena.writeSlab(descriptor.id, slab);
      arena.readSlabView(descriptor.id);
      arena.markConsumed(descriptor.id);
    });

    bench(`${size / 1024}KB slab fast write/read view`, () => {
      arena.writeSlabReady(descriptor.id, slab);
      arena.readSlabView(descriptor.id);
      arena.markConsumedById(descriptor.id);
    });
  }

  const ringArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
  const ringPayload = payload(4096);
  const ringDescriptors = Array.from({ length: 256 }, () => {
    const descriptor = ringArena.allocate(ringPayload.byteLength);
    ringArena.writeSlab(descriptor.id, ringPayload);
    return descriptor;
  });

  bench("sustained descriptor-ready ring traffic", () => {
    for (let index = 0; index < 4096; index += 1) {
      const descriptor = ringDescriptors[index % ringDescriptors.length];
      ringArena.enqueueDescriptorReady(descriptor.id, index);
      ringArena.dequeue();
    }
  });

  for (const batchSize of [1, 8, 32, 128]) {
    const batchArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const batchPayload = payload(4096);
    const descriptors = Array.from({ length: 256 }, () => {
      const descriptor = batchArena.allocate(batchPayload.byteLength);
      batchArena.writeSlabReady(descriptor.id, batchPayload);
      return descriptor;
    });
    const groups = Array.from({ length: 4096 / batchSize }, (_, groupIndex) =>
      Array.from({ length: batchSize }, (_, offset) => descriptors[(groupIndex * batchSize + offset) % descriptors.length].id)
    );

    bench(`descriptor-ready batch traffic x${batchSize}`, () => {
      for (let index = 0; index < groups.length; index += 1) {
        batchArena.enqueueDescriptorReadyBatch(groups[index], index);
        batchArena.dequeueBatch(batchSize);
      }
    });

    const scratch: RuntimeArenaQueueEntry[] = [];
    bench(`descriptor-ready fast batch traffic x${batchSize}`, () => {
      for (let index = 0; index < groups.length; index += 1) {
        batchArena.enqueueDescriptorReadyBatchFast(groups[index], index);
        batchArena.dequeueBatchFast(batchSize, scratch);
      }
    });
  }

  for (const batchSize of [1, 8, 32, 128]) {
    const batchArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const batchPayload = payload(1024);
    const descriptors = Array.from({ length: batchSize }, () => batchArena.allocate(batchPayload.byteLength));
    const ids = descriptors.map((descriptor) => descriptor.id);
    const writes = descriptors.map((descriptor) => ({ descriptorId: descriptor.id, data: batchPayload }));

    bench(`preallocated write/enqueue/dequeue batch x${batchSize}`, () => {
      batchArena.writeSlabsReady(writes);
      batchArena.enqueueDescriptorReadyBatch(ids);
      batchArena.dequeueBatch(batchSize);
    });
  }

  for (const batchSize of [1, 8, 32, 128]) {
    const lifecycleArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const batchPayload = payload(1024);
    const descriptors = Array.from({ length: batchSize }, () => lifecycleArena.allocate(batchPayload.byteLength));
    const ids = descriptors.map((descriptor) => descriptor.id);

    bench(`descriptor release/reallocate free-list x${batchSize}`, () => {
      lifecycleArena.releaseDescriptors(ids, { force: true });
      for (let index = 0; index < ids.length; index += 1) {
        ids[index] = lifecycleArena.allocate(batchPayload.byteLength).id;
      }
    });
  }

  for (const batchSize of [1, 8, 32, 128]) {
    const packetRing = new RuntimePacketRing({ slots: 512, slotBytes: 1024 });
    const packetPayload = payload(256);
    const packets = Array.from({ length: batchSize }, () => packetPayload);
    const scratch: RuntimePacketDescriptor[] = [];

    bench(`packet-ring enqueue/dequeue/complete/release x${batchSize}`, () => {
      packetRing.enqueueBurst(packets, 0, "packet-ring");
      const drained = packetRing.dequeueBurst(batchSize, scratch);
      for (let index = 0; index < drained.count; index += 1) {
        const descriptor = drained.descriptors[index];
        packetRing.view(descriptor.id);
        packetRing.complete(descriptor.id);
        packetRing.release(descriptor.id);
      }
    });
  }
});
