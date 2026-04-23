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
  capability: "crossOriginIsolated" | "sharedArrayBuffer" | "worker" | "waitAsync";
  reason: string;
  fallback: "main-thread" | "unsupported";
};

export type RuntimeCapabilities = {
  crossOriginIsolated: boolean;
  sharedArrayBuffer: boolean;
  worker: boolean;
  waitAsync: boolean;
  issues: RuntimeCapabilityIssue[];
  supportsWorkerPulse: boolean;
  supportsSharedMemoryRuntime: boolean;
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

export type RuntimeDiagnosticsSnapshot = {
  pulseMode: PulseMode;
  degraded: boolean;
  activeUnits: number;
  inFlight: number;
  lastRuntimeSource: string;
  lastError: string | null;
  lastEpoch: number;
  issues: RuntimeCapabilityIssue[];
};
