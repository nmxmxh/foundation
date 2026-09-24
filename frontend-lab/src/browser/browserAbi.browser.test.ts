import { expect, it } from "vitest";
import { BrowserRuntimeHost, RuntimeMemoryRegion, IDX_OUTPUT_WRITTEN } from "@ovasabi/runtime-browser";

declare module "vitest" {
  interface TaskMeta { browserAbi?: { userAgent: string; crossOriginIsolated: boolean; measurements: object[] }; }
}

const wasmUrl = new URL("../../../runtime-sdk/ts/browser-host/fixtures/browser-abi/parity_fixture.shared.wasm", import.meta.url).href;

function request(worker: Worker, message: unknown): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { worker.terminate(); reject(new Error("ABI worker timeout")); }, 5000);
    worker.onmessage = (event) => {
      clearTimeout(timeout);
      if (event.data.error) reject(new Error(event.data.error)); else resolve(event.data);
    };
    worker.onerror = (event) => { clearTimeout(timeout); reject(new Error(event.message)); };
    worker.postMessage(message);
  });
}

it("shares guest regions with two independent workers through epochs", async () => {
  expect(crossOriginIsolated).toBe(true);
  await Promise.all([3, 7].map(async (value) => {
    const worker = new Worker(new URL("./browserAbi.worker.ts", import.meta.url), { type: "module" });
    try {
      const initialized = await request(worker, { kind: "INIT", url: wasmUrl });
      const region = new RuntimeMemoryRegion(initialized.memory as WebAssembly.Memory,
        initialized.byteOffset as number, initialized.byteLength as number, initialized.handle as number);
      const host = new BrowserRuntimeHost();
      for (let i = 0; i < 20; i += 1) {
        host.setInputBytes(region, new Uint8Array([value, i]));
        expect((await request(worker, { kind: "RUN" })).result).toBe(0);
        expect(Atomics.load(host.getEpochView(region), IDX_OUTPUT_WRITTEN)).toBe(i + 1);
        expect(host.readOutputBytes(region)).toEqual(new Uint8Array([value ^ 0x5a, i ^ 0x5a]));
        host.markOutputConsumed(region);
      }
    } finally { worker.terminate(); }
  }));
});

it("measures copied and borrowed scans in Chromium with identical Rust code", async ({ task }) => {
  const host = new BrowserRuntimeHost();
  const memory = new WebAssembly.Memory({ initial: 32, maximum: 2048, shared: true });
  const imports = host.getImportObject({ env: { memory } });
  let copiedBytes = 0;
  const env = imports.env as Record<string, (...args: number[]) => number>;
  const copy = env.ovrt_copy_from_buffer;
  env.ovrt_copy_from_buffer = (...args) => { copiedBytes += args[3]; return copy(...args); };
  const { instance } = await WebAssembly.instantiateStreaming(fetch(wasmUrl), imports);
  host.attachInstance(instance);
  const checksum = instance.exports.ovrt_buffer_checksum as (handle: number, offset: number, length: number) => number;
  const measurements: object[] = [];
  for (const bytes of [4096, 65536, 1048576, 4194304]) {
    const direct = host.createBuffer(bytes);
    const copied = new SharedArrayBuffer(bytes);
    const legacy = host.registerBuffer(copied);
    direct.bytes.fill(3);
    new Uint8Array(copied).fill(3);
    const scan = (handle: number) => {
      if (checksum(handle, 0, bytes) !== bytes * 3) throw new Error("checksum mismatch");
    };
    for (let i = 0; i < 200; i += 1) { scan(direct.handle); scan(legacy); }
    const iterations = Math.max(100, Math.floor(32 * 1024 * 1024 / bytes));
    for (let sample = 0; sample < 5; sample += 1) {
      for (const lane of sample % 2 ? ["direct", "copied"] : ["copied", "direct"]) {
        const handle = lane === "direct" ? direct.handle : legacy;
        const copiesBefore = copiedBytes;
        const start = performance.now();
        for (let i = 0; i < iterations; i += 1) scan(handle);
        const nsPerOp = (performance.now() - start) * 1e6 / iterations;
        const bytesCopiedPerOp = (copiedBytes - copiesBefore) / iterations;
        expect(bytesCopiedPerOp).toBe(lane === "direct" ? 0 : bytes);
        measurements.push({ kind: "scan", bytes, lane, sample, iterations, nsPerOp, bytesCopiedPerOp });
      }
    }
    direct.release();
    host.unregisterBuffer(legacy);
  }
  const run = instance.exports.ovrt_unit_run as (handle: number) => number;
  for (const bytes of [128, 1024]) {
    const direct = host.createRuntimeBuffer();
    const copied = new SharedArrayBuffer(4096);
    const legacy = host.registerBuffer(copied);
    const input = new Uint8Array(bytes).fill(3);
    const request = (lane: string) => {
      const buffer = lane === "direct" ? direct : copied;
      host.setInputBytes(buffer, input);
      if (run(lane === "direct" ? direct.handle : legacy) !== 0 || host.readOutputBytes(buffer)[bytes - 1] !== (3 ^ 0x5a)) {
        throw new Error("control request mismatch");
      }
      host.markOutputConsumed(buffer);
    };
    for (let i = 0; i < 200; i += 1) { request("direct"); request("copied"); }
    const iterations = 10000;
    for (let sample = 0; sample < 5; sample += 1) {
      for (const lane of sample % 2 ? ["direct", "copied"] : ["copied", "direct"]) {
        const start = performance.now();
        for (let i = 0; i < iterations; i += 1) request(lane);
        measurements.push({ kind: "control", bytes, lane, sample, iterations, nsPerOp: (performance.now() - start) * 1e6 / iterations });
      }
    }
    direct.release();
    host.unregisterBuffer(legacy);
  }
  task.meta.browserAbi = { userAgent: navigator.userAgent, crossOriginIsolated, measurements };
  host.dispose();
}, 30000);
