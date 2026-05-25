import {
  measureRuntimeWebGpuDeviceRoundTrip,
  type RuntimeWebGpuDeviceTimingProbeOptions,
  type RuntimeWebGpuDeviceTimingProbeResult,
} from "./webgpuHost";

export type RuntimeWebGpuPhysicalProbeReport = {
  measuredAtUnixNs: number;
  runtime: "browser" | "worker" | "node" | "unknown";
  userAgent?: string;
  result: RuntimeWebGpuDeviceTimingProbeResult;
};

export const runRuntimeWebGpuPhysicalProbe = async (
  options: RuntimeWebGpuDeviceTimingProbeOptions = {}
): Promise<RuntimeWebGpuPhysicalProbeReport> => ({
  measuredAtUnixNs: Date.now() * 1_000_000,
  runtime: detectRuntime(),
  userAgent: globalThis.navigator?.userAgent,
  result: await measureRuntimeWebGpuDeviceRoundTrip({
    waitForSubmittedWork: true,
    ...options,
  }),
});

export const formatRuntimeWebGpuPhysicalProbe = (
  report: RuntimeWebGpuPhysicalProbeReport
): string => {
  const lines = [
    `measuredAtUnixNs=${report.measuredAtUnixNs}`,
    `runtime=${report.runtime}`,
  ];
  if (report.userAgent) {
    lines.push(`userAgent=${report.userAgent}`);
  }

  const result = report.result;
  lines.push(`available=${result.available}`);
  if (!result.available) {
    lines.push(`reason=${result.reason}`);
    return lines.join("\n");
  }

  lines.push(
    `adapterNs=${result.adapterNs}`,
    `deviceNs=${result.deviceNs}`,
    `prewarmNs=${result.prewarmNs}`,
    `dispatchNs=${result.dispatchNs}`,
    `queueDrainNs=${result.queueDrainNs}`,
    `materializeNs=${result.materializeNs}`,
    `totalNs=${result.totalNs}`,
    `uploadMode=${result.uploadMode}`,
    `materialized=${result.materialized}`
  );

  const dispatch = result.dispatchTimingsNs;
  lines.push(
    `dispatch.packNs=${dispatch.packNs}`,
    `dispatch.uploadNs=${dispatch.uploadNs}`,
    `dispatch.pipelineNs=${dispatch.pipelineNs}`,
    `dispatch.dispatchNs=${dispatch.dispatchNs}`,
    `dispatch.readbackNs=${dispatch.readbackNs}`,
    `dispatch.dispatchReadbackNs=${dispatch.dispatchReadbackNs}`,
    `dispatch.writebackNs=${dispatch.writebackNs}`,
    `dispatch.totalNs=${dispatch.totalNs}`
  );

  if (result.materializeTimingsNs) {
    lines.push(
      `materialize.readbackNs=${result.materializeTimingsNs.readbackNs}`,
      `materialize.writebackNs=${result.materializeTimingsNs.writebackNs}`,
      `materialize.totalNs=${result.materializeTimingsNs.totalNs}`
    );
  }
  if (result.resource) {
    lines.push(
      `resource.id=${result.resource.id}`,
      `resource.kind=${result.resource.kind}`,
      `resource.byteLength=${result.resource.byteLength}`,
      `resource.descriptorCount=${result.resource.descriptorIds.length}`
    );
  }
  return lines.join("\n");
};

const detectRuntime = (): RuntimeWebGpuPhysicalProbeReport["runtime"] => {
  const workerScope = (globalThis as { WorkerGlobalScope?: { prototype?: object } }).WorkerGlobalScope;
  if (workerScope?.prototype && workerScope.prototype.isPrototypeOf(globalThis)) {
    return "worker";
  }
  if (typeof window !== "undefined" && typeof document !== "undefined") {
    return "browser";
  }
  if (typeof navigator !== "undefined" && navigator.userAgent.includes("Node.js")) {
    return "node";
  }
  return "unknown";
};
