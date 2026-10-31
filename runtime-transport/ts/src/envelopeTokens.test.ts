import { describe, expect, it } from "vitest";
import { createEnvelope } from "./index";

describe("createEnvelope metadata ids", () => {
  it("gives each id its own random part", () => {
    const { metadata } = createEnvelope({ eventType: "runtime:dispatch:v1:requested", payload: {} });
    const suffix = (id: string) => id.slice(id.indexOf("_") + 1);
    expect(suffix(metadata.correlationId)).not.toBe(suffix(metadata.requestId));
    expect(suffix(metadata.requestId)).not.toBe(suffix(metadata.idempotencyKey));
    expect(suffix(metadata.correlationId)).not.toBe(suffix(metadata.idempotencyKey));
  });

  it("keeps caller ids and stays unique across many envelopes", () => {
    const kept = createEnvelope({
      eventType: "runtime:dispatch:v1:requested",
      payload: {},
      correlationId: "corr_given",
      idempotencyKey: "idem_given",
    });
    expect(kept.metadata.correlationId).toBe("corr_given");
    expect(kept.metadata.idempotencyKey).toBe("idem_given");

    const keys = new Set<string>();
    for (let i = 0; i < 5000; i++) {
      keys.add(createEnvelope({ eventType: "runtime:dispatch:v1:requested", payload: {} }).metadata.idempotencyKey);
    }
    expect(keys.size).toBe(5000);
  });
});
