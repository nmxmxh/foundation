import type { RuntimeCapabilities, RuntimeRole, RuntimeUnitDescriptor } from "./types";
import {
  validateRuntimeNativeGpuDescriptor,
  type RuntimeNativeGpuDescriptor,
  type RuntimeNativeGpuPlatform,
} from "./nativeGpu";

export type RuntimeWorkloadClass =
  | "control"
  | "stream"
  | "batch"
  | "vector"
  | "media"
  | "simulation"
  | "inference-prepost";

export type RuntimeExecutionLane =
  | "go-direct"
  | "cpu-scalar"
  | "cpu-simd"
  | "rust-ffi"
  | "shared-memory"
  | "native-gpu"
  | "webgpu"
  | "wasm-sab"
  | "wasm-transfer"
  | "packet-ring"
  | "stream"
  | "http";

export type RuntimeLanePlannerCapabilities = Pick<
  RuntimeCapabilities,
  "supportsSharedMemoryRuntime" | "supportsSharedWasmMemory" | "worker" | "sharedArrayBuffer" | "crossOriginIsolated"
> & {
  webGpu?: boolean;
  nativeGpu?: boolean;
  nativeGpuPlatforms?: RuntimeNativeGpuPlatform[];
  nativeFfi?: boolean;
  nativeSharedMemory?: boolean;
  cpuSimd?: boolean;
  hardwareMedia?: boolean;
  specializedAi?: boolean;
  packetRing?: boolean;
};

export type RuntimeLanePlanInput = {
  byteLength: number;
  workload: RuntimeWorkloadClass;
  role?: RuntimeRole;
  batchItems?: number;
  deadlineMs?: number;
  trust: "trusted" | "sandboxed" | "remote";
  locality: "same-process" | "same-host" | "browser" | "cross-host";
  unit?: Partial<RuntimeUnitDescriptor>;
  nativeGpuDescriptor?: RuntimeNativeGpuDescriptor;
  capabilities: RuntimeLanePlannerCapabilities;
};

export type RuntimeLanePlan = {
  lane: RuntimeExecutionLane;
  batchSize: number;
  copyBudget: "none" | "single-copy" | "bounded-copy" | "streaming";
  allocationBudget: "zero-heap" | "bounded-heap" | "runtime-managed" | "unbounded-stream";
  expectedLatencyClass: "nanoseconds" | "microseconds" | "milliseconds" | "streaming";
  deadlineRisk: "low" | "medium" | "high";
  requiresCrossOriginIsolation: boolean;
  reason: string;
  fallbacks: RuntimeExecutionLane[];
};

const CONTROL_MAX_BYTES = 4 * 1024;
const ARENA_MAX_BYTES = 1024 * 1024;
const GPU_MIN_BYTES = 256 * 1024;
const GPU_MIN_ITEMS = 1024;
const SIMD_MIN_BYTES = 16 * 1024;
const SIMD_MIN_ITEMS = 64;
const PACKET_RING_MIN_ITEMS = 8;

