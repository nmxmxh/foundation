import { BrowserRuntimeHost } from "./host";
import type { RuntimeModuleExports } from "./runtimeDispatcher";
import { getRuntimeCapabilities } from "./pulse/runtimeCaps";
import type { RuntimeMemoryRegion } from "./memoryRegion";

export type RuntimeModuleLoadResult = {
  name: string;
  exports: RuntimeModuleExports;
  memory: WebAssembly.Memory;
  initialized: boolean;
  host: BrowserRuntimeHost;
  controlBuffer: RuntimeMemoryRegion | null;
};

export type RuntimeModuleLoaderOptions = {
  host?: BrowserRuntimeHost;
  moduleBasePath?: string;
  versionQuery?: string;
  target?: Record<string, unknown>;
  compatInosGlobals?: boolean;
  imports?: WebAssembly.Imports;
  fetchImpl?: typeof fetch;
};

export type RuntimeModuleLoadOptions = {
  rawUrl?: string;
  compressedUrl?: string;
  initExportName?: string;
  moduleId?: number;
};

export class RuntimeModuleLoader {
  private static readonly claimedMemories = new WeakSet<WebAssembly.Memory>();
  private readonly compiled = new Map<string, WebAssembly.Module>();
  private readonly instances = new Map<string, RuntimeModuleLoadResult>();
  private readonly pending = new Map<string, { memory: WebAssembly.Memory; result: Promise<RuntimeModuleLoadResult> }>();
  private readonly host: BrowserRuntimeHost;
  private readonly moduleBasePath: string;
  private readonly versionQuery: string;
  private readonly target: Record<string, unknown>;
  private readonly compatInosGlobals: boolean;
  private readonly imports: WebAssembly.Imports;
  private readonly fetchImpl: typeof fetch;

