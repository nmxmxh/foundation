import { describe, expect, it, vi } from "vitest";
import {
  BUFFER_TOTAL_BYTES,
  IDX_INPUT_WRITTEN,
  IDX_OUTPUT_CONSUMED,
  INPUT_MAX_BYTES,
  INT_IDX_INPUT_LENGTH,
  INT_IDX_OUTPUT_LENGTH,
  OFFSET_INPUT_BYTES,
  OFFSET_OUTPUT_BYTES,
  OUTPUT_MAX_BYTES,
} from "./generated/runtimeBuffer";
import { BrowserRuntimeHost } from "./host";

describe("BrowserRuntimeHost", () => {
  it("owns runtime buffers, headers, payloads, diagnostics, and epochs", () => {
    const host = new BrowserRuntimeHost();
    const buffer = host.createRuntimeBuffer();
    expect(buffer.byteLength).toBe(BUFFER_TOTAL_BYTES);
    const handle = host.registerBuffer(buffer);
    const imports = host.getImportObject().env as Record<string, (...args: number[]) => number>;
    expect(imports.ovrt_get_byte_length(handle)).toBe(BUFFER_TOTAL_BYTES);
    expect(imports.ovrt_atomic_store(handle, IDX_INPUT_WRITTEN, 3)).toBe(3);
    expect(imports.ovrt_atomic_load(handle, IDX_INPUT_WRITTEN)).toBe(3);
    expect(imports.ovrt_atomic_add(handle, IDX_INPUT_WRITTEN, 2)).toBe(3);
    expect(imports.ovrt_atomic_compare_exchange(handle, IDX_INPUT_WRITTEN, 5, 7)).toBe(5);

    host.setInputBytes(buffer, new Uint8Array([1, 2, 3]), 4);
    expect(host.getHeaderInt(buffer, INT_IDX_INPUT_LENGTH)).toBe(3);
    expect(new Uint8Array(buffer, OFFSET_INPUT_BYTES, 3)).toEqual(new Uint8Array([1, 2, 3]));
    host.clearInput(buffer);
    expect(host.getHeaderInt(buffer, INT_IDX_INPUT_LENGTH)).toBe(0);

    new Uint8Array(buffer, OFFSET_OUTPUT_BYTES, 2).set([8, 9]);
    host.setHeaderInt(buffer, INT_IDX_OUTPUT_LENGTH, 2);
    expect(host.readOutputBytes(buffer)).toEqual(new Uint8Array([8, 9]));
    expect(host.markOutputConsumed(buffer)).toBe(1);
    expect(host.getEpochView(buffer)[IDX_OUTPUT_CONSUMED]).toBe(1);
    host.clearOutput(buffer);
    expect(host.readOutputBytes(buffer)).toHaveLength(0);

    host.writeDiagnostics(buffer, "diagnostic");
    expect(host.readDiagnostics(buffer)).toBe("diagnostic");
    expect(host.getHeaderView(buffer)).toHaveLength(8);
    host.unregisterBuffer(handle);
    expect(() => imports.ovrt_get_byte_length(handle)).toThrow("unknown runtime buffer handle");
  });

  it("checks payload bounds and copies through attached wasm memory", () => {
    const host = new BrowserRuntimeHost();
    const buffer = host.createRuntimeBuffer();
    const handle = host.registerBuffer(buffer);
    expect(() => host.setInputBytes(buffer, new Uint8Array(INPUT_MAX_BYTES + 1))).toThrow("exceeds runtime capacity");
    host.setHeaderInt(buffer, INT_IDX_OUTPUT_LENGTH, OUTPUT_MAX_BYTES + 1);
    expect(() => host.readOutputBytes(buffer)).toThrow("invalid output length");

    const memory = new WebAssembly.Memory({ initial: 1 });
    host.attachInstance({ exports: { memory } } as unknown as WebAssembly.Instance & { exports: { memory: WebAssembly.Memory } });
    new Uint8Array(memory.buffer, 16, 3).set([4, 5, 6]);
    const imports = host.getImportObject({ env: { custom: () => 1 } }).env as Record<string, (...args: number[]) => unknown>;
    imports.ovrt_copy_to_buffer(handle, 32, 16, 3);
    expect(new Uint8Array(buffer, 32, 3)).toEqual(new Uint8Array([4, 5, 6]));
    new Uint8Array(buffer, 40, 2).set([7, 8]);
    imports.ovrt_copy_from_buffer(handle, 40, 24, 2);
    expect(new Uint8Array(memory.buffer, 24, 2)).toEqual(new Uint8Array([7, 8]));
    expect(imports.custom()).toBe(1);
  });

  it("routes log imports and random filling", () => {
    const host = new BrowserRuntimeHost();
    const memory = new WebAssembly.Memory({ initial: 1 });
    host.attachInstance({ exports: { memory } } as unknown as WebAssembly.Instance & { exports: { memory: WebAssembly.Memory } });
    new TextEncoder().encodeInto("hello", new Uint8Array(memory.buffer, 0, 5));
    const spies = [vi.spyOn(console, "error").mockImplementation(() => undefined), vi.spyOn(console, "warn").mockImplementation(() => undefined), vi.spyOn(console, "info").mockImplementation(() => undefined), vi.spyOn(console, "debug").mockImplementation(() => undefined)];
    const imports = host.getImportObject().env as Record<string, (...args: number[]) => unknown>;
    for (let level = 0; level < 4; level += 1) imports.ovrt_log(0, 5, level);
    expect(spies.every((spy) => spy.mock.calls.length === 1)).toBe(true);
    imports.ovrt_fill_random(8, 8);
    expect(new Uint8Array(memory.buffer, 8, 8).some((value) => value !== 0)).toBe(true);
    expect(typeof imports.ovrt_get_now()).toBe("number");
    vi.restoreAllMocks();
  });

  it("routes log-ring bytes and rejects wasm-memory access before attachment", () => {
    const host = new BrowserRuntimeHost();
    const writeRaw = vi.fn();
    host.setLogRing({ writeRaw } as never);
    expect(() => (host.getImportObject().env as Record<string, (...args: number[]) => unknown>).ovrt_log_ring(0, 1)).toThrow("runtime memory is not attached");
    const memory = new WebAssembly.Memory({ initial: 1 });
    host.attachInstance({ exports: { memory } } as never);
    new Uint8Array(memory.buffer, 0, 2).set([1, 2]);
    (host.getImportObject().env as Record<string, (...args: number[]) => unknown>).ovrt_log_ring(0, 2);
    expect(writeRaw).toHaveBeenCalledWith(expect.objectContaining({ byteLength: 2 }));
  });

  it("negotiates arenas and reports missing shared-memory capability", () => {
    const host = new BrowserRuntimeHost();
    expect(host.negotiateMemory({ sharedMemory: "off" }).arena).toBeNull();
    expect(host.createSharedArena({ arenaProfile: "minimal" }).capacity()).toBeGreaterThan(BUFFER_TOTAL_BYTES);
    vi.stubGlobal("SharedArrayBuffer", undefined);
    try {
      expect(() => host.createRuntimeBuffer()).toThrow("SharedArrayBuffer is unavailable");
      expect(() => host.createSharedArena()).toThrow("SharedArrayBuffer is unavailable");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("instantiates wasm through streaming and byte fallbacks", async () => {
    const host = new BrowserRuntimeHost();
    const instance = { exports: { memory: new WebAssembly.Memory({ initial: 1 }) } } as unknown as WebAssembly.Instance;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new Uint8Array([0]))));
    const streaming = vi.spyOn(WebAssembly, "instantiateStreaming").mockResolvedValue({ instance, module: {} as WebAssembly.Module });
    await expect(host.instantiate("/runtime.wasm")).resolves.toBe(instance);
    expect(streaming).toHaveBeenCalled();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
});
