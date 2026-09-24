import { describe, expect, it, vi } from "vitest";
import { RuntimeMemoryRegion } from "./memoryRegion";
import { BrowserRuntimeHost } from "./host";

describe("RuntimeMemoryRegion", () => {
  it("caches region-relative views and refreshes them after memory growth", () => {
    const memory = new WebAssembly.Memory({ initial: 1, maximum: 2 });
    const region = new RuntimeMemoryRegion(memory, 64, 4096);
    region.bytes[0] = 7;
    expect(region.bytes).toBe(region.bytes);
    expect(region.ints.byteOffset).toBe(64);
    expect(region.view.byteLength).toBe(4096);
    expect(region.subarray(0, 1)).toEqual(new Uint8Array([7]));
    memory.grow(1);
    expect(region.buffer).toBe(memory.buffer);
    expect(region.bytes[0]).toBe(7);
    expect(region.view.getUint8(0)).toBe(7);
    expect(region.shared).toBe(false);
    expect(() => region.subarray(4096, 0)).not.toThrow();
    for (const offset of [-1, 0.5, NaN, Infinity, 4097]) expect(() => region.subarray(offset, 1)).toThrow("bounds");
    for (const length of [-1, 0.5, NaN, Infinity, 4097]) expect(() => region.subarray(0, length)).toThrow("bounds");
    for (const offset of [-8, 1, NaN]) expect(() => new RuntimeMemoryRegion(memory, offset, 4096)).toThrow("aligned");
    for (const size of [0, -4, 3, NaN]) expect(() => new RuntimeMemoryRegion(memory, 0, size)).toThrow("aligned");
    expect(() => new RuntimeMemoryRegion(memory, 128 * 1024, 4096)).toThrow("capacity");
  });

  it("retains ownership when release fails and rejects views after release", () => {
    const release = vi.fn().mockImplementationOnce(() => { throw new Error("busy"); });
    const region = new RuntimeMemoryRegion(new SharedArrayBuffer(4096), 0, 4096, 0, release);
    expect(region.shared).toBe(true);
    expect(() => region.release()).toThrow("busy");
    expect(region.bytes.byteLength).toBe(4096);
    region.release();
    region.release();
    expect(release).toHaveBeenCalledTimes(2);
    expect(() => region.bytes).toThrow("released");
    expect(() => new BrowserRuntimeHost().registerBuffer(region)).toThrow("released");
    new RuntimeMemoryRegion(new ArrayBuffer(4096), 0, 4096).release();
  });
});

describe("guest allocation ownership", () => {
  const fixture = (overrides = {}) => {
    const host = new BrowserRuntimeHost();
    const exports = {
      memory: new WebAssembly.Memory({ initial: 1, maximum: 2, shared: true }),
      ovrt_buffer_abi_version: () => 2,
      ovrt_buffer_alloc: vi.fn(() => -2147483648),
      ovrt_buffer_ptr: () => 64,
      ovrt_buffer_free: vi.fn(() => 1),
      ...overrides,
    };
    const instance = { exports } as unknown as WebAssembly.Instance;
    host.attachInstance(instance);
    return { host, exports, instance };
  };

  it("uses the guest allocation and prevents accidental host or instance reuse", () => {
    const { host, exports, instance } = fixture();
    const region = host.createRuntimeBuffer();
    expect(region.handle).toBe(0x80000000);
    expect(region.byteOffset).toBe(64);
    expect(host.registerBuffer(region)).toBe(region.handle);
    expect(() => new BrowserRuntimeHost().registerBuffer(region)).toThrow("another host");
    expect(() => host.createRuntimeBuffer()).toThrow("already allocated");
    expect(exports.ovrt_buffer_free).not.toHaveBeenCalled();
    expect(() => host.attachInstance(instance)).not.toThrow();
    expect(() => host.attachInstance(instance, new WebAssembly.Memory({ initial: 1 }))).toThrow("release linear buffers");
    expect(() => host.attachInstance({ exports: {} } as WebAssembly.Instance)).toThrow("release linear buffers");
    host.unregisterBuffer(region.handle);
    expect(exports.ovrt_buffer_free).toHaveBeenCalledWith(region.handle);
    expect(() => region.bytes).toThrow("released");
    host.attachInstance({ exports: {} } as WebAssembly.Instance);
    host.dispose();
  });

  it("rejects malformed ABI exports, pointers, sizes, and failed releases", () => {
    for (const size of [0, 4095, 4097, 64 * 1024 * 1024 + 8, NaN]) {
      expect(() => fixture().host.createBuffer(size)).toThrow("size must be aligned");
    }
    expect(() => fixture({ ovrt_buffer_free: undefined }).host.createRuntimeBuffer()).toThrow("incomplete");
    expect(() => fixture({ memory: undefined }).host.createRuntimeBuffer()).toThrow("incomplete");
    expect(() => fixture({ ovrt_buffer_alloc: () => 0 }).host.createRuntimeBuffer()).toThrow("allocation failed");
    for (const pointer of [0, 1, 65536]) {
      const { host, exports } = fixture({ ovrt_buffer_ptr: () => pointer });
      expect(() => host.createRuntimeBuffer()).toThrow();
      expect(exports.ovrt_buffer_free).toHaveBeenCalledOnce();
    }
    const { host, exports } = fixture();
    const region = host.createRuntimeBuffer();
    exports.ovrt_buffer_free.mockReturnValueOnce(0);
    expect(() => region.release()).toThrow("release failed");
    expect(host.registerBuffer(region)).toBe(region.handle);
    host.dispose();
  });
});
