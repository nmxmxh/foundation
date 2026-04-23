import * as runtimeBuffer from "../generated/runtimeBuffer";

type PulseMessage =
  | { type: "INIT"; payload: { buffer: SharedArrayBuffer } }
  | { type: "STOP"; payload?: undefined }
  | { type: "SET_TPS"; payload: { tps: number } }
  | { type: "SET_VISIBILITY"; payload: { visible: boolean } }
  | { type: "WATCH_INDICES"; payload: { indices: number[] } }
  | { type: "UNWATCH_INDICES"; payload: { indices: number[] } };

type PulseWorkerScope = {
  postMessage: (message: unknown) => void;
  onmessage: ((event: MessageEvent<PulseMessage>) => void) | null;
};

declare const self: PulseWorkerScope;

const watchers = new Set<number>();
let epochs: Int32Array | null = null;
let active = false;
let visible = true;
let targetTPS = 60;
let lastPulseAt = 0;

const backgroundTPS = 12;
const hasWaitAsync = typeof (Atomics as { waitAsync?: unknown }).waitAsync === "function";

const postEpochChange = (index: number, value: number) => {
  self.postMessage({
    type: "EPOCH_CHANGE",
    payload: { index, value },
  });
};

const watchIndex = (index: number) => {
  if (!epochs || !watchers.has(index)) {
    return;
  }

  const current = Atomics.load(epochs, index);
  if (hasWaitAsync) {
    const result = (
      Atomics as unknown as {
        waitAsync: (
          array: Int32Array,
          index: number,
          value: number
        ) => { async: boolean; value: Promise<string> };
      }
    ).waitAsync(epochs, index, current);
    if (result.async) {
      void result.value.then(() => {
        if (!epochs || !watchers.has(index) || !active) {
          return;
        }
        postEpochChange(index, Atomics.load(epochs, index));
        watchIndex(index);
      });
      return;
    }
  }

  setTimeout(() => {
    if (!epochs || !watchers.has(index) || !active) {
      return;
    }
    const next = Atomics.load(epochs, index);
    if (next !== current) {
      postEpochChange(index, next);
    }
    watchIndex(index);
  }, 16);
};

const runPulseLoop = () => {
  if (!active || !epochs) {
    return;
  }

  const now = performance.now();
  const tps = visible ? targetTPS : backgroundTPS;
  const interval = 1000 / Math.max(1, tps);
  const elapsed = now - lastPulseAt;

  if (elapsed >= interval) {
    Atomics.add(epochs, IDX_RUNTIME_TICK, 1);
    if (typeof Atomics.notify === "function") {
      Atomics.notify(epochs, IDX_RUNTIME_TICK, 1);
    }
    lastPulseAt = now - (elapsed % interval);
  }

  setTimeout(runPulseLoop, Math.max(0, interval - elapsed));
};

self.onmessage = (event: MessageEvent<PulseMessage>) => {
  const message = event.data;
  switch (message.type) {
    case "INIT":
      epochs = new Int32Array(message.payload.buffer, 0, EPOCH_SLOT_COUNT);
      active = true;
      for (const index of watchers) {
        watchIndex(index);
      }
      runPulseLoop();
      break;
    case "STOP":
      active = false;
      break;
    case "SET_TPS":
      targetTPS = Math.max(1, Math.floor(message.payload.tps));
      break;
    case "SET_VISIBILITY":
      visible = message.payload.visible;
      if (epochs) {
        Atomics.store(epochs, IDX_VISIBILITY_STATE, visible ? 1 : 0);
        if (typeof Atomics.notify === "function") {
          Atomics.notify(epochs, IDX_VISIBILITY_STATE, 1);
        }
      }
      break;
    case "WATCH_INDICES":
      for (const index of message.payload.indices) {
        if (!watchers.has(index)) {
          watchers.add(index);
          watchIndex(index);
        }
      }
      break;
    case "UNWATCH_INDICES":
      for (const index of message.payload.indices) {
        watchers.delete(index);
      }
      break;
  }
};

export {};

const EPOCH_SLOT_COUNT = runtimeBuffer.EPOCH_SLOT_COUNT;
const IDX_RUNTIME_TICK = runtimeBuffer.IDX_RUNTIME_TICK;
const IDX_VISIBILITY_STATE = runtimeBuffer.IDX_VISIBILITY_STATE;
