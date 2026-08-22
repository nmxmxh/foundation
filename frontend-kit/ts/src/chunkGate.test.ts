import { afterEach, describe, expect, it, vi } from "vitest";

import { ChunkLoadError, createChunkGate } from "./chunkGate";

afterEach(() => {
  vi.useRealTimers();
});

describe("createChunkGate", () => {
  it("resolves the loaded module", async () => {
    const gate = createChunkGate(async () => ({ default: "page" }), 5_000);

    await expect(gate.load()).resolves.toEqual({ default: "page" });
  });

  it("shares one attempt between concurrent callers", async () => {
    let calls = 0;
    let release!: (value: { default: string }) => void;
    const gate = createChunkGate(
      () =>
        new Promise((resolve) => {
          calls += 1;
          release = resolve;
        }),
      5_000
    );

    const first = gate.load();
    const second = gate.load();
    release({ default: "shared" });

    await expect(first).resolves.toEqual({ default: "shared" });
    await expect(second).resolves.toEqual({ default: "shared" });
    expect(calls).toBe(1);
  });

  it("rejects with ChunkLoadError when the loader stalls past the timeout", async () => {
    vi.useFakeTimers();
    const gate = createChunkGate(() => new Promise<never>(() => undefined), 50);

    let caught: unknown;
    const settled = gate.load().catch((error) => {
      caught = error;
      return null;
    });

    await vi.advanceTimersByTimeAsync(49);
    expect(caught).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1);
    await settled;

    expect(caught).toBeInstanceOf(ChunkLoadError);
    expect(String(caught)).toContain("50ms");
  });

  it("clears the timeout when the loader settles first", async () => {
    vi.useFakeTimers();
    const gate = createChunkGate(async () => ({ ok: true }), 100);

    const pending = gate.load();
    await vi.advanceTimersByTimeAsync(99);

    await expect(pending).resolves.toEqual({ ok: true });
    // No stray timer fires afterwards.
    await expect(vi.runOnlyPendingTimersAsync()).resolves.toBeDefined();
  });

  it("wraps loader rejections and preserves the cause", async () => {
    const boom = new Error("network hiccup");
    const gate = createChunkGate(async () => {
      throw boom;
    }, 5_000);

    const caught = await gate.load().then(
      () => null,
      (error) => error
    );
    expect(caught).toBeInstanceOf(ChunkLoadError);
    expect((caught as ChunkLoadError).cause).toBe(boom);
  });

  it("passes through existing ChunkLoadError unchanged", async () => {
    const original = new ChunkLoadError("already classified");
    const gate = createChunkGate(() => Promise.reject(original), 5_000);

    await expect(gate.load()).rejects.toBe(original);
  });

  it("does not poison future loads after a failure", async () => {
    let calls = 0;
    const gate = createChunkGate(async () => {
      calls += 1;
      if (calls === 1) throw new Error("first attempt fails");
      return { recovered: true };
    }, 5_000);

    await expect(gate.load()).rejects.toBeInstanceOf(ChunkLoadError);
    await expect(gate.load()).resolves.toEqual({ recovered: true });
    expect(calls).toBe(2);
  });

  it("does not poison future loads after a timeout", async () => {
    vi.useFakeTimers();
    let calls = 0;
    const gate = createChunkGate(
      async () => {
        calls += 1;
        if (calls === 1) {
          await new Promise(() => undefined);
        }
        return { recovered: true };
      },
      25
    );

    const first = expect(gate.load()).rejects.toBeInstanceOf(ChunkLoadError);
    await vi.advanceTimersByTimeAsync(30);
    await first;

    await expect(gate.load()).resolves.toEqual({ recovered: true });
    expect(calls).toBe(2);
  });

  it("reset() forces a fresh load even after success", async () => {
    let calls = 0;
    const gate = createChunkGate(async () => {
      calls += 1;
      return { call: calls };
    }, 5_000);

    await expect(gate.load()).resolves.toEqual({ call: 1 });
    await expect(gate.load()).resolves.toEqual({ call: 1 });
    gate.reset();
    await expect(gate.load()).resolves.toEqual({ call: 2 });
  });

  it("rejects non-positive or non-finite timeouts synchronously", () => {
    expect(() => createChunkGate(async () => null, 0)).toThrow(/positive/);
    expect(() => createChunkGate(async () => null, Number.NaN)).toThrow(/positive/);
  });
});
