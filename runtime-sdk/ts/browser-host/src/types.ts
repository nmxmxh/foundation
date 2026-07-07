export type RuntimeRole = "pulse" | "compute" | "gpu" | "io";

export type RuntimeUnitDescriptor = {
  unitId: string;
  role: RuntimeRole;
  inputSchema: string;
  outputSchema: string;
  supportsWasm: boolean;
  supportsNative: boolean;
  requiresSharedMemory: boolean;
  supportsGpu: boolean;
  maxConcurrency: number;
};

export type RuntimeWorkerRequest<TInput = Record<string, unknown>> = {
  kind: "RUN_UNIT";
  requestId: string;
  unitId: string;
  role: RuntimeRole;
  input: TInput;
  buffer: SharedArrayBuffer;
};

export type RuntimeWorkerResponse<TOutput = Record<string, unknown>> = {
  kind: "RUN_RESULT";
  requestId: string;
  unitId: string;
  runtimeSource: string;
  epoch: number;
  diagnostics?: string;
  output?: TOutput;
};

export type PulseMode = "worker" | "main-thread" | "stopped";

export type RuntimeCapabilityIssue = {
  capability:
    | "crossOriginIsolated"
    | "sharedArrayBuffer"
    | "webAssemblySharedMemory"
    | "worker"
    | "waitAsync";
  reason: string;
  fallback: "main-thread" | "unsupported";
};

export type RuntimeCapabilities = {
  crossOriginIsolated: boolean;
  sharedArrayBuffer: boolean;
  webAssemblySharedMemory: boolean;
  worker: boolean;
  waitAsync: boolean;
  issues: RuntimeCapabilityIssue[];
  supportsWorkerPulse: boolean;
  supportsSharedMemoryRuntime: boolean;
  supportsSharedWasmMemory: boolean;
};

export type PulseDiagnostics = {
  mode: PulseMode;
  waitAsync: boolean;
  crossOriginIsolated: boolean;
  degraded: boolean;
  targetTPS: number;
  watcherCount: number;
  visible: boolean;
  issues: RuntimeCapabilityIssue[];
};

// Canonical runtime mode. Superset of PulseMode: native hosts add "native".
export type RuntimeMode = "worker" | "main-thread" | "native" | "stopped";

// Canonical control-plane descriptor. Mirrors runtime_diagnostics.capnp
// field-for-field; the field-drift check enforces parity against the schema.
export type RuntimeDiagnostics = {
  mode: RuntimeMode;
  degraded: boolean;
  activeUnits: number;
  inFlight: number;
  lastRuntimeSource: string;
  lastError: string | null;
  lastEpoch: number;
};

// Host superset: the canonical wire contract plus a browser-only delta.
// Composed by intersection rather than re-spelled, so the canonical fields are
// the canonical type by construction and cannot drift.
export type RuntimeDiagnosticsSnapshot = RuntimeDiagnostics & {
  issues: RuntimeCapabilityIssue[];
};
