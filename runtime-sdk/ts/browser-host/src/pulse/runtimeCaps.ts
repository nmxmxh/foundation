import type { RuntimeCapabilities, RuntimeCapabilityIssue } from "../types";

export const getRuntimeCapabilities = (): RuntimeCapabilities => {
  const capabilities = {
    crossOriginIsolated:
      typeof globalThis.crossOriginIsolated === "boolean" ? globalThis.crossOriginIsolated : false,
    sharedArrayBuffer: typeof SharedArrayBuffer !== "undefined",
    worker: typeof Worker !== "undefined",
    waitAsync: typeof (Atomics as { waitAsync?: unknown }).waitAsync === "function",
  };
  const issues: RuntimeCapabilityIssue[] = [];

  if (!capabilities.crossOriginIsolated) {
    issues.push({
      capability: "crossOriginIsolated",
      reason: "crossOriginIsolated is required for worker-backed shared-memory runtime features",
      fallback: "main-thread",
    });
  }
  if (!capabilities.sharedArrayBuffer) {
    issues.push({
      capability: "sharedArrayBuffer",
      reason: "SharedArrayBuffer is unavailable; runtime host must degrade away from shared-memory execution",
      fallback: "main-thread",
    });
  }
  if (!capabilities.worker) {
    issues.push({
      capability: "worker",
      reason: "Worker is unavailable; runtime orchestration must stay on the main thread",
      fallback: "main-thread",
    });
  }

  return {
    ...capabilities,
    issues,
    supportsWorkerPulse: issues.length === 0,
    supportsSharedMemoryRuntime:
      capabilities.crossOriginIsolated && capabilities.sharedArrayBuffer,
  };
};

export const describeRuntimeCapabilityGaps = (capabilities: RuntimeCapabilities = getRuntimeCapabilities()) =>
  capabilities.issues;
