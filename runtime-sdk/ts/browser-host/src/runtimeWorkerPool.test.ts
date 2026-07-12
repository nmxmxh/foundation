import { describe, expect, it, vi } from "vitest";

import { RuntimeWorkerPool } from "./runtimeWorkerPool";

class MockWorker extends EventTarget {
  public sent: unknown[] = [];
  public autoSettle = true;

  postMessage(message: unknown): void {
    this.sent.push(message);
    const request = message as { id: number; library: string; method: string };
    if (!this.autoSettle) return;
    setTimeout(() => {
      this.dispatchEvent(new MessageEvent("message", {
        data: {
          type: "result",
          id: request.id,
          result: `${request.library}:${request.method}`,
        },
      }));
    }, 0);
  }

  emitReady(): void {
    this.dispatchEvent(new MessageEvent("message", { data: { type: "ready" } }));
  }

  settle(index = 0): void {
    const request = this.sent[index] as { id: number; library: string; method: string };
    this.dispatchEvent(new MessageEvent("message", {
      data: { type: "result", id: request.id, result: `${request.library}:${request.method}` },
    }));
  }
}

describe("RuntimeWorkerPool", () => {
  it("routes execute requests through ready workers", async () => {
    const worker = new MockWorker();
    const pool = new RuntimeWorkerPool();
    pool.addWorker("compute", worker as unknown as Worker);
    worker.emitReady();
    worker.dispatchEvent(new MessageEvent("message", { data: { type: "result" } }));

    await expect(pool.execute({ library: "image", method: "resize" })).resolves.toBe("image:resize");
    expect(worker.sent).toHaveLength(1);
  });

  it("balances concurrent requests by in-flight load and round-robin ties", async () => {
    const first = new MockWorker();
    const second = new MockWorker();
    first.autoSettle = false;
    second.autoSettle = false;
    const pool = new RuntimeWorkerPool();
    pool.addWorker("first", first as unknown as Worker, true);
    pool.addWorker("second", second as unknown as Worker, true);

    const requests = Array.from({ length: 4 }, (_, index) =>
      pool.execute({ library: "image", method: `resize-${index}` })
    );
    expect(first.sent).toHaveLength(2);
    expect(second.sent).toHaveLength(2);
    first.settle(0);
    first.settle(1);
    second.settle(0);
    second.settle(1);
    await expect(Promise.all(requests)).resolves.toHaveLength(4);

    const next = pool.execute({ library: "image", method: "next" });
    expect(first.sent.length + second.sent.length).toBe(5);
    if (first.sent.length === 3) first.settle(2);
    else second.settle(2);
    await expect(next).resolves.toBe("image:next");
  });

  it("releases load accounting after synchronous postMessage failure", async () => {
    const broken = new MockWorker();
    broken.postMessage = () => { throw new Error("post failed"); };
    const healthy = new MockWorker();
    const pool = new RuntimeWorkerPool();
    pool.addWorker("broken", broken as unknown as Worker, true);
    pool.addWorker("healthy", healthy as unknown as Worker, true);

    await expect(pool.execute({ library: "image", method: "broken" })).rejects.toThrow("post failed");
    await expect(pool.execute({ library: "image", method: "healthy" })).resolves.toBe("image:healthy");
  });

  it("rejects absent workers, saturation, timeout, worker errors, and removal", async () => {
    vi.useFakeTimers();
    const pool = new RuntimeWorkerPool({ maxPendingRequests: 1, defaultTimeoutMs: 5 });
    await expect(pool.execute({ library: "image", method: "none" })).rejects.toThrow("no ready runtime worker");
    const worker = new MockWorker();
    worker.autoSettle = false;
    pool.addWorker("compute", worker as unknown as Worker, true);
    const timed = pool.execute({ library: "image", method: "wait" });
    const timedRejection = expect(timed).rejects.toThrow("timed out");
    await expect(pool.execute({ library: "image", method: "saturated" })).rejects.toThrow("queue saturated");
    await vi.advanceTimersByTimeAsync(6);
    await timedRejection;

    const removed = pool.execute({ library: "image", method: "removed" });
    const removedRejection = expect(removed).rejects.toThrow("removed");
    pool.removeWorker("compute");
    await removedRejection;
    pool.removeWorker("missing");

    const failed = new MockWorker();
    failed.autoSettle = false;
    pool.addWorker("failed", failed as unknown as Worker, true);
    const pending = pool.execute({ library: "image", method: "failed" });
    const failure = expect(pending).rejects.toThrow("runtime worker failed failed");
    failed.dispatchEvent(new Event("error"));
    await failure;
    pool.terminate();
    vi.useRealTimers();
  });

  it("settles explicit worker error responses", async () => {
    const worker = new MockWorker();
    worker.autoSettle = false;
    const pool = new RuntimeWorkerPool();
    pool.addWorker("compute", worker as unknown as Worker, true);
    const pending = pool.execute({ library: "image", method: "error" });
    const request = worker.sent[0] as { id: number };
    worker.dispatchEvent(new MessageEvent("message", { data: { type: "error", id: request.id, error: "unit failed" } }));
    await expect(pending).rejects.toThrow("unit failed");
  });
});
