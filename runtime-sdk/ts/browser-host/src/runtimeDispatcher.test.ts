import { describe, expect, it, vi } from "vitest";

import { RuntimeDispatcher, type RuntimeExecutor } from "./runtimeDispatcher";

describe("RuntimeDispatcher", () => {
  it("dispatches through a bound executor", async () => {
    const executor: RuntimeExecutor = {
      async execute(request) {
        return `${request.library}:${request.method}`;
      },
    };
    const dispatcher = new RuntimeDispatcher();
    dispatcher.bindExecutor("compute", executor);

    await expect(dispatcher.execute({ library: "hash", method: "blake3" })).resolves.toBe("hash:blake3");
    expect(dispatcher.getMetrics().completed).toBe(1);
  });

  it("enforces pending queue bounds", async () => {
    const executor: RuntimeExecutor = {
      execute() {
        return new Promise(() => undefined);
      },
    };
    const dispatcher = new RuntimeDispatcher({ maxPendingRequests: 1, defaultTimeoutMs: 50 });
    dispatcher.bindExecutor("compute", executor);

    const first = dispatcher.execute({ library: "hash", method: "blake3" }).catch((error: unknown) => error);
    await expect(dispatcher.execute({ library: "hash", method: "sha256" })).rejects.toThrow("saturated");
    const firstResult = await first;
    expect(firstResult).toBeInstanceOf(Error);
    expect(dispatcher.getMetrics().saturated).toBe(1);
  });

  it("validates routes and manages capabilities", () => {
    const dispatcher = new RuntimeDispatcher();
    dispatcher.registerUnit("image", ["resize"]);
    dispatcher.registerUnit("compound", ["compound:special"]);
    dispatcher.registerCapability("image:crop");
    dispatcher.registerCapability("invalid");
    expect(dispatcher.hasCapability("image")).toBe(true);
    expect(dispatcher.hasCapability("image", "resize")).toBe(true);
    expect(dispatcher.hasCapability("image", "crop")).toBe(true);
    expect(dispatcher.hasCapability("compound", "special")).toBe(true);
    expect(dispatcher.hasCapability("missing")).toBe(false);
    expect(() => dispatcher.executeSync({ library: "", method: "run" })).toThrow("library must be");
    expect(() => dispatcher.executeSync({ library: "bad path", method: "run" })).toThrow("unsupported characters");
    expect(() => dispatcher.executeSync({ library: "x".repeat(65), method: "run" })).toThrow("1-64 bytes");
  });

  it("uses method, library, and compute executor precedence", async () => {
    const dispatcher = new RuntimeDispatcher();
    const method = { execute: vi.fn(async () => "method") };
    const library = { execute: vi.fn(async () => "library") };
    const compute = { execute: vi.fn(async () => "compute") };
    dispatcher.bindExecutor("image:resize", method);
    dispatcher.bindExecutor("image", library);
    dispatcher.bindExecutor("compute", compute);
    await expect(dispatcher.execute({ library: "image", method: "resize" })).resolves.toBe("method");
    dispatcher.unbindExecutor("image:resize");
    await expect(dispatcher.execute({ library: "image", method: "resize" })).resolves.toBe("library");
    dispatcher.unbindExecutor("image");
    await expect(dispatcher.decode({ library: "image", method: "resize" }, (value) => String(value))).resolves.toBe("compute");
  });

  it("tracks rejection and timeout metrics", async () => {
    vi.useFakeTimers();
    const dispatcher = new RuntimeDispatcher({ defaultTimeoutMs: 5 });
    dispatcher.bindExecutor("fail", { execute: async () => { throw "failure"; } });
    await expect(dispatcher.execute({ library: "fail", method: "run" })).rejects.toThrow("failure");
    dispatcher.bindExecutor("wait", { execute: () => new Promise(() => undefined) });
    const pending = dispatcher.execute({ library: "wait", method: "run" });
    const rejected = expect(pending).rejects.toThrow("timed out");
    await vi.advanceTimersByTimeAsync(6);
    await rejected;
    expect(dispatcher.getMetrics()).toMatchObject({ rejected: 1, timedOut: 1, pending: 0, maxPending: 1 });
    dispatcher.configure({ maxPendingRequests: 0, defaultTimeoutMs: 0 });
    vi.useRealTimers();
  });

  it("dispatches through wasm exports and frees owned regions", () => {
    const memory = new WebAssembly.Memory({ initial: 1 });
    let next = 128;
    const freed: Array<[number, number]> = [];
    const alloc = (size: number) => { const ptr = next; next += Math.max(1, size); return ptr; };
    const exports = {
      memory,
      compute_alloc: alloc,
      compute_free: (ptr: number, size: number) => freed.push([ptr, size]),
      compute_execute: (_lp: number, _ll: number, _mp: number, _ml: number, _ip: number, _il: number, _pp: number, _pl: number) => {
        const ptr = 1024;
        new DataView(memory.buffer).setUint32(ptr, 3, true);
        new Uint8Array(memory.buffer, ptr + 4, 3).set([7, 8, 9]);
        return ptr;
      },
    };
    const dispatcher = new RuntimeDispatcher();
    dispatcher.initialize(exports);
    expect(dispatcher.executeSync({ library: "image", method: "run", params: "fast", input: new Uint8Array([1]) })).toEqual(new Uint8Array([7, 8, 9]));
    expect(freed.length).toBe(5);
  });

  it("handles empty and malformed wasm results and missing exports", () => {
    const memory = new WebAssembly.Memory({ initial: 1 });
    const dispatcher = new RuntimeDispatcher();
    expect(() => dispatcher.executeSync({ library: "image", method: "run" })).toThrow("unavailable");
    dispatcher.initialize({ memory, compute_alloc: () => 64, compute_execute: () => 0 });
    expect(dispatcher.executeSync({ library: "image", method: "run" })).toBeNull();
    dispatcher.initialize({ memory, compute_alloc: () => 64, compute_execute: () => {
      new DataView(memory.buffer).setUint32(128, memory.buffer.byteLength, true);
      return 128;
    } });
    expect(() => dispatcher.executeSync({ library: "image", method: "run", params: new Uint8Array([1]) })).toThrow("exceeds memory buffer");
  });
});
