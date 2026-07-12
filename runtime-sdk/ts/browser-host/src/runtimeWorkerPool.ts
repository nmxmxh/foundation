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
  inFlight: number;
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
  private nextWorkerOffset = 0;

  constructor(options: RuntimeWorkerPoolOptions = {}) {
    this.maxPendingRequests = Math.max(1, options.maxPendingRequests ?? DEFAULT_MAX_PENDING);
    this.defaultTimeoutMs = Math.max(1, options.defaultTimeoutMs ?? DEFAULT_TIMEOUT_MS);
  }

  addWorker(id: string, worker: Worker, ready = false): void {
    const ref: WorkerRef = { id, worker, ready, inFlight: 0 };
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
        this.decrementInFlight(worker.id);
        reject(new Error(`runtime worker request ${request.library}:${request.method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(id, { workerId: worker.id, resolve, reject, timeoutId });
      worker.inFlight += 1;
      try {
        worker.worker.postMessage({
          type: "execute",
          id,
          library: request.library,
          method: request.method,
          params: request.params,
          input: request.input ?? null,
        } satisfies RuntimeWorkerMessage);
      } catch (error) {
        clearTimeout(timeoutId);
        this.pending.delete(id);
        worker.inFlight -= 1;
        reject(error instanceof Error ? error : new Error(String(error)));
      }
    });
  }

  terminate(): void {
    for (const id of this.workers.keys()) {
      this.removeWorker(id);
    }
  }

  private chooseWorker(_request: RuntimeDispatchRequest): WorkerRef | null {
    const ready = Array.from(this.workers.values()).filter((ref) => ref.ready);
    if (ready.length === 0) return null;
    const start = this.nextWorkerOffset % ready.length;
    let selected = ready[start];
    for (let offset = 1; offset < ready.length; offset += 1) {
      const candidate = ready[(start + offset) % ready.length];
      if (candidate.inFlight < selected.inFlight) {
        selected = candidate;
      }
    }
    this.nextWorkerOffset = (ready.indexOf(selected) + 1) % ready.length;
    return selected;
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
    this.decrementInFlight(pending.workerId);
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
      this.decrementInFlight(workerId);
      pending.reject(error);
    }
  }

  private decrementInFlight(workerId: string): void {
    const worker = this.workers.get(workerId);
    if (worker && worker.inFlight > 0) {
      worker.inFlight -= 1;
    }
  }
}

export const createRuntimeWorkerPool = (options?: RuntimeWorkerPoolOptions): RuntimeWorkerPool =>
  new RuntimeWorkerPool(options);
