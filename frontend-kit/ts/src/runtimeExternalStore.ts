import { useCallback, useSyncExternalStore } from "react";

export type RuntimeEpochListener = (epoch: number, previousEpoch: number) => void;

export interface RuntimeEpochSource {
  isReady(): boolean;
  subscribeEpoch(index: number, listener: RuntimeEpochListener, options?: { emitInitial?: boolean; pollMs?: number }): () => void;
}

export interface RuntimeRegionSource extends RuntimeEpochSource {
  getRegionDataView(regionOffset: number, regionSize: number): DataView | null;
  atomicLoad?(index: number): number;
}

export interface RuntimeExternalStoreOptions<TSnapshot> {
  source: RuntimeEpochSource;
  epochIndexes: readonly number[];
  readSnapshot: () => TSnapshot;
  emptySnapshot: TSnapshot;
  equals?: (left: TSnapshot, right: TSnapshot) => boolean;
  pollMs?: number;
}

export interface RuntimeExternalStore<TSnapshot> {
  subscribe(listener: () => void): () => void;
  getSnapshot(): TSnapshot;
  useSnapshot(): TSnapshot;
  sync(): void;
}

const objectIs = <T>(left: T, right: T): boolean => Object.is(left, right);

export const createRuntimeExternalStore = <TSnapshot>({
  source,
  epochIndexes,
  readSnapshot,
  emptySnapshot,
  equals = objectIs,
  pollMs = 32,
}: RuntimeExternalStoreOptions<TSnapshot>): RuntimeExternalStore<TSnapshot> => {
  let snapshot = emptySnapshot;
  let activeSubscriptions = 0;
  let epochUnsubscribers: Array<() => void> = [];
  const listeners = new Set<() => void>();

  const emit = (next: TSnapshot) => {
    if (equals(snapshot, next)) return;
    snapshot = next;
    listeners.forEach((listener) => listener());
  };

  const sync = () => {
    try {
      if (!source.isReady()) {
        emit(emptySnapshot);
        return;
      }
      emit(readSnapshot());
    } catch {
      emit(emptySnapshot);
    }
  };

  const start = () => {
    if (epochUnsubscribers.length > 0) return;
    sync();
    epochUnsubscribers = epochIndexes.map((index) =>
      source.subscribeEpoch(index, sync, { emitInitial: true, pollMs })
    );
  };

  const stop = () => {
    epochUnsubscribers.forEach((unsubscribe) => unsubscribe());
    epochUnsubscribers = [];
  };

  const subscribe = (listener: () => void) => {
    activeSubscriptions += 1;
    listeners.add(listener);
    start();

    return () => {
      listeners.delete(listener);
      activeSubscriptions = Math.max(0, activeSubscriptions - 1);
      if (activeSubscriptions === 0) stop();
    };
  };

  const getSnapshot = () => snapshot;
  const useSnapshot = () => useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  return { subscribe, getSnapshot, useSnapshot, sync };
};

export const readRegion = (
  source: Pick<RuntimeRegionSource, "getRegionDataView">,
  regionOffset: number,
  regionSize: number
): DataView | null => source.getRegionDataView(regionOffset, regionSize);

/**
 * useRuntimePeek provides a zero-copy "peek" into the runtime memory.
 * It subscribes to epoch changes to trigger re-renders, but allows
 * the component to read directly from the DataView without intermediate snapshots.
 */
export const useRuntimePeek = <T>(
  source: RuntimeRegionSource,
  epochIndexes: readonly number[],
  peek: (source: RuntimeRegionSource) => T
): T => {
  const subscribe = useCallback(
    (listener: () => void) => {
      const unsubs = epochIndexes.map((index) =>
        source.subscribeEpoch(index, listener)
      );
      return () => unsubs.forEach((unsub) => unsub());
    },
    [source, epochIndexes]
  );

  // We use useSyncExternalStore to trigger re-renders, but we don't 
  // actually store the snapshot in the store's internal state if we
  // want to stay zero-copy. The "peek" function is called during render.
  useSyncExternalStore(
    subscribe,
    () => source.atomicLoad?.(epochIndexes[0] || 0) || 0,
    () => 0
  );

  return peek(source);
};
