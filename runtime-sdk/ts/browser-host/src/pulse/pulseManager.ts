import {
  EPOCH_SLOT_COUNT,
  IDX_RUNTIME_TICK,
  IDX_VISIBILITY_STATE,
} from "../generated/runtimeBuffer";
import { type PulseDiagnostics, type PulseMode } from "../types";
import { getRuntimeCapabilities } from "./runtimeCaps";

type EpochHandler = (value: number, index: number) => void;

type PulseManagerOptions = {
  defaultTPS?: number;
  onDiagnostics?: (diagnostics: PulseDiagnostics) => void;
  createWorker?: () => Worker;
};

type PulseMessage =
  | { type: "INIT"; payload: { buffer: SharedArrayBuffer } }
  | { type: "STOP" }
  | { type: "SET_TPS"; payload: { tps: number } }
  | { type: "SET_VISIBILITY"; payload: { visible: boolean } }
  | { type: "WATCH_INDICES"; payload: { indices: number[] } }
  | { type: "UNWATCH_INDICES"; payload: { indices: number[] } };

const defaultWorkerFactory = () => new Worker(new URL("./pulse.worker.ts", import.meta.url), { type: "module" });

export type PulseManager = ReturnType<typeof createPulseManager>;

export const createPulseManager = (options: PulseManagerOptions = {}) => {
  const capabilities = getRuntimeCapabilities();
  const handlers = new Map<number, Set<EpochHandler>>();
  const lastSeen = new Map<number, number>();
  const onDiagnostics = options.onDiagnostics ?? (() => undefined);
  const createWorker = options.createWorker ?? defaultWorkerFactory;

  let mode: PulseMode = "stopped";
  let degraded = false;
  let targetTPS = options.defaultTPS ?? 60;
  let visible = true;
  let worker: Worker | null = null;
  let mainThreadTimer: number | null = null;
  let buffer: SharedArrayBuffer | null = null;
  let visibilityHandlersInstalled = false;

  const emitDiagnostics = () => {
    onDiagnostics({
      mode,
      waitAsync: capabilities.waitAsync,
      crossOriginIsolated: capabilities.crossOriginIsolated,
      degraded,
      targetTPS,
      watcherCount: handlers.size,
      visible,
      issues: capabilities.issues,
    });
  };

  const epochView = () => {
    if (!buffer) {
      return null;
    }
    return new Int32Array(buffer, 0, EPOCH_SLOT_COUNT);
  };

  const notifyHandlers = (index: number, value: number) => {
    const group = handlers.get(index);
    if (!group) {
      return;
    }
    lastSeen.set(index, value);
    for (const handler of group) {
      handler(value, index);
    }
  };

  const inspectWatchedEpochs = () => {
    const epochs = epochView();
    if (!epochs) {
      return;
    }
    for (const index of handlers.keys()) {
      const next = Atomics.load(epochs, index);
      const previous = lastSeen.get(index) ?? next;
      if (next !== previous) {
        notifyHandlers(index, next);
      }
    }
  };

  const stopMainThreadLoop = () => {
    if (mainThreadTimer !== null) {
      window.clearTimeout(mainThreadTimer);
      mainThreadTimer = null;
    }
  };

  const startMainThreadLoop = () => {
    const tick = () => {
      const epochs = epochView();
      if (!epochs || mode !== "main-thread") {
        return;
      }
      const tps = visible ? targetTPS : 12;
      Atomics.add(epochs, IDX_RUNTIME_TICK, 1);
      Atomics.store(epochs, IDX_VISIBILITY_STATE, visible ? 1 : 0);
      inspectWatchedEpochs();
      mainThreadTimer = window.setTimeout(tick, Math.max(1, Math.floor(1000 / Math.max(1, tps))));
    };

    stopMainThreadLoop();
    tick();
  };

  const setVisibility = (nextVisible: boolean) => {
    visible = nextVisible;
    const epochs = epochView();
    if (epochs) {
      Atomics.store(epochs, IDX_VISIBILITY_STATE, visible ? 1 : 0);
    }
    if (worker) {
      const message: PulseMessage = {
        type: "SET_VISIBILITY",
        payload: { visible },
      };
      worker.postMessage(message);
    }
    emitDiagnostics();
  };

  const installVisibilityHandlers = () => {
    if (visibilityHandlersInstalled || typeof document === "undefined" || typeof window === "undefined") {
      return;
    }
    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);
    visibilityHandlersInstalled = true;
    visible = document.visibilityState === "visible";
  };

  const handleVisibilityChange = () => setVisibility(document.visibilityState === "visible");
  const handleFocus = () => setVisibility(true);
  const handleBlur = () => setVisibility(false);

  const removeVisibilityHandlers = () => {
    if (!visibilityHandlersInstalled || typeof document === "undefined" || typeof window === "undefined") {
      return;
    }
    document.removeEventListener("visibilitychange", handleVisibilityChange);
    window.removeEventListener("focus", handleFocus);
    window.removeEventListener("blur", handleBlur);
    visibilityHandlersInstalled = false;
  };

  const attachWorker = () => {
    if (!worker) {
      return;
    }
    worker.onmessage = (event: MessageEvent<{ type: string; payload: { index: number; value: number } }>) => {
      if (event.data.type === "EPOCH_CHANGE") {
        notifyHandlers(event.data.payload.index, event.data.payload.value);
      }
    };
    worker.onerror = () => {
      degraded = true;
      mode = "main-thread";
      worker?.terminate();
      worker = null;
      startMainThreadLoop();
      emitDiagnostics();
    };
  };

  return {
    start(nextBuffer: SharedArrayBuffer) {
      buffer = nextBuffer;
      const epochs = epochView();
      if (epochs) {
        for (const index of handlers.keys()) {
          lastSeen.set(index, Atomics.load(epochs, index));
        }
      }
      installVisibilityHandlers();

      if (!capabilities.supportsWorkerPulse) {
        degraded = true;
        mode = "main-thread";
        startMainThreadLoop();
        emitDiagnostics();
        return;
      }

      if (!worker) {
        try {
          worker = createWorker();
          attachWorker();
          mode = "worker";
        } catch {
          degraded = true;
          mode = "main-thread";
          worker = null;
          startMainThreadLoop();
        }
      }

      if (worker) {
        const initMessage: PulseMessage = {
          type: "INIT",
          payload: { buffer: nextBuffer },
        };
        worker.postMessage(initMessage);
      }

      setVisibility(visible);
      emitDiagnostics();
    },
    watchEpochs(indices: number[], handler: EpochHandler) {
      const epochs = epochView();
      for (const index of indices) {
        let group = handlers.get(index);
        if (!group) {
          group = new Set();
          handlers.set(index, group);
          if (epochs) {
            lastSeen.set(index, Atomics.load(epochs, index));
          }
        }
        group.add(handler);
      }

      if (worker) {
        const message: PulseMessage = {
          type: "WATCH_INDICES",
          payload: { indices },
        };
        worker.postMessage(message);
      }
      emitDiagnostics();
    },
    unwatchEpoch(index: number, handler: EpochHandler) {
      const group = handlers.get(index);
      if (!group) {
        return;
      }
      group.delete(handler);
      if (group.size === 0) {
        handlers.delete(index);
        lastSeen.delete(index);
        if (worker) {
          const message: PulseMessage = {
            type: "UNWATCH_INDICES",
            payload: { indices: [index] },
          };
          worker.postMessage(message);
        }
      }
      emitDiagnostics();
    },
    setTPS(nextTPS: number) {
      targetTPS = Math.max(1, Math.floor(nextTPS));
      if (worker) {
        const message: PulseMessage = {
          type: "SET_TPS",
          payload: { tps: targetTPS },
        };
        worker.postMessage(message);
      }
      emitDiagnostics();
    },
    stop() {
      if (worker) {
        const message: PulseMessage = { type: "STOP" };
        worker.postMessage(message);
      }
      stopMainThreadLoop();
      removeVisibilityHandlers();
      mode = "stopped";
      emitDiagnostics();
    },
    shutdown() {
      if (worker) {
        worker.terminate();
        worker = null;
      }
      stopMainThreadLoop();
      removeVisibilityHandlers();
      handlers.clear();
      lastSeen.clear();
      mode = "stopped";
      buffer = null;
      emitDiagnostics();
    },
    getDiagnostics(): PulseDiagnostics {
      return {
        mode,
        waitAsync: capabilities.waitAsync,
        crossOriginIsolated: capabilities.crossOriginIsolated,
        degraded,
        targetTPS,
        watcherCount: handlers.size,
        visible,
        issues: capabilities.issues,
      };
    },
    getMode(): PulseMode {
      return mode;
    },
  };
};
