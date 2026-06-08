export type RuntimeModuleExports = WebAssembly.Exports & {
  memory?: WebAssembly.Memory;
  compute_alloc?: (size: number) => number;
  compute_free?: (ptr: number, size: number) => void;
  compute_execute?: (
    servicePtr: number,
    serviceLen: number,
    actionPtr: number,
    actionLen: number,
    inputPtr: number,
    inputLen: number,
    paramsPtr: number,
    paramsLen: number
  ) => number;
  compute_dispatch?: (requestPtr: number, requestLen: number) => number;
};

export type RuntimeDispatchParams = Uint8Array | string | null;
export type RuntimeDispatchResult = Uint8Array | string | null;

export type RuntimeDispatchRequest = {
  library: string;
  method: string;
  params?: RuntimeDispatchParams;
  input?: Uint8Array | null;
  timeoutMs?: number;
};

export type RuntimeDispatchMetrics = {
  pending: number;
  maxPending: number;
  completed: number;
  rejected: number;
  timedOut: number;
  saturated: number;
};

export interface RuntimeExecutor {
  execute(request: RuntimeDispatchRequest): Promise<RuntimeDispatchResult>;
}

export type RuntimeDispatcherOptions = {
  maxPendingRequests?: number;
  defaultTimeoutMs?: number;
};

type PendingRequest = {
  reject: (error: Error) => void;
  timeoutId: ReturnType<typeof setTimeout>;
};

const encoder = new TextEncoder();

const DEFAULT_MAX_PENDING = 256;
const DEFAULT_TIMEOUT_MS = 5000;
const MAX_ROUTE_PART_BYTES = 64;

const encodePayload = (value: RuntimeDispatchRequest["params"]): Uint8Array => {
  if (value instanceof Uint8Array) return value;
  if (typeof value === "string") return encoder.encode(value);
  return new Uint8Array(0);
};

const validateRoutePart = (label: string, value: string): void => {
  if (!value || encoder.encode(value).byteLength > MAX_ROUTE_PART_BYTES) {
    throw new Error(`${label} must be 1-${MAX_ROUTE_PART_BYTES} bytes`);
  }
  if (!/^[A-Za-z0-9_.:-]+$/.test(value)) {
    throw new Error(`${label} contains unsupported characters`);
  }
};

export class RuntimeDispatcher {
  private moduleExports: RuntimeModuleExports | null = null;
  private memory: WebAssembly.Memory | null = null;
  private readonly capabilities = new Map<string, Set<string>>();
  private readonly executors = new Map<string, RuntimeExecutor>();
  private maxPendingRequests: number;
  private defaultTimeoutMs: number;
  private nextRequestId = 1;
  private readonly pending = new Map<number, PendingRequest>();
  private readonly metrics: RuntimeDispatchMetrics = {
    pending: 0,
    maxPending: 0,
    completed: 0,
    rejected: 0,
    timedOut: 0,
    saturated: 0,
  };

  constructor(options: RuntimeDispatcherOptions = {}) {
    this.maxPendingRequests = Math.max(1, options.maxPendingRequests ?? DEFAULT_MAX_PENDING);
    this.defaultTimeoutMs = Math.max(1, options.defaultTimeoutMs ?? DEFAULT_TIMEOUT_MS);
  }

  initialize(moduleExports: RuntimeModuleExports, memory?: WebAssembly.Memory): void {
    this.moduleExports = moduleExports;
    this.memory = memory ?? moduleExports.memory ?? null;
  }

  configure(options: RuntimeDispatcherOptions): void {
    if (options.maxPendingRequests !== undefined) {
      this.maxPendingRequests = Math.max(1, options.maxPendingRequests);
    }
    if (options.defaultTimeoutMs !== undefined) {
      this.defaultTimeoutMs = Math.max(1, options.defaultTimeoutMs);
    }
  }

  registerUnit(unit: string, methods: readonly string[]): void {
    this.capabilities.set(unit, new Set(methods));
  }

  registerCapability(capability: string): void {
    const [unit, method] = capability.split(":");
    if (!unit || !method) return;
    const methods = this.capabilities.get(unit) ?? new Set<string>();
    methods.add(method);
    this.capabilities.set(unit, methods);
  }

  bindExecutor(id: string, executor: RuntimeExecutor): void {
    this.executors.set(id, executor);
  }

  unbindExecutor(id: string): void {
    this.executors.delete(id);
  }

  hasCapability(unit: string, method?: string): boolean {
    const methods = this.capabilities.get(unit);
    if (!methods) return false;
    return method === undefined || methods.has(method) || methods.has(`${unit}:${method}`);
  }

