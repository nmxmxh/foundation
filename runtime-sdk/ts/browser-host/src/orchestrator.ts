import { type BrowserRuntimeHost } from "./host";
import { createPulseManager, type PulseManager } from "./pulse/pulseManager";
import {
  type RuntimeDiagnosticsSnapshot,
  type RuntimeRole,
  type RuntimeUnitDescriptor,
  type RuntimeWorkerRequest,
  type RuntimeWorkerResponse,
} from "./types";

type WorkerFactory = (role: RuntimeRole) => Worker | null;

type RuntimeOrchestratorOptions = {
  host: BrowserRuntimeHost;
  createWorker: WorkerFactory;
  pulseManager?: PulseManager;
  defaultTimeoutMs?: number;
  restartOnWorkerFailure?: boolean;
  restartOnTimeout?: boolean;
  onDiagnostics?: (snapshot: RuntimeDiagnosticsSnapshot) => void;
};

type RunUnitInput<TInput> = {
  unitId: string;
  input: TInput;
  buffer: SharedArrayBuffer;
  timeoutMs?: number;
};

type PendingRequest = {
  resolve: (value: RuntimeWorkerResponse) => void;
  reject: (reason?: unknown) => void;
  timeoutId: number;
  role: RuntimeRole;
};

const nextRequestId = () => `ovrt_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;

export const createRuntimeOrchestrator = (options: RuntimeOrchestratorOptions) => {
  const units = new Map<string, RuntimeUnitDescriptor>();
  const workers = new Map<RuntimeRole, Worker>();
  const inFlight = new Map<string, PendingRequest>();
  const pulseManager =
    options.pulseManager ??
    createPulseManager({
      onDiagnostics: () => emitDiagnostics(),
    });

  let diagnostics: RuntimeDiagnosticsSnapshot = {
    mode: pulseManager.getMode(),
    degraded: false,
    activeUnits: 0,
    inFlight: 0,
    lastRuntimeSource: "idle",
    lastError: null,
    lastEpoch: 0,
    issues: pulseManager.getDiagnostics().issues,
  };

  const emitDiagnostics = () => {
    const pulseDiagnostics = pulseManager.getDiagnostics();
    diagnostics = {
      ...diagnostics,
      mode: pulseManager.getMode(),
      activeUnits: units.size,
      inFlight: inFlight.size,
      degraded: diagnostics.degraded || pulseDiagnostics.degraded,
      issues: pulseDiagnostics.issues,
    };
    options.onDiagnostics?.(diagnostics);
  };

  const releasePendingForRole = (role: RuntimeRole, reason: Error) => {
    for (const [requestId, pending] of inFlight.entries()) {
      if (pending.role === role) {
        globalThis.clearTimeout(pending.timeoutId);
        pending.reject(reason);
        inFlight.delete(requestId);
      }
    }
  };

  const resetWorker = (role: RuntimeRole, reason: Error) => {
    const worker = workers.get(role);
    if (worker) {
      worker.terminate();
      workers.delete(role);
    }
    releasePendingForRole(role, reason);
  };

  const attachWorker = (role: RuntimeRole, worker: Worker) => {
    worker.onmessage = (event: MessageEvent<RuntimeWorkerResponse>) => {
      const response = event.data;
      if (response.kind !== "RUN_RESULT") {
        return;
      }
      const pending = inFlight.get(response.requestId);
      if (!pending) {
        return;
      }
      globalThis.clearTimeout(pending.timeoutId);
      inFlight.delete(response.requestId);
      diagnostics = {
        ...diagnostics,
        degraded: diagnostics.degraded || Boolean(response.diagnostics),
        lastRuntimeSource: response.runtimeSource,
        lastError: response.diagnostics ?? null,
        lastEpoch: response.epoch,
      };
      emitDiagnostics();
      pending.resolve(response);
    };

    worker.onerror = (event) => {
      diagnostics = {
        ...diagnostics,
        degraded: true,
        lastError: event.message || `runtime worker ${role} failed`,
      };
      emitDiagnostics();
      const error = new Error(diagnostics.lastError ?? `runtime worker ${role} failed`);
      if (options.restartOnWorkerFailure ?? true) {
        resetWorker(role, error);
        return;
      }
      releasePendingForRole(role, error);
    };
  };

  const getWorker = (role: RuntimeRole): Worker => {
    const existing = workers.get(role);
    if (existing) {
      return existing;
    }

    const created = options.createWorker(role);
    if (!created) {
      throw new Error(`no worker factory is registered for runtime role ${role}`);
    }
    workers.set(role, created);
    attachWorker(role, created);
    return created;
  };

  emitDiagnostics();

  return {
    registerUnit(descriptor: RuntimeUnitDescriptor) {
      units.set(descriptor.unitId, descriptor);
      emitDiagnostics();
    },
    async runUnit<TInput>(input: RunUnitInput<TInput>): Promise<RuntimeWorkerResponse> {
      const descriptor = units.get(input.unitId);
      if (!descriptor) {
        throw new Error(`runtime unit ${input.unitId} is not registered`);
      }

      if (descriptor.requiresSharedMemory) {
        pulseManager.start(input.buffer);
      }

      const requestId = nextRequestId();
      const worker = getWorker(descriptor.role);

      const timeoutId = globalThis.setTimeout(() => {
        const pending = inFlight.get(requestId);
        if (!pending) {
          return;
        }
        inFlight.delete(requestId);
        diagnostics = {
          ...diagnostics,
          degraded: true,
          lastError: `runtime unit ${descriptor.unitId} timed out`,
        };
        emitDiagnostics();
        const error = new Error(`runtime unit ${descriptor.unitId} timed out`);
        pending.reject(error);
        if (options.restartOnTimeout ?? true) {
          resetWorker(descriptor.role, error);
        }
      }, input.timeoutMs ?? options.defaultTimeoutMs ?? 3000) as unknown as number;

      const promise = new Promise<RuntimeWorkerResponse>((resolve, reject) => {
        inFlight.set(requestId, {
          resolve,
          reject,
          timeoutId,
          role: descriptor.role,
        });
      });

      const message: RuntimeWorkerRequest<TInput> = {
        kind: "RUN_UNIT",
        requestId,
        unitId: descriptor.unitId,
        role: descriptor.role,
        input: input.input,
        buffer: input.buffer,
      };
      worker.postMessage(message);
      emitDiagnostics();
      return promise;
    },
    getDiagnostics(): RuntimeDiagnosticsSnapshot {
      return diagnostics;
    },
    getPulseManager(): PulseManager {
      return pulseManager;
    },
    shutdown() {
      for (const worker of workers.values()) {
        worker.terminate();
      }
      workers.clear();
      pulseManager.shutdown();
      diagnostics = {
        ...diagnostics,
        mode: "stopped",
        inFlight: 0,
      };
      emitDiagnostics();
    },
  };
};
