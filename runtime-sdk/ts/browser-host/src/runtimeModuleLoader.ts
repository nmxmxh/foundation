import { BrowserRuntimeHost } from "./host";
import type { RuntimeModuleExports } from "./runtimeDispatcher";

export type RuntimeModuleLoadResult = {
  name: string;
  exports: RuntimeModuleExports;
  memory: WebAssembly.Memory;
  initialized: boolean;
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
  private readonly compiled = new Map<string, WebAssembly.Module>();
  private readonly instances = new Map<string, RuntimeModuleLoadResult>();
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
    this.fetchImpl = options.fetchImpl ?? fetch;
  }

  async load(name: string, sharedMemory: WebAssembly.Memory, options: RuntimeModuleLoadOptions = {}): Promise<RuntimeModuleLoadResult> {
    const cached = this.instances.get(name);
    if (cached) return cached;

    this.publishMemoryGlobals(sharedMemory, options.moduleId ?? 0);
    const module = await this.compile(name, options);
    const instance = await WebAssembly.instantiate(module, this.host.getImportObject({
      ...this.imports,
      env: {
        ...(this.imports.env ?? {}),
        memory: sharedMemory,
      },
    }));
    const exports = instance.exports as RuntimeModuleExports;
    const memory = exports.memory ?? sharedMemory;
    const initName = options.initExportName ?? `${name}_init_with_sab`;
    const initFn = (exports[initName] ?? exports.init_with_sab) as unknown;
    const initialized = typeof initFn === "function" ? Boolean((initFn as () => number | boolean)()) : false;
    const result = { name, exports, memory, initialized };
    this.instances.set(name, result);
    return result;
  }

  clear(): void {
    this.instances.clear();
  }

  private async compile(name: string, options: RuntimeModuleLoadOptions): Promise<WebAssembly.Module> {
    const cached = this.compiled.get(name);
    if (cached) return cached;

    const urls = this.moduleUrls(name, options);
    let lastError: unknown = null;
    for (const url of urls) {
      try {
        const response = await this.fetchImpl(url);
        if (!response.ok) {
          throw new Error(`failed to fetch ${url}: ${response.status} ${response.statusText}`);
        }
        let module: WebAssembly.Module;
        if (typeof WebAssembly.compileStreaming === "function" && !url.endsWith(".br")) {
          module = await WebAssembly.compileStreaming(response);
        } else {
          module = await WebAssembly.compile(await response.arrayBuffer());
        }
        this.compiled.set(name, module);
        return module;
      } catch (error) {
        lastError = error;
      }
    }
    throw lastError instanceof Error ? lastError : new Error(`failed to compile runtime module ${name}`);
  }

  private moduleUrls(name: string, options: RuntimeModuleLoadOptions): string[] {
    if (options.rawUrl || options.compressedUrl) {
      return [options.compressedUrl, options.rawUrl].filter((url): url is string => Boolean(url));
    }
    const query = this.versionQuery ? `?${this.versionQuery.replace(/^\?/, "")}` : "";
    return [`${this.moduleBasePath}/${name}.wasm.br${query}`, `${this.moduleBasePath}/${name}.wasm${query}`];
  }

  private publishMemoryGlobals(memory: WebAssembly.Memory, moduleId: number): void {
    const buffer = memory.buffer;
    this.target.__OVRT_SAB__ = buffer;
    this.target.__OVRT_MEM__ = memory;
    this.target.__OVRT_SAB_OFFSET__ = 0;
    this.target.__OVRT_SAB_SIZE__ = buffer.byteLength;
    this.target.__OVRT_MODULE_ID__ = moduleId;

    if (this.compatInosGlobals) {
      this.target.__INOS_SAB__ = buffer;
      this.target.__INOS_MEM__ = memory;
      this.target.__INOS_SAB_OFFSET__ = 0;
      this.target.__INOS_SAB_SIZE__ = buffer.byteLength;
      this.target.__INOS_MODULE_ID__ = moduleId;
    }
  }
}

export const createRuntimeModuleLoader = (options?: RuntimeModuleLoaderOptions): RuntimeModuleLoader =>
  new RuntimeModuleLoader(options);
