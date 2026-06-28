import { useMemo, useSyncExternalStore } from "react";

/**
 * Client mirror of the server-kit `transfer` package progress lane.
 *
 * The fact lane (durable `<domain>:<action>:requested|success|failed` events) is
 * consumed by server-side projections. The browser instead subscribes to the
 * ephemeral progress lane and reconciles a monotonic snapshot from it. Both
 * lanes are joined by the same `correlationId`/`transferId`.
 */

export type TransferPhase =
  | "pending"
  | "uploading"
  | "staged"
  | "processing"
  | "ready"
  | "failed"
  | "aborted";

const TERMINAL_PHASES: ReadonlySet<TransferPhase> = new Set<TransferPhase>([
  "ready",
  "failed",
  "aborted",
]);

/** Wire shape of a single progress-lane update (snake_case, matching the Go JSON tags). */
export interface TransferUpdate {
  transfer_id: string;
  correlation_id: string;
  phase: TransferPhase;
  bytes_done: number;
  bytes_total: number; // 0 means unknown.
  checksum?: string;
  reason?: string;
  seq: number;
  timestamp?: string;
}

/** Reconciled, UI-friendly snapshot derived from the stream of updates. */
export interface TransferSnapshot {
  transferId: string;
  phase: TransferPhase;
  bytesDone: number;
  bytesTotal: number;
  /** Completion in [0,1]; 0 when total is unknown, 1 once ready. */
  fraction: number;
  checksum?: string;
  /** Populated from `reason` when the transfer failed or was aborted. */
  error?: string;
  terminal: boolean;
  /** Highest server sequence applied; -1 before any update. */
  seq: number;
}

/**
 * TransferProgressSource is the transport seam. An app wires this to its
 * WebSocket / runtime-transport progress channel; the hook stays transport
 * agnostic. `subscribe` must return an unsubscribe function.
 */
export interface TransferProgressSource {
  subscribe(transferId: string, onUpdate: (update: TransferUpdate) => void): () => void;
}

export const isTerminalPhase = (phase: TransferPhase): boolean => TERMINAL_PHASES.has(phase);

/** Mirrors Go `Update.Fraction`: ready => 1, unknown/over => clamped. */
export const computeFraction = (phase: TransferPhase, bytesDone: number, bytesTotal: number): number => {
  if (phase === "ready") return 1;
  if (bytesTotal <= 0) return 0;
  if (bytesDone >= bytesTotal) return 1;
  return bytesDone / bytesTotal;
};

export const initialTransferSnapshot = (transferId: string): TransferSnapshot => ({
  transferId,
  phase: "pending",
  bytesDone: 0,
  bytesTotal: 0,
  fraction: 0,
  terminal: false,
  seq: -1,
});

/**
 * reduceTransfer applies one update with the progress lane's contract: updates
 * are coalescible and may arrive out of order, so a stale or duplicate `seq` is
 * dropped and byte progress never regresses. The same object is returned when
 * nothing changed so `useSyncExternalStore` can skip re-renders.
 */
export const reduceTransfer = (prev: TransferSnapshot, update: TransferUpdate): TransferSnapshot => {
  if (update.transfer_id !== prev.transferId) return prev;
  // Once settled, ignore everything (terminal is a sink, as on the server).
  if (prev.terminal) return prev;
  // Drop stale/duplicate sequences.
  if (update.seq <= prev.seq) return prev;

  const bytesDone = Math.max(prev.bytesDone, update.bytes_done);
  const bytesTotal = update.bytes_total > 0 ? Math.max(prev.bytesTotal, update.bytes_total) : prev.bytesTotal;
  const phase = update.phase;

  return {
    transferId: prev.transferId,
    phase,
    bytesDone,
    bytesTotal,
    fraction: computeFraction(phase, bytesDone, bytesTotal),
    checksum: update.checksum ?? prev.checksum,
    error: phase === "failed" || phase === "aborted" ? update.reason ?? prev.error : prev.error,
    terminal: isTerminalPhase(phase),
    seq: update.seq,
  };
};

export interface TransferStore {
  subscribe(listener: () => void): () => void;
  getSnapshot(): TransferSnapshot;
}

/**
 * createTransferStore is a framework-agnostic external store: it owns the
 * reconciled snapshot, lazily subscribes to the source while it has listeners,
 * and tears the subscription down when the last listener leaves.
 */
export const createTransferStore = (source: TransferProgressSource, transferId: string): TransferStore => {
  let snapshot = initialTransferSnapshot(transferId);
  const listeners = new Set<() => void>();
  let unsubscribeSource: (() => void) | null = null;

  const apply = (update: TransferUpdate): void => {
    const next = reduceTransfer(snapshot, update);
    if (next === snapshot) return;
    snapshot = next;
    listeners.forEach((listener) => listener());
  };

  const ensureSourceSubscription = (): void => {
    if (unsubscribeSource || listeners.size === 0) return;
    unsubscribeSource = source.subscribe(transferId, apply);
  };

  const maybeTeardown = (): void => {
    if (listeners.size > 0 || !unsubscribeSource) return;
    unsubscribeSource();
    unsubscribeSource = null;
  };

  return {
    subscribe(listener: () => void): () => void {
      listeners.add(listener);
      ensureSourceSubscription();
      return () => {
        listeners.delete(listener);
        maybeTeardown();
      };
    },
    getSnapshot(): TransferSnapshot {
      return snapshot;
    },
  };
};

/**
 * useTransfer subscribes to a transfer's progress lane and returns a monotonic,
 * reconciled snapshot suitable for rendering a progress bar and terminal state.
 */
export const useTransfer = (source: TransferProgressSource, transferId: string): TransferSnapshot => {
  const store = useMemo(() => createTransferStore(source, transferId), [source, transferId]);
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
};
