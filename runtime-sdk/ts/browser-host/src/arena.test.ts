import { describe, expect, it, vi } from "vitest";
import {
  RuntimeSharedArena,
  clampRuntimeArenaBytes,
  decodeRuntimeColumnarBatchDescriptor,
  encodeRuntimeColumnarBatchDescriptor,
  negotiateRuntimeMemory,
  type RuntimeArenaQueueEntry,
} from "./arena";
import {
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_OFFSETS,
  ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES,
  COLUMNAR_BATCH_ALIGNMENT_BYTES,
  COLUMNAR_BATCH_SCHEMA_VERSION,
  COLUMNAR_DESCRIPTOR_ID_NONE,
  COLUMNAR_FIELD_FLAG_NULLABLE,
  COLUMNAR_LOGICAL_TYPE_INT,
  COLUMNAR_LOGICAL_TYPE_UTF8,
  COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH,
  COLUMNAR_PHYSICAL_TYPE_VARIABLE_BINARY,
  ARENA_DEFAULT_BYTES,
  ARENA_DESCRIPTOR_COUNT,
  ARENA_DESCRIPTOR_STATE_CONSUMED,
  ARENA_DESCRIPTOR_STATE_FREE,
  ARENA_DESCRIPTOR_STATE_READY,
  ARENA_HEAVY_BYTES,
  ARENA_INTERACTIVE_BYTES,
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

  it("selects arena profiles and rejects invalid allocation boundaries", () => {
    expect(RuntimeSharedArena.create({ arenaProfile: "minimal" }).capacity()).toBe(ARENA_MIN_BYTES);
    expect(RuntimeSharedArena.create({ arenaProfile: "interactive" }).capacity()).toBe(ARENA_INTERACTIVE_BYTES);
    expect(RuntimeSharedArena.create({ arenaProfile: "heavy" }).capacity()).toBe(ARENA_HEAVY_BYTES);
    expect(RuntimeSharedArena.create().capacity()).toBe(ARENA_DEFAULT_BYTES);
    expect(() => new RuntimeSharedArena(new SharedArrayBuffer(4096))).toThrow("too small");
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    expect(arena.epochView()).toBeInstanceOf(Int32Array);
    expect(() => arena.allocate(0)).toThrow("invalid runtime arena allocation length");
    expect(() => arena.allocate(Number.NaN)).toThrow("invalid runtime arena allocation length");
    expect(() => arena.allocate(ARENA_MIN_BYTES)).toThrow("capacity exceeded");
  });

  it("guards descriptor snapshot copy and write boundaries", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const descriptor = arena.allocate(8);
    expect(() => arena.writeSlabReady(descriptor.id, new Uint8Array(descriptor.capacity + 1))).toThrow("slab too small");
    arena.writeSlabReady(descriptor.id, new Uint8Array([1, 2, 3, 4]));
    const ready = arena.readDescriptor(descriptor.id);
    expect(arena.readDescriptorBytesView(ready, 2)).toEqual(new Uint8Array([1, 2]));
    expect(arena.descriptorByteOffset(ready)).toBe(ready.offset);
    expect(arena.readArenaBytesView().buffer).toBe(arena.buffer);
    expect(() => arena.copyDescriptorBytesTo(ready, new Uint8Array(1), 0, 2)).toThrow("target too small");
    expect(() => arena.readDescriptorBytesView(ready, 5)).toThrow("exceeds snapshot bounds");
    expect(() => arena.writeDescriptorBytesReady(ready, new Uint8Array(2), -1, 1)).toThrow("source too small");
    expect(() => arena.writeDescriptorBytesReady(ready, new Uint8Array(ready.capacity + 1))).toThrow("snapshot capacity");
    arena.markConsumedById(descriptor.id);
    arena.releaseDescriptorById(descriptor.id);
    arena.releaseDescriptorById(descriptor.id);
    expect(() => arena.writeSlabReady(descriptor.id, new Uint8Array(1))).toThrow("is free");
    expect(() => arena.readDescriptorBytesView(arena.readDescriptor(descriptor.id), 0)).toThrow("is free");
    expect(() => arena.readDescriptor(-1)).toThrow("invalid runtime arena descriptor id");
  });

  it("rejects malformed columnar headers and identifiers", () => {
    const base = {
      schemaVersion: COLUMNAR_BATCH_SCHEMA_VERSION,
      rowCount: 1,
      columnCount: 1,
      flags: 0,
      metadataDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
      dictionaryDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
      fields: [{ fieldId: 1, logicalType: COLUMNAR_LOGICAL_TYPE_INT, physicalType: COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH, length: 1, valuesDescriptorId: 1, byteWidth: 4 }],
    };
    expect(() => encodeRuntimeColumnarBatchDescriptor({ ...base, schemaVersion: 99 })).toThrow("unsupported columnar batch schema");
    expect(() => encodeRuntimeColumnarBatchDescriptor({ ...base, rowCount: -1 })).toThrow("rowCount must be a uint32");
    expect(() => encodeRuntimeColumnarBatchDescriptor({ ...base, columnCount: 2 })).toThrow("does not match fields");
    expect(() => encodeRuntimeColumnarBatchDescriptor({ ...base, columnCount: 0, fields: [] })).toThrow("invalid columnar batch column count");
    expect(() => decodeRuntimeColumnarBatchDescriptor(new Uint8Array(1))).toThrow("too small");
    const encoded = encodeRuntimeColumnarBatchDescriptor(base);
    const badMagic = encoded.slice();
    new DataView(badMagic.buffer).setUint32(0, 0, true);
    expect(() => decodeRuntimeColumnarBatchDescriptor(badMagic)).toThrow("invalid columnar batch magic");
    const badSchema = encoded.slice();
    new DataView(badSchema.buffer).setUint32(4, 99, true);
    expect(() => decodeRuntimeColumnarBatchDescriptor(badSchema)).toThrow("unsupported columnar batch schema");
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

    expect(arena.enqueueDescriptorReadyBatchFast(ids, 11)).toBe(8);
    const idScratch: number[] = [];
    const idDrain = arena.dequeueDescriptorReadyIdsFast(8, idScratch);

    expect(idDrain.count).toBe(8);
    expect(idDrain.descriptorIds).toBe(idScratch);
    expect(idDrain.descriptorIds).toEqual(ids);
  });

  it("encodes columnar batch descriptors as 64-byte aligned metadata slabs", () => {
    const payload = encodeRuntimeColumnarBatchDescriptor({
      schemaVersion: COLUMNAR_BATCH_SCHEMA_VERSION,
      rowCount: 3,
      columnCount: 2,
      flags: 0,
      metadataDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
      dictionaryDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
      fields: [
        {
          fieldId: 1,
          logicalType: COLUMNAR_LOGICAL_TYPE_INT,
          physicalType: COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH,
          length: 3,
          valuesDescriptorId: 11,
          byteWidth: 8,
        },
        {
          fieldId: 2,
          logicalType: COLUMNAR_LOGICAL_TYPE_UTF8,
          physicalType: COLUMNAR_PHYSICAL_TYPE_VARIABLE_BINARY,
          flags: COLUMNAR_FIELD_FLAG_NULLABLE,
          length: 3,
          nullCount: 1,
          validityDescriptorId: 12,
          offsetsDescriptorId: 13,
          valuesDescriptorId: 14,
        },
      ],
    });

    expect(payload.byteLength % COLUMNAR_BATCH_ALIGNMENT_BYTES).toBe(0);
    const decoded = decodeRuntimeColumnarBatchDescriptor(payload);
    expect(decoded.rowCount).toBe(3);
    expect(decoded.fields.map((field) => field.valuesDescriptorId)).toEqual([11, 14]);
    expect(decoded.fields[1].offsetsDescriptorId).toBe(13);
  });

  it("publishes columnar batch descriptors through the arena queue", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: ARENA_MIN_BYTES });
    const values = arena.allocate(24, ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES);
    const offsets = arena.allocate(16, ARENA_DESCRIPTOR_TYPE_COLUMNAR_OFFSETS);
    arena.writeSlabReady(values.id, new Uint8Array(24));
    arena.writeSlabReady(offsets.id, new Uint8Array(16));

    const batch = arena.writeColumnarBatchDescriptor(
      [
        {
          fieldId: 7,
          logicalType: COLUMNAR_LOGICAL_TYPE_UTF8,
          physicalType: COLUMNAR_PHYSICAL_TYPE_VARIABLE_BINARY,
          length: 3,
          offsetsDescriptorId: offsets.id,
          valuesDescriptorId: values.id,
        },
      ],
      { rowCount: 3, correlationHash: 99 }
    );

    expect(batch.type).toBe(ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH);
    expect(arena.dequeue()?.descriptorId).toBe(batch.id);
    expect(arena.readColumnarBatchDescriptor(batch.id).fields[0]).toMatchObject({
      fieldId: 7,
      length: 3,
      offsetsDescriptorId: offsets.id,
      valuesDescriptorId: values.id,
    });
  });

  it("rejects columnar descriptors whose field lengths do not match the row count", () => {
    expect(() =>
      encodeRuntimeColumnarBatchDescriptor({
        schemaVersion: COLUMNAR_BATCH_SCHEMA_VERSION,
        rowCount: 2,
        columnCount: 1,
        flags: 0,
        metadataDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
        dictionaryDescriptorId: COLUMNAR_DESCRIPTOR_ID_NONE,
        fields: [
          {
            fieldId: 1,
            logicalType: COLUMNAR_LOGICAL_TYPE_INT,
            physicalType: COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH,
            length: 3,
            valuesDescriptorId: 1,
            byteWidth: 4,
          },
        ],
      })
    ).toThrow(/does not match row count/);
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
