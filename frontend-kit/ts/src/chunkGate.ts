export const DEFAULT_CHUNK_TIMEOUT_MS = 20_000;

/**
 * Route chunks that hang (stalled fetch, cache-first service worker miss)
 * otherwise leave an eternal loader: React caches a pending lazy() promise
 * forever. ChunkLoadError marks the failure as retryable.
 */
export class ChunkLoadError extends Error {
  readonly cause?: unknown;

  constructor(message: string, options?: { cause?: unknown }) {
    super(message);
    this.name = "ChunkLoadError";
    this.cause = options?.cause;
  }
}

const normalizeChunkError = (error: unknown): ChunkLoadError =>
  error instanceof ChunkLoadError ? error : new ChunkLoadError("Route chunk failed to load", { cause: error });

export interface ChunkGate<T> {
  /**
   * Loads the chunk through the timeout guard. Concurrent callers share one
   * attempt. A failed or timed-out attempt clears itself, so the next call
   * starts fresh instead of returning the cached rejection forever.
   */
  load(): Promise<T>;
  /** Forces the next load() to start a new attempt. */
  reset(): void;
}

export const createChunkGate = <T>(
  load: () => Promise<T>,
  timeoutMs: number = DEFAULT_CHUNK_TIMEOUT_MS
): ChunkGate<T> => {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new Error(`chunkGate: timeoutMs must be positive and finite, got ${timeoutMs}`);
  }

  let inflight: Promise<T> | null = null;

  const attempt = (): Promise<T> => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    return new Promise<T>((resolve, reject) => {
      timer = setTimeout(
        () => reject(new ChunkLoadError(`Route chunk did not load within ${timeoutMs}ms`)),
        timeoutMs
      );
      load().then(
        (value) => {
          clearTimeout(timer);
          resolve(value);
        },
        (cause) => {
          clearTimeout(timer);
          reject(normalizeChunkError(cause));
        }
      );
    });
  };

  return {
    load: () => {
      if (!inflight) {
        inflight = attempt().catch((error: unknown) => {
          inflight = null;
          throw error;
        });
      }
      return inflight;
    },
    reset: () => {
      inflight = null;
    },
  };
};
