import { bench, describe } from "vitest";
import { RuntimeSharedArena } from "./arena";
import { ARENA_HEAVY_BYTES } from "./generated/runtimeBuffer";

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
});
