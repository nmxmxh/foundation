import { describe, expect, it, vi } from "vitest";

import {
  computeFraction,
  createTransferStore,
  initialTransferSnapshot,
  isTerminalPhase,
  reduceTransfer,
  type TransferPhase,
  type TransferProgressSource,
  type TransferUpdate,
} from "./transferProgress";

const update = (over: Partial<TransferUpdate> = {}): TransferUpdate => ({
  transfer_id: "tx-1",
  correlation_id: "corr-1",
  phase: "uploading",
  bytes_done: 0,
  bytes_total: 100,
  seq: 0,
  ...over,
});

describe("isTerminalPhase", () => {
  it("classifies terminal vs non-terminal phases", () => {
    for (const phase of ["ready", "failed", "aborted"] as TransferPhase[]) {
      expect(isTerminalPhase(phase)).toBe(true);
    }
    for (const phase of ["pending", "uploading", "staged", "processing"] as TransferPhase[]) {
      expect(isTerminalPhase(phase)).toBe(false);
    }
  });
});

describe("computeFraction (server parity)", () => {
  it("matches the Go Update.Fraction contract", () => {
    expect(computeFraction("uploading", 10, 0)).toBe(0); // unknown total
    expect(computeFraction("uploading", 50, 100)).toBe(0.5);
    expect(computeFraction("uploading", 150, 100)).toBe(1); // clamp
    expect(computeFraction("ready", 0, 0)).toBe(1); // ready always complete
    expect(computeFraction("uploading", 0, 100)).toBe(0);
  });
});

describe("reduceTransfer", () => {
  it("advances bytes, phase, and fraction on a fresh update", () => {
    const next = reduceTransfer(initialTransferSnapshot("tx-1"), update({ bytes_done: 40, seq: 0 }));
    expect(next.phase).toBe("uploading");
    expect(next.bytesDone).toBe(40);
    expect(next.fraction).toBeCloseTo(0.4);
    expect(next.seq).toBe(0);
  });

  it("ignores updates for a different transfer id", () => {
    const prev = initialTransferSnapshot("tx-1");
    expect(reduceTransfer(prev, update({ transfer_id: "other", seq: 5 }))).toBe(prev);
  });

  it("drops stale or duplicate sequences and returns the same reference", () => {
    const first = reduceTransfer(initialTransferSnapshot("tx-1"), update({ bytes_done: 50, seq: 2 }));
    expect(reduceTransfer(first, update({ bytes_done: 90, seq: 2 }))).toBe(first); // duplicate
    expect(reduceTransfer(first, update({ bytes_done: 90, seq: 1 }))).toBe(first); // stale
  });

  it("never regresses byte progress even if an update reports fewer bytes", () => {
    const first = reduceTransfer(initialTransferSnapshot("tx-1"), update({ bytes_done: 80, seq: 1 }));
    const next = reduceTransfer(first, update({ bytes_done: 10, seq: 2 }));
    expect(next.bytesDone).toBe(80);
  });

  it("raises an unknown total when a later update declares one", () => {
    const first = reduceTransfer(initialTransferSnapshot("tx-1"), update({ bytes_total: 0, bytes_done: 10, seq: 1 }));
    expect(first.bytesTotal).toBe(0);
    const next = reduceTransfer(first, update({ bytes_total: 200, bytes_done: 20, seq: 2 }));
    expect(next.bytesTotal).toBe(200);
  });

  it("captures checksum on success and surfaces reason on failure", () => {
    const ready = reduceTransfer(
      initialTransferSnapshot("tx-1"),
      update({ phase: "ready", bytes_done: 100, checksum: "abc", seq: 3 }),
    );
    expect(ready.terminal).toBe(true);
    expect(ready.checksum).toBe("abc");
    expect(ready.fraction).toBe(1);

    const failed = reduceTransfer(
      initialTransferSnapshot("tx-1"),
      update({ phase: "failed", reason: "boom", seq: 1 }),
    );
    expect(failed.terminal).toBe(true);
    expect(failed.error).toBe("boom");
  });

  it("treats a terminal snapshot as a sink", () => {
    const failed = reduceTransfer(initialTransferSnapshot("tx-1"), update({ phase: "failed", reason: "x", seq: 1 }));
    expect(reduceTransfer(failed, update({ phase: "ready", seq: 2 }))).toBe(failed);
  });
});

describe("createTransferStore", () => {
  const makeSource = () => {
    const listeners: Array<(u: TransferUpdate) => void> = [];
    const unsubscribe = vi.fn();
    const source: TransferProgressSource = {
      subscribe: vi.fn((_, onUpdate) => {
        listeners.push(onUpdate);
        return unsubscribe;
      }),
    };
    const emit = (u: TransferUpdate) => listeners.forEach((fn) => fn(u));
    return { source, emit, unsubscribe };
  };

  it("starts at the initial snapshot before any subscription", () => {
    const { source } = makeSource();
    const store = createTransferStore(source, "tx-1");
    expect(store.getSnapshot()).toEqual(initialTransferSnapshot("tx-1"));
    expect(source.subscribe).not.toHaveBeenCalled();
  });

  it("subscribes to the source lazily and notifies listeners on change", () => {
    const { source, emit } = makeSource();
    const store = createTransferStore(source, "tx-1");
    const listener = vi.fn();

    const unsub = store.subscribe(listener);
    expect(source.subscribe).toHaveBeenCalledTimes(1);

    emit(update({ bytes_done: 30, seq: 0 }));
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().bytesDone).toBe(30);

    // A coalesced no-op (stale seq) must not notify.
    emit(update({ bytes_done: 30, seq: 0 }));
    expect(listener).toHaveBeenCalledTimes(1);

    unsub();
  });

  it("tears down the source subscription when the last listener leaves", () => {
    const { source, unsubscribe } = makeSource();
    const store = createTransferStore(source, "tx-1");
    const unsubA = store.subscribe(vi.fn());
    const unsubB = store.subscribe(vi.fn());

    unsubA();
    expect(unsubscribe).not.toHaveBeenCalled(); // B still listening
    unsubB();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });

  it("re-subscribes to the source after a full teardown", () => {
    const { source } = makeSource();
    const store = createTransferStore(source, "tx-1");
    store.subscribe(vi.fn())();
    store.subscribe(vi.fn());
    expect(source.subscribe).toHaveBeenCalledTimes(2);
  });
});
