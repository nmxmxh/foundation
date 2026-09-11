import { test, type BenchRegistration } from "vitest";
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

// One test per group: vitest 5 applies the test timeout to a whole
// bench.compare, so a single test holding every registration times out.
for (const size of [4 * 1024, 64 * 1024, 1024 * 1024]) {
  test(`RuntimeSharedArena slab movement ${size / 1024}KB`, async ({ bench }) => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const slab = payload(size);
    const descriptor = arena.allocate(slab.byteLength);

    await bench.compare(
      bench(`${size / 1024}KB slab write/read`, () => {
        arena.writeSlab(descriptor.id, slab);
        arena.readSlab(descriptor.id);
        arena.markConsumed(descriptor.id);
      }),

      bench(`${size / 1024}KB slab write/read view`, () => {
        arena.writeSlab(descriptor.id, slab);
        arena.readSlabView(descriptor.id);
        arena.markConsumed(descriptor.id);
      }),

      bench(`${size / 1024}KB slab fast write/read view`, () => {
        arena.writeSlabReady(descriptor.id, slab);
        arena.readSlabView(descriptor.id);
        arena.markConsumedById(descriptor.id);
      }),
    );
  });
}

test("RuntimeSharedArena sustained ring traffic", async ({ bench }) => {
  const ringArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
  const ringPayload = payload(4096);
  const ringDescriptors = Array.from({ length: 256 }, () => {
    const descriptor = ringArena.allocate(ringPayload.byteLength);
    ringArena.writeSlab(descriptor.id, ringPayload);
    return descriptor;
  });

  await bench("sustained descriptor-ready ring traffic", () => {
    for (let index = 0; index < 4096; index += 1) {
      const descriptor = ringDescriptors[index % ringDescriptors.length];
      ringArena.enqueueDescriptorReady(descriptor.id, index);
      ringArena.dequeue();
    }
  }).run();
});

for (const batchSize of [1, 8, 32, 128]) {
  test(`RuntimeSharedArena descriptor-ready batch traffic x${batchSize}`, async ({ bench }) => {
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
    const scratch: RuntimeArenaQueueEntry[] = [];
    const idScratch: number[] = [];

    await bench.compare(
      bench(`descriptor-ready batch traffic x${batchSize}`, () => {
        for (let index = 0; index < groups.length; index += 1) {
          batchArena.enqueueDescriptorReadyBatch(groups[index], index);
          batchArena.dequeueBatch(batchSize);
        }
      }),

      bench(`descriptor-ready fast batch traffic x${batchSize}`, () => {
        for (let index = 0; index < groups.length; index += 1) {
          batchArena.enqueueDescriptorReadyBatchFast(groups[index], index);
          batchArena.dequeueBatchFast(batchSize, scratch);
        }
      }),

      bench(`descriptor-ready id batch traffic x${batchSize}`, () => {
        for (let index = 0; index < groups.length; index += 1) {
          batchArena.enqueueDescriptorReadyBatchFast(groups[index], index);
          batchArena.dequeueDescriptorReadyIdsFast(batchSize, idScratch);
        }
      }),
    );
  });
}

test("RuntimeSharedArena preallocated write/enqueue/dequeue", async ({ bench }) => {
  const registrations: BenchRegistration<string>[] = [];

  for (const batchSize of [1, 8, 32, 128]) {
    const batchArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const batchPayload = payload(1024);
    const descriptors = Array.from({ length: batchSize }, () => batchArena.allocate(batchPayload.byteLength));
    const ids = descriptors.map((descriptor) => descriptor.id);
    const writes = descriptors.map((descriptor) => ({ descriptorId: descriptor.id, data: batchPayload }));

    registrations.push(bench(`preallocated write/enqueue/dequeue batch x${batchSize}`, () => {
      batchArena.writeSlabsReady(writes);
      batchArena.enqueueDescriptorReadyBatch(ids);
      batchArena.dequeueBatch(batchSize);
    }));
  }

  await bench.compare(...registrations);
});

test("RuntimeSharedArena descriptor release/reallocate", async ({ bench }) => {
  const registrations: BenchRegistration<string>[] = [];

  for (const batchSize of [1, 8, 32, 128]) {
    const lifecycleArena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const batchPayload = payload(1024);
    const descriptors = Array.from({ length: batchSize }, () => lifecycleArena.allocate(batchPayload.byteLength));
    const ids = descriptors.map((descriptor) => descriptor.id);

    registrations.push(bench(`descriptor release/reallocate free-list x${batchSize}`, () => {
      lifecycleArena.releaseDescriptors(ids, { force: true });
      for (let index = 0; index < ids.length; index += 1) {
        ids[index] = lifecycleArena.allocate(batchPayload.byteLength).id;
      }
    }));
  }

  await bench.compare(...registrations);
});

for (const batchSize of [1, 8, 32, 128]) {
  test(`RuntimePacketRing burst traffic x${batchSize}`, async ({ bench }) => {
    const packetRing = new RuntimePacketRing({ slots: 512, slotBytes: 1024 });
    const packetRingIds = new RuntimePacketRing({ slots: 512, slotBytes: 1024 });
    const packetPayload = payload(256);
    const packets = Array.from({ length: batchSize }, () => packetPayload);
    const scratch: RuntimePacketDescriptor[] = [];
    const idScratch: number[] = [];

    await bench.compare(
      bench(`packet-ring enqueue/dequeue/complete/release x${batchSize}`, () => {
        packetRing.enqueueBurst(packets, 0, "packet-ring");
        const drained = packetRing.dequeueBurst(batchSize, scratch);
        for (let index = 0; index < drained.count; index += 1) {
          const descriptor = drained.descriptors[index];
          packetRing.view(descriptor.id);
          packetRing.complete(descriptor.id);
          packetRing.release(descriptor.id);
        }
      }),

      bench(`packet-ring id enqueue/dequeue/complete/release x${batchSize}`, () => {
        packetRingIds.enqueueBurst(packets, 0, "packet-ring");
        const count = packetRingIds.dequeueIdsBurstInto(batchSize, idScratch);
        for (let index = 0; index < count; index += 1) {
          const descriptorId = idScratch[index];
          packetRingIds.view(descriptorId);
          packetRingIds.complete(descriptorId);
          packetRingIds.release(descriptorId);
        }
      }),
    );
  });
}
