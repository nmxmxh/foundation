import type {
  RuntimeDispatchRequest,
  RuntimeDispatchResult,
  RuntimeExecutor,
} from "./runtimeDispatcher";

export type RuntimeWorkerMessage = {
  type: "execute";
  id: number;
  library: string;
  method: string;
  params?: RuntimeDispatchRequest["params"];
  input?: Uint8Array | null;
};

export type RuntimeWorkerPoolResponse = {
  type: "result" | "error" | "ready";
  id?: number;
  result?: RuntimeDispatchResult;
  error?: string;
};

export type RuntimeWorkerPoolOptions = {
  maxPendingRequests?: number;
  defaultTimeoutMs?: number;
};

type WorkerRef = {
  id: string;
  worker: Worker;
  ready: boolean;
};

type PendingWorkerRequest = {
  workerId: string;
  resolve: (value: RuntimeDispatchResult) => void;
  reject: (error: Error) => void;
  timeoutId: ReturnType<typeof setTimeout>;
};

const DEFAULT_MAX_PENDING = 256;
const DEFAULT_TIMEOUT_MS = 5000;

export class RuntimeWorkerPool implements RuntimeExecutor {
  private readonly workers = new Map<string, WorkerRef>();
  private readonly pending = new Map<number, PendingWorkerRequest>();
  private nextMessageId = 1;
  private maxPendingRequests: number;
  private defaultTimeoutMs: number;

  constructor(options: RuntimeWorkerPoolOptions = {}) {
    this.maxPendingRequests = Math.max(1, options.maxPendingRequests ?? DEFAULT_MAX_PENDING);
    this.defaultTimeoutMs = Math.max(1, options.defaultTimeoutMs ?? DEFAULT_TIMEOUT_MS);
  }

  addWorker(id: string, worker: Worker, ready = false): void {
    const ref: WorkerRef = { id, worker, ready };
    this.workers.set(id, ref);

    worker.addEventListener("message", (event: MessageEvent<RuntimeWorkerPoolResponse>) => {
      const message = event.data;
      if (message.type === "ready") {
        ref.ready = true;
        return;
      }
      if (message.id === undefined) return;
      this.settle(message.id, message.type === "error" ? new Error(message.error ?? "runtime worker failed") : null, message.result ?? null);
    });

    worker.addEventListener("error", () => {
      this.rejectPendingForWorker(id, new Error(`runtime worker ${id} failed`));
      ref.ready = false;
    });
  }

  removeWorker(id: string): void {
    const ref = this.workers.get(id);
    if (!ref) return;
    this.rejectPendingForWorker(id, new Error(`runtime worker ${id} removed`));
    this.workers.delete(id);
  }

  async execute(request: RuntimeDispatchRequest): Promise<RuntimeDispatchResult> {
    const worker = this.chooseWorker(request);
    if (!worker) {
      throw new Error(`no ready runtime worker for ${request.library}:${request.method}`);
    }
    if (this.pending.size >= this.maxPendingRequests) {
      throw new Error(`runtime worker queue saturated (${this.maxPendingRequests})`);
    }

    const id = this.nextMessageId++;
    const timeoutMs = Math.max(1, request.timeoutMs ?? this.defaultTimeoutMs);
    return await new Promise((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        if (!this.pending.has(id)) return;
        this.pending.delete(id);
        reject(new Error(`runtime worker request ${request.library}:${request.method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(id, { workerId: worker.id, resolve, reject, timeoutId });
      worker.worker.postMessage({
        type: "execute",
        id,
        library: request.library,
        method: request.method,
        params: request.params,
        input: request.input ?? null,
      } satisfies RuntimeWorkerMessage);
    });
  }

  terminate(): void {
    for (const id of this.workers.keys()) {
      this.removeWorker(id);
    }
  }

  private chooseWorker(_request: RuntimeDispatchRequest): WorkerRef | null {
    for (const ref of this.workers.values()) {
      if (ref.ready) return ref;
    }
    return null;
  }

  private settle(
    id: number,
    error: Error | null,
    result: RuntimeDispatchResult
  ): void {
    const pending = this.pending.get(id);
    if (!pending) return;
    clearTimeout(pending.timeoutId);
    this.pending.delete(id);
    if (error) {
      pending.reject(error);
      return;
    }
    pending.resolve(result);
  }

  private rejectPendingForWorker(workerId: string, error: Error): void {
    for (const [id, pending] of this.pending.entries()) {
      if (pending.workerId !== workerId) continue;
      clearTimeout(pending.timeoutId);
      this.pending.delete(id);
      pending.reject(error);
    }
  }
}

export const createRuntimeWorkerPool = (options?: RuntimeWorkerPoolOptions): RuntimeWorkerPool =>
  new RuntimeWorkerPool(options);