  async execute(request: RuntimeDispatchRequest): Promise<RuntimeDispatchResult> {
    validateRoutePart("library", request.library);
    validateRoutePart("method", request.method);

    if (this.pending.size >= this.maxPendingRequests) {
      this.metrics.saturated += 1;
      throw new Error(`runtime dispatcher pending queue saturated (${this.maxPendingRequests})`);
    }

    const requestId = this.nextRequestId++;
    const timeoutMs = Math.max(1, request.timeoutMs ?? this.defaultTimeoutMs);

    return await new Promise((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        if (!this.pending.has(requestId)) return;
        this.pending.delete(requestId);
        this.refreshPendingMetrics();
        this.metrics.timedOut += 1;
        reject(new Error(`runtime dispatch ${request.library}:${request.method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);

      this.pending.set(requestId, { reject, timeoutId });
      this.refreshPendingMetrics();

      void this.executeNow(request)
        .then((result) => {
          this.settle(requestId);
          this.metrics.completed += 1;
          resolve(result);
        })
        .catch((error: unknown) => {
          this.settle(requestId);
          this.metrics.rejected += 1;
          reject(error instanceof Error ? error : new Error(String(error)));
        });
    });
  }

  executeSync(request: RuntimeDispatchRequest): Uint8Array | null {
    validateRoutePart("library", request.library);
    validateRoutePart("method", request.method);
    return this.executeExported(request);
  }

  async decode<T>(
    request: RuntimeDispatchRequest,
    decodeResult: (result: RuntimeDispatchResult) => T | null
  ): Promise<T | null> {
    const result = await this.execute(request);
    return decodeResult(result);
  }

  getMetrics(): RuntimeDispatchMetrics {
    this.refreshPendingMetrics();
    return { ...this.metrics };
  }

  private async executeNow(request: RuntimeDispatchRequest): Promise<RuntimeDispatchResult> {
    const executor = this.findExecutor(request.library, request.method);
    if (executor) {
      return await executor.execute(request);
    }
    return this.executeExported(request);
  }

  private findExecutor(library: string, method: string): RuntimeExecutor | undefined {
    return (
      this.executors.get(`${library}:${method}`) ??
      this.executors.get(library) ??
      this.executors.get("compute")
    );
  }

  private executeExported(request: RuntimeDispatchRequest): Uint8Array | null {
    const exports = this.moduleExports;
    const memory = this.memory;
    if (!exports?.compute_execute || !exports.compute_alloc || !memory) {
      throw new Error("runtime compute_execute export is unavailable");
    }

    const libraryBytes = encoder.encode(request.library);
    const methodBytes = encoder.encode(request.method);
    const paramsBytes = encodePayload(request.params);
    const input = request.input ?? null;
    const libraryPtr = exports.compute_alloc(libraryBytes.length);
    const methodPtr = exports.compute_alloc(methodBytes.length);
    const paramsPtr = paramsBytes.byteLength > 0 ? exports.compute_alloc(paramsBytes.length) : 0;
    const inputPtr = input ? exports.compute_alloc(input.byteLength) : 0;

    const heap = new Uint8Array(memory.buffer);
    heap.set(libraryBytes, libraryPtr);
    heap.set(methodBytes, methodPtr);
    if (paramsPtr !== 0) {
      heap.set(paramsBytes, paramsPtr);
    }
    if (input && inputPtr !== 0) {
      heap.set(input, inputPtr);
    }

    try {
      const resultPtr = exports.compute_execute(
        libraryPtr,
        libraryBytes.length,
        methodPtr,
        methodBytes.length,
        inputPtr,
        input?.byteLength ?? 0,
        paramsPtr,
        paramsBytes.length
      );
      if (resultPtr === 0) return null;
      const resultView = new DataView(memory.buffer);
      const outputLen = resultView.getUint32(resultPtr, true);
      if (outputLen > memory.buffer.byteLength - resultPtr - 4) {
        throw new Error(`runtime result length exceeds memory buffer: ${outputLen}`);
      }
      const output = new Uint8Array(memory.buffer, resultPtr + 4, outputLen).slice();
      exports.compute_free?.(resultPtr, outputLen + 4);
      return output;
    } finally {
      exports.compute_free?.(libraryPtr, libraryBytes.length);
      exports.compute_free?.(methodPtr, methodBytes.length);
      if (paramsPtr !== 0) {
        exports.compute_free?.(paramsPtr, paramsBytes.length);
      }
      if (input && inputPtr !== 0) {
        exports.compute_free?.(inputPtr, input.byteLength);
      }
    }
  }

  private settle(requestId: number): void {
    const pending = this.pending.get(requestId);
    if (!pending) return;
    clearTimeout(pending.timeoutId);
    this.pending.delete(requestId);
    this.refreshPendingMetrics();
  }

  private refreshPendingMetrics(): void {
    this.metrics.pending = this.pending.size;
    this.metrics.maxPending = Math.max(this.metrics.maxPending, this.pending.size);
  }
}

export const createRuntimeDispatcher = (options?: RuntimeDispatcherOptions): RuntimeDispatcher =>
  new RuntimeDispatcher(options);
