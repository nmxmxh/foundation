import { describe, expect, it, vi } from "vitest";
import {
  RuntimeSharedArena,
  clampRuntimeArenaBytes,
  negotiateRuntimeMemory,
} from "./arena";
import {
  ARENA_DEFAULT_BYTES,
  ARENA_DESCRIPTOR_STATE_CONSUMED,
  ARENA_DESCRIPTOR_STATE_READY,
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
    expect(arena.enqueueDescriptorReady(descriptor.id, 42)).toBe(true);

    const entry = arena.dequeue();
    expect(entry?.descriptorId).toBe(descriptor.id);
    expect(entry?.length).toBe(payload.byteLength);

    const consumed = arena.markConsumed(descriptor.id);
    expect(consumed.state).toBe(ARENA_DESCRIPTOR_STATE_CONSUMED);
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