  constructor(options: RuntimeModuleLoaderOptions = {}) {
    this.host = options.host ?? new BrowserRuntimeHost();
    this.moduleBasePath = options.moduleBasePath ?? "/modules";
    this.versionQuery = options.versionQuery ?? "";
    this.target = options.target ?? (globalThis as unknown as Record<string, unknown>);
    this.compatInosGlobals = options.compatInosGlobals === true;
    this.imports = options.imports ?? {};
    this.fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);
  }

  async load(name: string, suppliedMemory?: WebAssembly.Memory, options: RuntimeModuleLoadOptions = {}): Promise<RuntimeModuleLoadResult> {
    const cached = this.instances.get(name);
    if (cached) {
      if (suppliedMemory && cached.memory !== suppliedMemory) throw new Error("runtime module is already bound to another memory");
      return cached;
    }
    const pending = this.pending.get(name);
    if (pending) {
      if (suppliedMemory && pending.memory !== suppliedMemory) throw new Error("runtime module is already bound to another memory");
      return pending.result;
    }

    const shared = suppliedMemory ? typeof SharedArrayBuffer !== "undefined" && suppliedMemory.buffer instanceof SharedArrayBuffer
      : getRuntimeCapabilities().supportsSharedWasmMemory;
    const importedMemory = suppliedMemory ?? new WebAssembly.Memory({ initial: 32, maximum: 2048, shared });
    this.claimMemory(importedMemory);
    const result = this.loadInstance(name, importedMemory, options, shared);
    this.pending.set(name, { memory: importedMemory, result });
    try {
      return await result;
    } finally {
      this.pending.delete(name);
    }
  }

  private async loadInstance(name: string, importedMemory: WebAssembly.Memory, options: RuntimeModuleLoadOptions, shared: boolean): Promise<RuntimeModuleLoadResult> {
    const host = this.host.fork();
    let memory = importedMemory;
    try {
      const module = await this.compile(name, options, shared);
      const instance = await WebAssembly.instantiate(module, host.getImportObject({
        ...this.imports,
        env: { ...(this.imports.env ?? {}), memory: importedMemory },
      }));
      const exports = instance.exports as RuntimeModuleExports;
      const exportedMemory = exports.memory ?? importedMemory;
      if (exportedMemory !== importedMemory) {
        this.claimMemory(exportedMemory);
        RuntimeModuleLoader.claimedMemories.delete(importedMemory);
        memory = exportedMemory;
      }
      host.attachInstance(instance, memory);
      const abiVersion = exports.ovrt_buffer_abi_version;
      const controlBuffer = typeof abiVersion === "function" && abiVersion() === 2 ? host.createRuntimeBuffer() : null;
      this.publishMemoryGlobals(memory, options.moduleId ?? 0, controlBuffer);
      const initName = options.initExportName ?? `${name}_init_with_sab`;
      const initFn = (exports[initName] ?? exports.init_with_sab) as unknown;
      const initialized = typeof initFn === "function" ? Boolean((initFn as () => number | boolean)()) : false;
      const result = { name, exports, memory, initialized, host, controlBuffer };
      this.instances.set(name, result);
      return result;
    } catch (error) {
      host.dispose();
      RuntimeModuleLoader.claimedMemories.delete(importedMemory);
      RuntimeModuleLoader.claimedMemories.delete(memory);
      this.removeMemoryGlobals(memory);
      throw error;
    }
  }

  clear(): void {
    if (this.pending.size > 0) throw new Error("wait for runtime module loads before clearing the loader");
    for (const result of this.instances.values()) {
      result.host.dispose();
      RuntimeModuleLoader.claimedMemories.delete(result.memory);
      this.removeMemoryGlobals(result.memory);
    }
    this.instances.clear();
  }

  private claimMemory(memory: WebAssembly.Memory): void {
    if (RuntimeModuleLoader.claimedMemories.has(memory)) throw new Error("runtime modules require separate allocator memories");
    RuntimeModuleLoader.claimedMemories.add(memory);
  }

  private async compile(name: string, options: RuntimeModuleLoadOptions, shared: boolean): Promise<WebAssembly.Module> {
    const key = `${name}:${shared}`;
    const cached = this.compiled.get(key);
    if (cached) return cached;

    const urls = this.moduleUrls(name, options, shared);
    let lastError: unknown = null;
    for (const url of urls) {
      try {
        const response = await this.fetchImpl(url, { signal: AbortSignal.timeout(30_000) });
        if (!response.ok) {
          throw new Error(`failed to fetch ${url}: ${response.status} ${response.statusText}`);
        }
        let module: WebAssembly.Module;
        if (typeof WebAssembly.compileStreaming === "function" && !url.split("?")[0].endsWith(".br")) {
          module = await WebAssembly.compileStreaming(response);
        } else {
          module = await WebAssembly.compile(await response.arrayBuffer());
        }
        this.compiled.set(key, module);
        return module;
      } catch (error) {
        lastError = error;
      }
    }
    throw lastError instanceof Error ? lastError : new Error(`failed to compile runtime module ${name}`);
  }

  private moduleUrls(name: string, options: RuntimeModuleLoadOptions, shared: boolean): string[] {
    if (options.rawUrl || options.compressedUrl) {
      return [options.compressedUrl, options.rawUrl].filter((url): url is string => Boolean(url));
    }
    const query = this.versionQuery ? `?${this.versionQuery.replace(/^\?/, "")}` : "";
    return [
      ...(shared ? [`${this.moduleBasePath}/${name}.shared.wasm${query}`] : []),
      `${this.moduleBasePath}/${name}.wasm.br${query}`, `${this.moduleBasePath}/${name}.wasm${query}`,
    ];
  }

  private publishMemoryGlobals(memory: WebAssembly.Memory, moduleId: number, control: RuntimeMemoryRegion | null): void {
    const buffer = memory.buffer;
    Object.defineProperty(this.target, "__OVRT_SAB__", { configurable: true, get: () => memory.buffer });
    this.target.__OVRT_MEM__ = memory;
    this.target.__OVRT_SAB_OFFSET__ = control?.byteOffset ?? 0;
    this.target.__OVRT_SAB_SIZE__ = control?.byteLength ?? buffer.byteLength;
    this.target.__OVRT_BUFFER_HANDLE__ = control?.handle ?? 0;
    this.target.__OVRT_MODULE_ID__ = moduleId;

    if (this.compatInosGlobals) {
      Object.defineProperty(this.target, "__INOS_SAB__", { configurable: true, get: () => memory.buffer });
      this.target.__INOS_MEM__ = memory;
      this.target.__INOS_SAB_OFFSET__ = control?.byteOffset ?? 0;
      this.target.__INOS_SAB_SIZE__ = control?.byteLength ?? buffer.byteLength;
      this.target.__INOS_MODULE_ID__ = moduleId;
    }
  }

  private removeMemoryGlobals(memory: WebAssembly.Memory): void {
    for (const prefix of ["__OVRT", ...(this.compatInosGlobals ? ["__INOS"] : [])]) {
      if (this.target[`${prefix}_MEM__`] !== memory) continue;
      for (const key of ["SAB", "MEM", "SAB_OFFSET", "SAB_SIZE", "BUFFER_HANDLE", "MODULE_ID"]) {
        delete this.target[`${prefix}_${key}__`];
      }
    }
  }
}

export const createRuntimeModuleLoader = (options?: RuntimeModuleLoaderOptions): RuntimeModuleLoader =>
  new RuntimeModuleLoader(options);
