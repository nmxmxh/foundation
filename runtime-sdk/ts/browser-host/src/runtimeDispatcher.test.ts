import { describe, expect, it } from "vitest";

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
});
