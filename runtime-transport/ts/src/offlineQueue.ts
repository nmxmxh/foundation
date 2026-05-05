import type { RuntimeEnvelope } from "./index";

export type OfflineConflictResolution = "server-wins" | "client-wins" | "manual";

export type OfflineQueueOptions = {
  maxQueueSize?: number;
  conflictResolution?: OfflineConflictResolution;
};

export type OfflineQueueSnapshot = {
  size: number;
  capacity: number;
  attempts: number;
  oldestQueuedAt: string | null;
};

export type OfflineQueueEntry<TPayload = unknown> = {
  envelope: RuntimeEnvelope<TPayload>;
  queuedAt: string;
  attempts: number;
};

export const createOfflineQueue = (options: OfflineQueueOptions = {}) => {
  const maxQueueSize = Math.max(1, options.maxQueueSize ?? 100);
  const entries: OfflineQueueEntry[] = [];

  return {
    enqueue<TPayload>(envelope: RuntimeEnvelope<TPayload>) {
      if (entries.length >= maxQueueSize) {
        throw new Error(`offline queue capacity exceeded: ${entries.length}/${maxQueueSize}`);
      }
      entries.push({ envelope: envelope as RuntimeEnvelope, queuedAt: new Date().toISOString(), attempts: 0 });
    },
    drain(): OfflineQueueEntry[] {
      return entries.splice(0, entries.length);
    },
    snapshot(): OfflineQueueSnapshot {
      return {
        size: entries.length,
        capacity: maxQueueSize,
        attempts: entries.reduce((total, entry) => total + entry.attempts, 0),
        oldestQueuedAt: entries[0]?.queuedAt ?? null,
      };
    },
    size(): number {
      return entries.length;
    },
    conflictResolution(): OfflineConflictResolution {
      return options.conflictResolution ?? "server-wins";
    },
  };
};