export const planRuntimeLane = (input: RuntimeLanePlanInput): RuntimeLanePlan => {
  const byteLength = Math.max(0, input.byteLength);
  const batchItems = Math.max(1, input.batchItems ?? 1);
  const fallbacks = fallbackOrder(input);
  const deadlineMs = input.deadlineMs ?? Number.POSITIVE_INFINITY;

  if (input.locality === "same-process" && input.trust === "trusted" && input.workload === "control" && byteLength <= CONTROL_MAX_BYTES) {
    return plan("go-direct", 1, "none", "zero-heap", "nanoseconds", deadlineRisk(deadlineMs, 0.001), false, "trusted same-process control payload fits the 4KB control plane", fallbacks);
  }

  if (shouldUsePacketRing(input, batchItems)) {
    return plan("packet-ring", roundBatch(batchItems, 8), "none", "zero-heap", "nanoseconds", deadlineRisk(deadlineMs, 0.05), false, "packet-like same-host stream can use fixed descriptor rings with burst dequeue", fallbacks);
  }

  if (shouldUseNativeGpu(input, byteLength, batchItems, deadlineMs)) {
    return plan("native-gpu", roundBatch(batchItems, 64), "none", "runtime-managed", "microseconds", deadlineRisk(deadlineMs, 0.15), false, "trusted native GPU descriptor can stay resident in the platform device lane", fallbacks);
  }

  if (shouldUseGpu(input, byteLength, batchItems, deadlineMs)) {
    return plan("webgpu", roundBatch(batchItems, 128), "single-copy", "runtime-managed", "milliseconds", deadlineRisk(deadlineMs, 2), true, "wide data-parallel workload large enough to amortize GPU dispatch", fallbacks);
  }

  if (shouldUseSimd(input, byteLength, batchItems)) {
    if (input.capabilities.nativeFfi && input.trust === "trusted") {
      return plan("rust-ffi", roundBatch(batchItems, 32), "none", "zero-heap", "nanoseconds", deadlineRisk(deadlineMs, 0.02), false, "trusted vector workload can run through native Rust FFI with SIMD-friendly layout", fallbacks);
    }
    if (input.capabilities.cpuSimd) {
      return plan("cpu-simd", roundBatch(batchItems, 32), "bounded-copy", "bounded-heap", "nanoseconds", deadlineRisk(deadlineMs, 0.05), false, "vector workload is large enough for SIMD lane amortization", fallbacks);
    }
  }

  if (input.locality === "same-host" && input.capabilities.nativeSharedMemory && byteLength <= ARENA_MAX_BYTES && input.trust !== "remote") {
    return plan("shared-memory", roundBatch(batchItems, 16), "none", "zero-heap", "microseconds", deadlineRisk(deadlineMs, 0.1), false, "same-host payload fits arena-backed shared-memory transport", fallbacks);
  }

  if (input.locality === "browser" && input.capabilities.supportsSharedMemoryRuntime && byteLength <= ARENA_MAX_BYTES) {
    return plan("wasm-sab", roundBatch(batchItems, 16), "single-copy", "zero-heap", "microseconds", deadlineRisk(deadlineMs, 0.1), true, "browser payload fits SharedArrayBuffer arena transport", fallbacks);
  }

  if (byteLength > ARENA_MAX_BYTES || input.workload === "stream") {
    return plan("stream", roundBatch(batchItems, 8), "streaming", "unbounded-stream", "streaming", "medium", false, "payload is too large for bounded arena movement or is explicitly streaming", fallbacks);
  }

  if (input.locality === "browser" && input.capabilities.worker) {
    return plan("wasm-transfer", roundBatch(batchItems, 8), "single-copy", "bounded-heap", "microseconds", deadlineRisk(deadlineMs, 0.5), false, "browser worker fallback can transfer bounded payloads without SAB", fallbacks);
  }

  return plan("http", 1, "bounded-copy", "runtime-managed", "milliseconds", deadlineRisk(deadlineMs, 5), false, "no lower-level local lane is available for this trust/locality/capability mix", fallbacks);
};

const shouldUseGpu = (input: RuntimeLanePlanInput, byteLength: number, batchItems: number, deadlineMs: number): boolean => {
  if (!input.capabilities.webGpu || input.locality !== "browser") {
    return false;
  }
  if (input.unit?.supportsGpu === false) {
    return false;
  }
  if (deadlineMs < 2) {
    return false;
  }
  return (
    input.workload === "simulation" ||
    input.workload === "media" ||
    input.workload === "inference-prepost" ||
    ((input.workload === "vector" || input.workload === "batch") && byteLength >= GPU_MIN_BYTES && batchItems >= GPU_MIN_ITEMS)
  );
};

