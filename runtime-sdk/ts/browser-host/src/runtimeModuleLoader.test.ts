import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserRuntimeHost } from "./host";
import { RuntimeModuleLoader } from "./runtimeModuleLoader";

describe("RuntimeModuleLoader", () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("falls back from compressed input, initializes, caches, and publishes globals", async () => {
    const target: Record<string, unknown> = {};
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response(new Uint8Array([0]), { headers: { "Content-Type": "application/wasm" } }));
    const module = {} as WebAssembly.Module;
    vi.spyOn(WebAssembly, "compile").mockResolvedValue(module);
    vi.spyOn(WebAssembly, "compileStreaming").mockResolvedValue(module);
    vi.spyOn(WebAssembly, "instantiate").mockResolvedValue({ exports: { init_with_sab: () => 1 } } as WebAssembly.Instance);
    const loader = new RuntimeModuleLoader({ host: new BrowserRuntimeHost(), target, compatInosGlobals: true, versionQuery: "v=1", fetchImpl: fetchImpl as typeof fetch });
    const memory = new WebAssembly.Memory({ initial: 1 });
    const first = await loader.load("echo", memory, { moduleId: 7 });
    const second = await loader.load("echo", memory);
    expect(first).toBe(second);
    expect(first.initialized).toBe(true);
    expect(fetchImpl.mock.calls.map(([url]) => url)).toEqual(["/modules/echo.wasm.br?v=1", "/modules/echo.wasm?v=1"]);
    expect(target).toMatchObject({ __OVRT_MEM__: memory, __OVRT_MODULE_ID__: 7, __INOS_MEM__: memory, __INOS_MODULE_ID__: 7 });
    loader.clear();
    await loader.load("echo", memory);
    expect(WebAssembly.compileStreaming).toHaveBeenCalledTimes(1);
    expect(WebAssembly.instantiate).toHaveBeenCalledTimes(2);
    loader.clear();
  });

  it("surfaces final fetch failure for explicit URLs", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response("bad", { status: 500, statusText: "No" }));
    const loader = new RuntimeModuleLoader({ fetchImpl: fetchImpl as typeof fetch });
    await expect(loader.load("bad", new WebAssembly.Memory({ initial: 1 }), { compressedUrl: "/bad.br", rawUrl: "/bad.wasm" })).rejects.toThrow("failed to fetch /bad.wasm");
  });

  it("selects shared artifacts automatically and publishes refreshed region globals", async () => {
    vi.stubGlobal("crossOriginIsolated", true);
    vi.stubGlobal("Worker", function () {});
    const target: Record<string, unknown> = {};
    const fetchImpl = vi.fn().mockImplementation(async () => new Response(new Uint8Array([0])));
    vi.spyOn(WebAssembly, "compileStreaming").mockResolvedValue({} as WebAssembly.Module);
    const release = vi.fn(() => 1);
    vi.spyOn(WebAssembly, "instantiate").mockImplementation(async (_module, imports) => ({
      exports: { memory: imports!.env.memory, ovrt_buffer_abi_version: () => 2,
        ovrt_buffer_alloc: () => -2147483648, ovrt_buffer_ptr: () => 64, ovrt_buffer_free: release },
    }) as never);
    const loader = new RuntimeModuleLoader({ fetchImpl, target });
    const first = await loader.load("echo");
    expect(fetchImpl.mock.calls[0][0]).toBe("/modules/echo.shared.wasm");
    expect(first.controlBuffer?.shared).toBe(true);
    expect(target.__OVRT_SAB_OFFSET__).toBe(64);
    expect(target.__OVRT_SAB_SIZE__).toBe(4096);
    expect(target.__OVRT_BUFFER_HANDLE__).toBe(0x80000000);
    first.memory.grow(1);
    expect(target.__OVRT_SAB__).toBe(first.memory.buffer);
    const second = await loader.load("second");
    expect(second.memory).not.toBe(first.memory);
    expect(second.host).not.toBe(first.host);
    await expect(loader.load("echo", second.memory)).rejects.toThrow("another memory");
    loader.clear();
    expect(release).toHaveBeenCalledTimes(2);
    expect(target.__OVRT_MEM__).toBeUndefined();
    expect(() => first.controlBuffer!.bytes).toThrow("released");
  });

  it("deduplicates concurrent loads and reserves allocator memory across loaders", async () => {
    let complete!: (response: Response) => void;
    const fetchImpl = vi.fn(() => new Promise<Response>((resolve) => { complete = resolve; }));
    vi.spyOn(WebAssembly, "compile").mockResolvedValue({} as WebAssembly.Module);
    vi.spyOn(WebAssembly, "instantiate").mockResolvedValue({ exports: {} } as WebAssembly.Instance);
    const loader = new RuntimeModuleLoader({ fetchImpl });
    const other = new RuntimeModuleLoader({ fetchImpl });
    const memory = new WebAssembly.Memory({ initial: 1 });
    const first = loader.load("echo", memory);
    const second = loader.load("echo", memory);
    await expect(loader.load("echo", new WebAssembly.Memory({ initial: 1 }))).rejects.toThrow("another memory");
    await expect(other.load("other", memory)).rejects.toThrow("separate allocator memories");
    expect(() => loader.clear()).toThrow("wait for runtime module loads");
    complete(new Response(new Uint8Array([0])));
    expect(await first).toBe(await second);
    expect(fetchImpl).toHaveBeenCalledOnce();
    await expect(loader.load("other", memory)).rejects.toThrow("separate allocator memories");
    loader.clear();
  });

  it("releases allocations and memory ownership after failed initialization", async () => {
    const memory = new WebAssembly.Memory({ initial: 1 });
    const target: Record<string, unknown> = {};
    const release = vi.fn(() => 1);
    const fetchImpl = vi.fn(async () => new Response(new Uint8Array([0])));
    vi.spyOn(WebAssembly, "compile").mockResolvedValue({} as WebAssembly.Module);
    const init = vi.fn().mockImplementationOnce(() => { throw new Error("init failed"); }).mockReturnValue(1);
    vi.spyOn(WebAssembly, "instantiate").mockResolvedValue({ exports: {
      memory, ovrt_buffer_abi_version: () => 2, ovrt_buffer_alloc: () => -2147483648,
      ovrt_buffer_ptr: () => 64, ovrt_buffer_free: release, init_with_sab: init,
    } } as unknown as WebAssembly.Instance);
    const loader = new RuntimeModuleLoader({ fetchImpl, target });
    await expect(loader.load("echo", memory)).rejects.toThrow("init failed");
    expect(release).toHaveBeenCalledOnce();
    expect(target.__OVRT_MEM__).toBeUndefined();
    expect((await loader.load("echo", memory)).initialized).toBe(true);
    expect(fetchImpl).toHaveBeenCalledOnce();
    loader.clear();
  });
});
