import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserRuntimeHost } from "./host";
import { RuntimeModuleLoader } from "./runtimeModuleLoader";

describe("RuntimeModuleLoader", () => {
  afterEach(() => vi.restoreAllMocks());

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
  });

  it("surfaces final fetch failure for explicit URLs", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response("bad", { status: 500, statusText: "No" }));
    const loader = new RuntimeModuleLoader({ fetchImpl: fetchImpl as typeof fetch });
    await expect(loader.load("bad", new WebAssembly.Memory({ initial: 1 }), { compressedUrl: "/bad.br", rawUrl: "/bad.wasm" })).rejects.toThrow("failed to fetch /bad.wasm");
  });
});