const shouldUseNativeGpu = (
  input: RuntimeLanePlanInput,
  byteLength: number,
  batchItems: number,
  deadlineMs: number
): boolean => {
  if (!input.capabilities.nativeGpu) {
    return false;
  }
  if (input.trust !== "trusted" || (input.locality !== "same-host" && input.locality !== "same-process")) {
    return false;
  }
  if (input.unit?.supportsGpu === false) {
    return false;
  }
  if (deadlineMs < 0.05) {
    return false;
  }
  if (!input.nativeGpuDescriptor) {
    return false;
  }
  const validation = validateRuntimeNativeGpuDescriptor(input.nativeGpuDescriptor);
  if (!validation.ok) {
    return false;
  }
  if (input.capabilities.nativeGpuPlatforms?.length &&
    !input.capabilities.nativeGpuPlatforms.includes(validation.descriptor.platform)) {
    return false;
  }
  return (
    input.workload === "media" ||
    input.workload === "simulation" ||
    input.workload === "inference-prepost" ||
    ((input.workload === "vector" || input.workload === "batch") &&
      (byteLength >= SIMD_MIN_BYTES || batchItems >= SIMD_MIN_ITEMS))
  );
};

const shouldUseSimd = (input: RuntimeLanePlanInput, byteLength: number, batchItems: number): boolean =>
  (input.workload === "vector" || input.workload === "inference-prepost") &&
  byteLength >= SIMD_MIN_BYTES &&
  batchItems >= SIMD_MIN_ITEMS &&
  (input.locality === "same-process" || input.locality === "same-host");

const shouldUsePacketRing = (input: RuntimeLanePlanInput, batchItems: number): boolean =>
  input.capabilities.packetRing === true &&
  input.trust !== "remote" &&
  input.locality !== "cross-host" &&
  (input.workload === "stream" || input.workload === "batch") &&
  batchItems >= PACKET_RING_MIN_ITEMS;

const fallbackOrder = (input: RuntimeLanePlanInput): RuntimeExecutionLane[] => {
  const lanes: RuntimeExecutionLane[] = [];
  if (input.capabilities.nativeGpu) {
    lanes.push("native-gpu");
  }
  if (input.capabilities.webGpu) {
    lanes.push("webgpu");
  }
  if (input.capabilities.supportsSharedMemoryRuntime) {
    lanes.push("wasm-sab");
  }
  if (input.capabilities.nativeSharedMemory) {
    lanes.push("shared-memory");
  }
  if (input.capabilities.worker) {
    lanes.push("wasm-transfer");
  }
  if (input.capabilities.packetRing) {
    lanes.push("packet-ring");
  }
  lanes.push("stream", "http");
  return [...new Set(lanes)];
};

const roundBatch = (items: number, multiple: number): number =>
  Math.max(multiple, Math.ceil(Math.max(1, items) / multiple) * multiple);

const deadlineRisk = (deadlineMs: number, expectedMs: number): RuntimeLanePlan["deadlineRisk"] => {
  if (!Number.isFinite(deadlineMs)) {
    return "low";
  }
  if (deadlineMs < expectedMs) {
    return "high";
  }
  if (deadlineMs < expectedMs * 4) {
    return "medium";
  }
  return "low";
};

const plan = (
  lane: RuntimeExecutionLane,
  batchSize: number,
  copyBudget: RuntimeLanePlan["copyBudget"],
  allocationBudget: RuntimeLanePlan["allocationBudget"],
  expectedLatencyClass: RuntimeLanePlan["expectedLatencyClass"],
  deadlineRisk: RuntimeLanePlan["deadlineRisk"],
  requiresCrossOriginIsolation: boolean,
  reason: string,
  fallbacks: RuntimeExecutionLane[]
): RuntimeLanePlan => ({
  lane,
  batchSize,
  copyBudget,
  allocationBudget,
  expectedLatencyClass,
  deadlineRisk,
  requiresCrossOriginIsolation,
  reason,
  fallbacks: fallbacks.filter((fallback) => fallback !== lane),
});
