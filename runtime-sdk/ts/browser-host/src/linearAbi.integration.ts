import { describe, expect, it } from "vitest";
import { createAbiFixture, loadAbiModule } from "../fixtures/browserAbi";
import { RuntimeMemoryRegion } from "./memoryRegion";
import { ARENA_OFFSET_PAGES, BUFFER_TOTAL_BYTES, INPUT_MAX_BYTES, INT_IDX_INPUT_LENGTH } from "./generated/runtimeBuffer";

for (const shared of [false, true]) {
  describe(`real Rust browser ABI, shared=${shared}`, () => {
    it("preserves the full control contract without copy imports", async () => {
      const module = await loadAbiModule(shared);
      for (const input of [new Uint8Array(), new TextEncoder().encode("preview"), new TextEncoder().encode("!reject"), new TextEncoder().encode("@clock"), new Uint8Array(INPUT_MAX_BYTES).fill(7)]) {
        const { host, exports, copies } = await createAbiFixture(module, shared);
        const direct = host.createRuntimeBuffer();
        expect(direct.handle).toBeGreaterThanOrEqual(0x80000000);
        expect(direct.buffer).toBe(exports.memory.buffer);
        const legacy = new SharedArrayBuffer(BUFFER_TOTAL_BYTES);
        const oldHandle = host.registerBuffer(legacy);
        host.setInputBytes(direct, input);
        host.setInputBytes(legacy, input);
        const result = exports.ovrt_unit_run(direct.handle);
        expect(copies.calls).toBe(0);
        expect(exports.ovrt_unit_run(oldHandle)).toBe(result);
        expect(copies.calls).toBeGreaterThan(0);
        expect(direct.bytes).toEqual(new Uint8Array(legacy));
        host.dispose();
      }
    });

    it("refreshes views after growth and rejects stale handles and invalid lengths", async () => {
      const { host, exports } = await createAbiFixture(await loadAbiModule(shared), shared);
      const region = host.createRuntimeBuffer();
      const before = region.bytes;
      exports.memory.grow(1);
      expect(region.bytes === before).toBe(false);
      host.setInputBytes(region, new Uint8Array([1, 2, 3]));
      expect(exports.ovrt_unit_run(region.handle)).toBe(0);
      expect(host.readOutputBytes(region)).toEqual(new Uint8Array([91, 88, 89]));
      host.setHeaderInt(region, INT_IDX_INPUT_LENGTH, -1);
      expect(exports.ovrt_unit_run(region.handle)).toBe(2);
      host.setHeaderInt(region, INT_IDX_INPUT_LENGTH, INPUT_MAX_BYTES + 1);
      expect(exports.ovrt_unit_run(region.handle)).toBe(2);
      region.release();
      expect(() => region.bytes).toThrow("released");
      expect(exports.ovrt_unit_run(region.handle)).toBe(2);
      host.dispose();
    });

    it("borrows large regions and bounds the allocation budget", async () => {
      const { host, exports, copies } = await createAbiFixture(await loadAbiModule(shared), shared);
      const region = host.createBuffer(4 * 1024 * 1024);
      region.bytes.fill(3);
      expect(exports.ovrt_buffer_checksum(region.handle, 0, region.byteLength)).toBe(3 * region.byteLength);
      expect(copies).toEqual({ calls: 0, bytes: 0 });
      expect(exports.ovrt_buffer_checksum(region.handle, region.byteLength, 1) >>> 0).toBe(0xffffffff);
      expect(() => host.createBuffer(64 * 1024 * 1024 + 8)).toThrow("64 MiB");
      const regions = Array.from({ length: 63 }, () => host.createRuntimeBuffer());
      expect(() => host.createRuntimeBuffer()).toThrow("allocation failed");
      regions.forEach((buffer) => buffer.release());
      region.release();
      expect(host.createRuntimeBuffer().handle).toBeGreaterThan(region.handle);
      host.dispose();
    });
  });
}

it("maps arena descriptors into the guest allocation after memory growth", async () => {
  const { host, exports, copies } = await createAbiFixture(await loadAbiModule(true), true);
  const arena = host.createSharedArena();
  const descriptor = arena.allocate(4096);
  arena.writeSlabReady(descriptor.id, new Uint8Array(4096).fill(5));
  expect(arena.byteOffset).toBeGreaterThan(0);
  expect(descriptor.offset).toBeGreaterThanOrEqual(ARENA_OFFSET_PAGES);
  exports.memory.grow(1);
  expect(exports.ovrt_buffer_checksum(arena.region.handle, descriptor.offset, 4096)).toBe(4096 * 5);
  expect(copies.calls).toBe(0);
  expect(arena.capacity()).toBe(arena.region.byteLength);
  host.dispose();
});

it("supports the heavy arena with a control region and enforces the total byte budget", async () => {
  const { host } = await createAbiFixture(await loadAbiModule(true), true);
  const control = host.createRuntimeBuffer();
  const arena = host.createSharedArena({ arenaProfile: "heavy" });
  expect(arena.capacity()).toBe(64 * 1024 * 1024);
  expect(() => host.createBuffer(32 * 1024 * 1024)).toThrow("allocation failed");
  control.release();
  const rest = host.createBuffer(32 * 1024 * 1024);
  expect(() => host.createRuntimeBuffer()).toThrow("allocation failed");
  rest.release();
  expect(host.createRuntimeBuffer().byteLength).toBe(4096);
  host.dispose();
});

it("rejects malformed host regions", () => {
  const memory = new WebAssembly.Memory({ initial: 1 });
  expect(() => new RuntimeMemoryRegion(memory, 1, 4096)).toThrow("aligned");
  expect(() => new RuntimeMemoryRegion(memory, 65536, 4096)).toThrow("capacity");
  const region = new RuntimeMemoryRegion(memory, 64, 4096);
  for (const offset of [-1, 0.5, NaN, Infinity, 4097]) expect(() => region.subarray(offset, 1)).toThrow("bounds");
});
