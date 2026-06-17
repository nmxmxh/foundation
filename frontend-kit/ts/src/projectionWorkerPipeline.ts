import {
  normalizeHermesProjectionMutation,
  normalizeProjectionLoadResult,
  type ProjectionLoadResult,
  type ProjectionMutation,
  type ProjectionScope,
} from "./runtimeWorkbench";

export type ProjectionWorkerMessage =
  | {
      id: number;
      type: "normalizeMutations";
      scope: ProjectionScope;
      events: readonly unknown[];
    }
  | {
      id: number;
      type: "normalizeLoadResult";
      scope: ProjectionScope;
      input: unknown;
    };

export type ProjectionWorkerResponse =
  | {
      id: number;
      type: "mutations";
      mutations: readonly ProjectionMutation<Record<string, unknown>>[];
    }
  | {
      id: number;
      type: "loadResult";
      result: ProjectionLoadResult<Record<string, unknown>>;
    }
  | {
      id: number;
      type: "error";
      error: string;
    };

export type ProjectionWorkerLike = {
  postMessage(message: ProjectionWorkerMessage): void;
  addEventListener(
    type: "message",
    listener: (event: MessageEvent<ProjectionWorkerResponse>) => void
  ): void;
  removeEventListener?(
    type: "message",
    listener: (event: MessageEvent<ProjectionWorkerResponse>) => void
  ): void;
  terminate?(): void;
};

export type ProjectionWorkerTarget = {
  postMessage(message: ProjectionWorkerResponse): void;
  addEventListener(
    type: "message",
    listener: (event: MessageEvent<ProjectionWorkerMessage>) => void
  ): void;
};

export type ProjectionNormalizer = {
  normalizeMutations<TRecord extends Record<string, unknown>>(
    events: readonly unknown[],
    scope: ProjectionScope
  ): Promise<ProjectionMutation<TRecord>[]>;
  normalizeLoadResult<TRecord extends Record<string, unknown>>(
    input: unknown,
    scope: ProjectionScope
  ): Promise<ProjectionLoadResult<TRecord>>;
  getSnapshot(): ProjectionWorkerPipelineSnapshot;
  close(): void;
};

export type ProjectionWorkerPipelineSnapshot = {
  pendingRequests: number;
  processedBatches: number;
  processedEvents: number;
  fallbackRuns: number;
  errors: number;
  closed: boolean;
};

export type ProjectionWorkerNormalizerOptions = {
  maxPendingRequests?: number;
  timeoutMs?: number;
  fallback?: "local" | "fail";
};

export type ProjectionEventPipelineOptions = {
  maxBatchSize?: number;
  maxQueuedEvents?: number;
  flushIntervalMs?: number;
};

export type ProjectionEventPipeline<TRecord extends Record<string, unknown>> = {
  push(event: unknown): boolean;
  flush(): Promise<void>;
  getSnapshot(): ProjectionEventPipelineSnapshot;
  close(): void;
  readonly _recordType?: TRecord;
};

export type ProjectionEventPipelineSnapshot = {
  queuedEvents: number;
  processedEvents: number;
  droppedEvents: number;
  processedBatches: number;
  flushing: boolean;
  closed: boolean;
};

type PendingRequest = {
  kind: ProjectionWorkerMessage["type"];
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timeoutId: ReturnType<typeof setTimeout>;
  fallback: () => unknown;
};

type ProjectionWorkerRequest =
  | Omit<Extract<ProjectionWorkerMessage, { type: "normalizeMutations" }>, "id">
  | Omit<Extract<ProjectionWorkerMessage, { type: "normalizeLoadResult" }>, "id">;

const DEFAULT_MAX_PENDING_REQUESTS = 64;
const DEFAULT_WORKER_TIMEOUT_MS = 100;

const localNormalizeMutations = <TRecord extends Record<string, unknown>>(
  events: readonly unknown[],
  scope: ProjectionScope
): ProjectionMutation<TRecord>[] =>
  events
    .map((event) => normalizeHermesProjectionMutation<TRecord>(event, scope))
    .filter((mutation): mutation is ProjectionMutation<TRecord> => Boolean(mutation));

