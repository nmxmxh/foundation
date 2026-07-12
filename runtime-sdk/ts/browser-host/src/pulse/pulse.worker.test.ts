import { afterEach, describe, expect, it, vi } from "vitest";
import { EPOCH_SLOT_COUNT, IDX_RUNTIME_TICK, IDX_VISIBILITY_STATE } from "../generated/runtimeBuffer";

describe("pulse worker", () => {
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.resetModules(); });

  it("processes lifecycle, visibility, and watcher messages", async () => {
    vi.useFakeTimers();
    const posted: unknown[] = [];
    const scope: { postMessage: (message: unknown) => void; onmessage: ((event: MessageEvent) => void) | null } = {
      postMessage: (message) => posted.push(message), onmessage: null,
    };
    vi.stubGlobal("self", scope);
    let now = 0;
    vi.spyOn(performance, "now").mockImplementation(() => { now += 100; return now; });
    await import("./pulse.worker");
    const buffer = new SharedArrayBuffer(EPOCH_SLOT_COUNT * Int32Array.BYTES_PER_ELEMENT);
    const send = (data: unknown) => scope.onmessage?.(new MessageEvent("message", { data }));
    send({ type: "WATCH_INDICES", payload: { indices: [IDX_RUNTIME_TICK] } });
    send({ type: "SET_TPS", payload: { tps: 10 } });
    send({ type: "INIT", payload: { buffer } });
    await vi.advanceTimersByTimeAsync(20);
    expect(new Int32Array(buffer)[IDX_RUNTIME_TICK]).toBeGreaterThan(0);
    send({ type: "SET_VISIBILITY", payload: { visible: false } });
    expect(new Int32Array(buffer)[IDX_VISIBILITY_STATE]).toBe(0);
    Atomics.add(new Int32Array(buffer), IDX_RUNTIME_TICK, 1);
    await vi.advanceTimersByTimeAsync(20);
    expect(posted).toEqual(expect.arrayContaining([expect.objectContaining({ type: "EPOCH_CHANGE" })]));
    send({ type: "UNWATCH_INDICES", payload: { indices: [IDX_RUNTIME_TICK] } });
    send({ type: "STOP" });
  });

  it("polls watched epochs when waitAsync is unavailable", async () => {
    vi.useFakeTimers();
    const posted: unknown[] = [];
    const scope: { postMessage: (message: unknown) => void; onmessage: ((event: MessageEvent) => void) | null } = {
      postMessage: (message) => posted.push(message), onmessage: null,
    };
    vi.stubGlobal("self", scope);
    const original = (Atomics as { waitAsync?: unknown }).waitAsync;
    Object.defineProperty(Atomics, "waitAsync", { configurable: true, value: undefined });
    await import("./pulse.worker");
    const buffer = new SharedArrayBuffer(EPOCH_SLOT_COUNT * 4);
    const send = (data: unknown) => scope.onmessage?.(new MessageEvent("message", { data }));
    send({ type: "WATCH_INDICES", payload: { indices: [IDX_VISIBILITY_STATE, IDX_VISIBILITY_STATE] } });
    send({ type: "INIT", payload: { buffer } });
    Atomics.store(new Int32Array(buffer), IDX_VISIBILITY_STATE, 1);
    await vi.advanceTimersByTimeAsync(17);
    expect(posted).toEqual(expect.arrayContaining([expect.objectContaining({ type: "EPOCH_CHANGE" })]));
    send({ type: "STOP" });
    Object.defineProperty(Atomics, "waitAsync", { configurable: true, value: original });
  });
});
