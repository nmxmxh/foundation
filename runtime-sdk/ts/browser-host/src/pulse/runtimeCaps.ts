import type { RuntimeCapabilities, RuntimeCapabilityIssue } from "../types";

const hasSharedWebAssemblyMemory = (): boolean => {
  if (typeof WebAssembly === "undefined" || typeof SharedArrayBuffer === "undefined") {
    return false;
  }
  try {
    const memory = new WebAssembly.Memory({
      initial: 1,
      maximum: 1,
      shared: true,
    });
    return memory.buffer instanceof SharedArrayBuffer;
  } catch {
    return false;
  }
};

export const getRuntimeCapabilities = (): RuntimeCapabilities => {
  const capabilities = {
    crossOriginIsolated:
      typeof globalThis.crossOriginIsolated === "boolean" ? globalThis.crossOriginIsolated : false,
    sharedArrayBuffer: typeof SharedArrayBuffer !== "undefined",
    webAssemblySharedMemory: hasSharedWebAssemblyMemory(),
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
  if (!capabilities.webAssemblySharedMemory) {
    issues.push({
      capability: "webAssemblySharedMemory",
      reason: "Shared WebAssembly.Memory is unavailable; WASM worker units must use copy/transfer fallback",
      fallback: "unsupported",
    });
  }

  return {
    ...capabilities,
    issues,
    supportsWorkerPulse:
      capabilities.crossOriginIsolated && capabilities.sharedArrayBuffer && capabilities.worker,
    supportsSharedMemoryRuntime:
      capabilities.crossOriginIsolated && capabilities.sharedArrayBuffer && capabilities.worker,
    supportsSharedWasmMemory:
      capabilities.crossOriginIsolated &&
      capabilities.sharedArrayBuffer &&
      capabilities.worker &&
      capabilities.webAssemblySharedMemory,
  };
};

export const describeRuntimeCapabilityGaps = (capabilities: RuntimeCapabilities = getRuntimeCapabilities()) =>
  capabilities.issues;