export const createLocalProjectionNormalizer = (): ProjectionNormalizer => {
  let processedBatches = 0;
  let processedEvents = 0;
  let closed = false;

  return {
    async normalizeMutations<TRecord extends Record<string, unknown>>(
      events: readonly unknown[],
      scope: ProjectionScope
    ): Promise<ProjectionMutation<TRecord>[]> {
      processedBatches += 1;
      processedEvents += events.length;
      return localNormalizeMutations<TRecord>(events, scope);
    },
    async normalizeLoadResult<TRecord extends Record<string, unknown>>(
      input: unknown,
      scope: ProjectionScope
    ): Promise<ProjectionLoadResult<TRecord>> {
      processedBatches += 1;
      return normalizeProjectionLoadResult<TRecord>(input, scope);
    },
    getSnapshot(): ProjectionWorkerPipelineSnapshot {
      return {
        closed,
        errors: 0,
        fallbackRuns: 0,
        pendingRequests: 0,
        processedBatches,
        processedEvents,
      };
    },
    close(): void {
      closed = true;
    },
  };
};

export const createProjectionWorkerNormalizer = (
  worker: ProjectionWorkerLike | undefined,
  options: ProjectionWorkerNormalizerOptions = {}
): ProjectionNormalizer => {
  const local = createLocalProjectionNormalizer();
  if (!worker) return local;

  const maxPendingRequests = Math.max(1, options.maxPendingRequests ?? DEFAULT_MAX_PENDING_REQUESTS);
  const timeoutMs = Math.max(1, options.timeoutMs ?? DEFAULT_WORKER_TIMEOUT_MS);
  const fallback = options.fallback ?? "local";
  const pending = new Map<number, PendingRequest>();
  let nextId = 1;
  let processedBatches = 0;
  let processedEvents = 0;
  let fallbackRuns = 0;
  let errors = 0;
  let closed = false;

  const runFallback = async <T>(compute: () => T): Promise<T> => {
    fallbackRuns += 1;
    return compute();
  };

  const settle = (id: number, value: unknown, error?: Error) => {
    const request = pending.get(id);
    if (!request) return;
    clearTimeout(request.timeoutId);
    pending.delete(id);
    if (error) {
      errors += 1;
      if (fallback === "local") {
        request.resolve(runFallback(request.fallback));
        return;
      }
      request.reject(error);
      return;
    }
    request.resolve(value);
  };

  const onMessage = (event: MessageEvent<ProjectionWorkerResponse>) => {
    const message = event.data;
    if (!message || typeof message.id !== "number") return;
    if (message.type === "error") {
      settle(message.id, undefined, new Error(message.error));
      return;
    }
    if (message.type === "mutations") {
      settle(message.id, message.mutations);
      return;
    }
    settle(message.id, message.result);
  };

  worker.addEventListener("message", onMessage);

  const request = async <T>(
    message: ProjectionWorkerRequest,
    fallbackCompute: () => T
  ): Promise<T> => {
    if (closed || pending.size >= maxPendingRequests) {
      if (fallback === "local") return runFallback(fallbackCompute);
      throw new Error(`projection worker queue saturated (${maxPendingRequests})`);
    }

    const id = nextId;
    nextId += 1;
    return await new Promise<T>((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        settle(id, undefined, new Error(`projection worker request timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      pending.set(id, {
        fallback: fallbackCompute,
        kind: message.type,
        reject,
        resolve: resolve as (value: unknown) => void,
        timeoutId,
      });
      worker.postMessage({ ...message, id } as ProjectionWorkerMessage);
    });
  };

  return {
    async normalizeMutations<TRecord extends Record<string, unknown>>(
      events: readonly unknown[],
      scope: ProjectionScope
    ): Promise<ProjectionMutation<TRecord>[]> {
      processedBatches += 1;
      processedEvents += events.length;
      return await request(
        { events, scope, type: "normalizeMutations" },
        () => localNormalizeMutations<TRecord>(events, scope)
      );
    },
    async normalizeLoadResult<TRecord extends Record<string, unknown>>(
      input: unknown,
      scope: ProjectionScope
    ): Promise<ProjectionLoadResult<TRecord>> {
      processedBatches += 1;
      return await request(
        { input, scope, type: "normalizeLoadResult" },
        () => normalizeProjectionLoadResult<TRecord>(input, scope)
      );
    },
    getSnapshot(): ProjectionWorkerPipelineSnapshot {
      return {
        closed,
        errors,
        fallbackRuns,
        pendingRequests: pending.size,
        processedBatches,
        processedEvents,
      };
    },
    close(): void {
      closed = true;
      worker.removeEventListener?.("message", onMessage);
      for (const [id, requestState] of pending.entries()) {
        clearTimeout(requestState.timeoutId);
        pending.delete(id);
        requestState.reject(new Error("projection worker normalizer closed"));
      }
      worker.terminate?.();
      local.close();
    },
  };
};

export const installProjectionWorkerHandler = (target: ProjectionWorkerTarget): void => {
  target.addEventListener("message", (event: MessageEvent<ProjectionWorkerMessage>) => {
    const message = event.data;
    try {
      if (message.type === "normalizeMutations") {
        target.postMessage({
          id: message.id,
          mutations: localNormalizeMutations(message.events, message.scope),
          type: "mutations",
        });
        return;
      }
      target.postMessage({
        id: message.id,
        result: normalizeProjectionLoadResult(message.input, message.scope),
        type: "loadResult",
      });
    } catch (error) {
      target.postMessage({
        error: error instanceof Error ? error.message : String(error),
        id: message.id,
        type: "error",
      });
    }
  });
};

const scheduleEventPipelineFlush = (run: () => void, delayMs: number): (() => void) => {
  if (delayMs <= 0 && typeof queueMicrotask === "function") {
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) run();
    });
    return () => {
      cancelled = true;
    };
  }
  const timeoutId = setTimeout(run, Math.max(0, delayMs));
  return () => clearTimeout(timeoutId);
};

export const createProjectionEventPipeline = <TRecord extends Record<string, unknown>>(
  scope: ProjectionScope,
  normalizer: ProjectionNormalizer,
  listener: (mutation: ProjectionMutation<TRecord>) => void,
  options: ProjectionEventPipelineOptions = {}
): ProjectionEventPipeline<TRecord> => {
  const maxBatchSize = Math.max(1, options.maxBatchSize ?? 128);
  const maxQueuedEvents = Math.max(1, options.maxQueuedEvents ?? 4096);
  const flushIntervalMs = Math.max(0, options.flushIntervalMs ?? 0);
  const queue: unknown[] = [];
  let scheduledCancel: (() => void) | undefined;
  let processedEvents = 0;
  let droppedEvents = 0;
  let processedBatches = 0;
  let flushing = false;
  let closed = false;

  const scheduleFlush = (flush: () => void) => {
    if (scheduledCancel || flushing || closed) return;
    scheduledCancel = scheduleEventPipelineFlush(flush, flushIntervalMs);
  };

  const pipeline: ProjectionEventPipeline<TRecord> = {
    push(event: unknown): boolean {
      if (closed || queue.length >= maxQueuedEvents) {
        droppedEvents += 1;
        return false;
      }
      queue.push(event);
      if (queue.length >= maxBatchSize) {
        scheduledCancel?.();
        scheduledCancel = undefined;
        void pipeline.flush();
        return true;
      }
      scheduleFlush(() => {
        void pipeline.flush();
      });
      return true;
    },
    async flush(): Promise<void> {
      if (closed || flushing || queue.length === 0) return;
      scheduledCancel = undefined;
      flushing = true;
      const batch = queue.splice(0, maxBatchSize);
      try {
        processedBatches += 1;
        processedEvents += batch.length;
        const mutations = await normalizer.normalizeMutations<TRecord>(batch, scope);
        if (!closed) mutations.forEach(listener);
      } finally {
        flushing = false;
        if (!closed && queue.length > 0) {
          scheduleFlush(() => {
            void pipeline.flush();
          });
        }
      }
    },
    getSnapshot(): ProjectionEventPipelineSnapshot {
      return {
        closed,
        droppedEvents,
        flushing,
        processedBatches,
        processedEvents,
        queuedEvents: queue.length,
      };
    },
    close(): void {
      closed = true;
      scheduledCancel?.();
      scheduledCancel = undefined;
      queue.length = 0;
    },
  };

  return pipeline;
};
