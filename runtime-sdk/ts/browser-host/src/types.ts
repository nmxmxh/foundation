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

// Canonical control-plane descriptors. Mirror runtime_dispatch.capnp
// field-for-field; the field-drift check enforces parity against the schema.
export type DispatchLaneDescriptor = {
  unitClassMask: number;
  affinityBloom: number;
  laneId: number;
  jurisdiction: number;
  maxConcurrency: number;
  generation: number;
};

export type DispatchLaneStats = {
  ewmaNs: number;
  inflight: number;
  maxConcurrency: number;
  lastTickSeen: number;
};

/* ── render surfaces ────────────────────────────────────────────────── */

/**
 * Where a render lane ended up.
 *
 * `worker` means the canvas was transferred and the main thread can no longer
 * draw to it. `main-thread` means nothing was transferred and the caller owns
 * its own loop. The distinction is permanent for the life of a canvas, because
 * `transferControlToOffscreen` cannot be undone.
 */
export type RenderSurfaceMode = "worker" | "main-thread";

/**
 * One rung of a quality ladder.
 *
 * `performance_practices.md` §39.7 — degrade resolution, sample count and
 * update cadence before violating anything that is true. A rung is exactly
 * those three and nothing else, so moving between rungs cannot change what a
 * surface is showing, only how finely and how often it is shown.
 */
export type RenderSurfaceQualityTier = {
  /** Render scale as a fraction of CSS pixels, before the ratio cap. */
  scale: number;
  /** Milliseconds between frames this rung is paced to. */
  cadenceMs: number;
  /** Sample, step or iteration budget. Meaning is the surface's own. */
  detail?: number;
};

/** What a render surface reports about itself. Low-cardinality by construction. */
export type RenderSurfaceDiagnostics = {
  surface: string;
  mode: RenderSurfaceMode;
  /** The lane actually taken: "webgpu", "webgl2", "2d", "none". */
  lane: string;
  tier: number;
  cadenceMs: number;
  scale: number;
  visible: boolean;
  /** Capability names that were missing, if any. Never free text at scale. */
  issues: readonly string[];
};
