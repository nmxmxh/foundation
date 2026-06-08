import { describe, expect, it } from "vitest";

import { RuntimeWorkerPool } from "./runtimeWorkerPool";

class MockWorker extends EventTarget {
  public sent: unknown[] = [];

  postMessage(message: unknown): void {
    this.sent.push(message);
    const request = message as { id: number; library: string; method: string };
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
}

describe("RuntimeWorkerPool", () => {
  it("routes execute requests through ready workers", async () => {
    const worker = new MockWorker();
    const pool = new RuntimeWorkerPool();
    pool.addWorker("compute", worker as unknown as Worker);
    worker.emitReady();

    await expect(pool.execute({ library: "image", method: "resize" })).resolves.toBe("image:resize");
    expect(worker.sent).toHaveLength(1);
  });
});
