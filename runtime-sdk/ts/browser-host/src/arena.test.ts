import { describe, expect, it, vi } from "vitest";
import {
  RuntimeSharedArena,
  clampRuntimeArenaBytes,
  negotiateRuntimeMemory,
  type RuntimeArenaQueueEntry,
} from "./arena";
import {
  ARENA_DEFAULT_BYTES,
  ARENA_DESCRIPTOR_COUNT,
  ARENA_DESCRIPTOR_STATE_CONSUMED,
  ARENA_DESCRIPTOR_STATE_FREE,
  ARENA_DESCRIPTOR_STATE_READY,
  ARENA_HEAVY_BYTES,
  ARENA_MIN_BYTES,
  BUFFER_TOTAL_BYTES,
} from "./generated/runtimeBuffer";
import { getRuntimeCapabilities } from "./pulse/runtimeCaps";

describe("RuntimeSharedArena", () => {
  it("clamps arena sizes to supported page-aligned ranges", () => {
    expect(clampRuntimeArenaBytes(0)).toBe(ARENA_DEFAULT_BYTES);
    expect(clampRuntimeArenaBytes(1)).toBe(ARENA_MIN_BYTES);
    expect(clampRuntimeArenaBytes(ARENA_MIN_BYTES + 1) % 4096).toBe(0);
  });

  it("allocates slabs and publishes descriptor-ready queue entries", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const payload = new TextEncoder().encode("runtime-arena-payload");
    const descriptor = arena.allocate(payload.byteLength);
    const ready = arena.writeSlab(descriptor.id, payload);

    expect(ready.state).toBe(ARENA_DESCRIPTOR_STATE_READY);
    expect(new TextDecoder().decode(arena.readSlab(descriptor.id))).toBe("runtime-arena-payload");
    expect(new TextDecoder().decode(arena.readSlabView(descriptor.id))).toBe("runtime-arena-payload");
    expect(arena.enqueueDescriptorReady(descriptor.id, 42)).toBe(true);

    const entry = arena.dequeue();
    expect(entry?.descriptorId).toBe(descriptor.id);
    expect(entry?.length).toBe(payload.byteLength);

    const consumed = arena.markConsumed(descriptor.id);
    expect(consumed.state).toBe(ARENA_DESCRIPTOR_STATE_CONSUMED);

    arena.writeSlabReady(descriptor.id, payload);
    expect(arena.readDescriptor(descriptor.id).state).toBe(ARENA_DESCRIPTOR_STATE_READY);
    arena.markConsumedById(descriptor.id);
    expect(arena.readDescriptor(descriptor.id).state).toBe(ARENA_DESCRIPTOR_STATE_CONSUMED);

    expect(arena.invariantSnapshot()).toMatchObject({
      capacityBytes: ARENA_MIN_BYTES,
      queueDepth: 0,
      queueDropped: 0,
      invalidDescriptors: 0,
    });
  });

  it("moves 1MB payloads through the shared arena data plane", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 8 * 1024 * 1024 });
    const payload = new Uint8Array(1024 * 1024);
    payload.fill(17);

    const descriptor = arena.allocate(payload.byteLength);
    arena.writeSlab(descriptor.id, payload);

    expect(arena.readSlab(descriptor.id).byteLength).toBe(payload.byteLength);
    expect(arena.enqueueDescriptorReady(descriptor.id)).toBe(true);
    expect(arena.dequeue()?.length).toBe(payload.byteLength);
  });

  it("publishes and drains descriptor-ready entries in batches", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const payloads = Array.from({ length: 8 }, (_, index) => {
      const payload = new Uint8Array(256);
      payload.fill(index + 1);
      return { data: payload, correlationHash: index };
    });

    const result = arena.allocateWriteReadyBatch(payloads);
    expect(result.descriptors).toHaveLength(8);
    expect(result.enqueued).toBe(8);

    const entries = arena.dequeueBatch(4);
    expect(entries).toHaveLength(4);
    expect(entries.map((entry) => entry.descriptorId)).toEqual([0, 1, 2, 3]);

    const remaining = arena.dequeueBatch(16);
    expect(remaining).toHaveLength(4);
    expect(remaining.map((entry) => entry.descriptorId)).toEqual([4, 5, 6, 7]);
    expect(arena.dequeueBatch(1)).toEqual([]);
  });

  it("publishes and drains descriptor-ready entries through fast batch reservations", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const payloads = Array.from({ length: 8 }, (_, index) => {
      const payload = new Uint8Array(128);
      payload.fill(index + 1);
      return { data: payload };
    });
    const result = arena.allocateWriteReadyBatch(payloads);
    arena.dequeueBatch(result.enqueued);
    const ids = result.descriptors.map((descriptor) => descriptor.id);

    expect(arena.enqueueDescriptorReadyBatchFast(ids, 9)).toBe(8);
    const scratch: RuntimeArenaQueueEntry[] = [];
    const drained = arena.dequeueBatchFast(8, scratch);

    expect(drained.count).toBe(8);
    expect(drained.entries).toBe(scratch);
    expect(drained.entries.map((entry) => entry.descriptorId)).toEqual(ids);
    expect(drained.entries.every((entry) => entry.correlationHash === 9)).toBe(true);
  });

  it("reuses released descriptors through a disciplined free-list", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_HEAVY_BYTES });
    const payload = new Uint8Array(64);
    const descriptors = Array.from({ length: ARENA_DESCRIPTOR_COUNT }, () => arena.allocate(payload.byteLength));

    expect(() => arena.allocate(payload.byteLength)).toThrow(/descriptor table is full/);

    const releasedIds = descriptors.slice(0, 4).map((descriptor) => descriptor.id);
    for (const id of releasedIds) {
      arena.markConsumedById(id);
    }
    const allocatedBeforeReuse = arena.invariantSnapshot().allocatedBytes;
    expect(arena.releaseDescriptors(releasedIds)).toBe(4);
    expect(arena.readDescriptor(releasedIds[0]).state).toBe(ARENA_DESCRIPTOR_STATE_FREE);

    const reused = Array.from({ length: 4 }, () => arena.allocate(payload.byteLength).id);
    expect(reused).toEqual([...releasedIds].reverse());
    expect(arena.invariantSnapshot().allocatedBytes).toBe(allocatedBeforeReuse);
  });

  it("guards ready descriptors from accidental release unless forced", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const payload = new Uint8Array(32);
    const descriptor = arena.allocate(payload.byteLength);
    arena.writeSlabReady(descriptor.id, payload);

    expect(() => arena.releaseDescriptor(descriptor.id)).toThrow(/must be consumed before release/);
    expect(arena.readDescriptor(descriptor.id).state).toBe(ARENA_DESCRIPTOR_STATE_READY);

    arena.releaseDescriptor(descriptor.id, { force: true });
    expect(arena.readDescriptor(descriptor.id).state).toBe(ARENA_DESCRIPTOR_STATE_FREE);
  });

  it("returns a 4KB control buffer even when shared arena mode is off", () => {
    const selection = negotiateRuntimeMemory({ sharedMemory: "off" });
    expect(selection.controlBuffer.byteLength).toBe(BUFFER_TOTAL_BYTES);
    expect(selection.arena).toBeNull();
    expect(selection.transportOrder).toEqual(["transferable", "postMessage"]);
  });

  it("falls back to transferable buffers when SharedArrayBuffer is unavailable", () => {
    vi.stubGlobal("SharedArrayBuffer", undefined);
    try {
      const selection = negotiateRuntimeMemory({ sharedMemory: "auto" });
      expect(selection.controlBuffer).toBeInstanceOf(ArrayBuffer);
      expect(selection.arena).toBeNull();
      expect(selection.degraded).toBe(true);
      expect(selection.transportOrder).toEqual(["transferable", "postMessage"]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("probes shared WebAssembly.Memory support explicitly", () => {
    const capabilities = getRuntimeCapabilities();
    expect(typeof capabilities.webAssemblySharedMemory).toBe("boolean");
    expect(typeof capabilities.supportsSharedWasmMemory).toBe("boolean");
  });
});
