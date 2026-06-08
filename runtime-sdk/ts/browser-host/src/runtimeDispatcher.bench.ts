import { bench, describe } from "vitest";
import { RuntimeDispatcher, type RuntimeModuleExports } from "./runtimeDispatcher";

const encoder = new TextEncoder();

describe("RuntimeDispatcher", () => {
  const executorDispatcher = new RuntimeDispatcher({ maxPendingRequests: 4096 });
  executorDispatcher.bindExecutor("compute", {
    async execute(request) {
      return `${request.library}:${request.method}`;
    },
  });
  const benchParams = encoder.encode("batch=1");

  bench("async executor dispatch", async () => {
    await executorDispatcher.execute({
      library: "compute",
      method: "project",
      params: benchParams,
    });
  });

  const memory = new WebAssembly.Memory({ initial: 1 });
  let head = 1024;
  const exports: RuntimeModuleExports = {
    memory,
    compute_alloc(size: number) {
      const ptr = head;
      head += Math.max(8, size + 8);
      return ptr;
    },
    compute_free() {},
    compute_execute(
      servicePtr,
      serviceLen,
      actionPtr,
      actionLen,
      inputPtr,
      inputLen,
      paramsPtr,
      paramsLen
    ) {
      const heap = new Uint8Array(memory.buffer);
      const service = new TextDecoder().decode(heap.slice(servicePtr, servicePtr + serviceLen));
      const action = new TextDecoder().decode(heap.slice(actionPtr, actionPtr + actionLen));
      const params = heap.slice(paramsPtr, paramsPtr + paramsLen);
      const input = inputPtr === 0 ? new Uint8Array() : heap.slice(inputPtr, inputPtr + inputLen);
      const result = encoder.encode(`${service}:${action}:${params.byteLength}:${input.byteLength}`);
      const ptr = head;
      head += result.byteLength + 8;
      new DataView(memory.buffer).setUint32(ptr, result.byteLength, true);
      heap.set(result, ptr + 4);
      return ptr;
    },
  };
  const exportedDispatcher = new RuntimeDispatcher();
  exportedDispatcher.initialize(exports, memory);

  bench("compute_execute exported ABI", () => {
    head = 1024;
    exportedDispatcher.executeSync({
      library: "compute",
      method: "project",
      params: benchParams,
    });
  });
});
