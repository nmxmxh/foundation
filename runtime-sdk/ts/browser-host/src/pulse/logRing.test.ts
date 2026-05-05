import { describe, expect, it } from "vitest";
import { LogRingBuffer } from "./logRing";

describe("LogRingBuffer diagnostics", () => {
  it("preserves request identity fields in readable entries", () => {
    const ring = LogRingBuffer.create(512);
    ring.write({
      level: "info",
      component: "runtime",
      message: "dispatch complete",
      timestamp: 123,
      correlationId: "corr_keep",
      extra: { requestId: "req_keep", eventType: "media:process_asset:v1:requested" },
    });

    expect(ring.readAll()).toEqual([
      {
        level: "info",
        component: "runtime",
        message: "dispatch complete",
        timestamp: 123,
        correlationId: "corr_keep",
        extra: { requestId: "req_keep", eventType: "media:process_asset:v1:requested" },
      },
    ]);
    expect(ring.diagnostics()).toEqual({ wrapCount: 0, droppedWrites: 0, corruptReads: 0 });
  });

  it("counts oversized log writes instead of dropping them invisibly", () => {
    const ring = LogRingBuffer.create(32);
    ring.write({
      level: "error",
      component: "runtime",
      message: "x".repeat(256),
      timestamp: 123,
      correlationId: "corr_large",
    });

    expect(ring.readAll()).toEqual([]);
    expect(ring.diagnostics().droppedWrites).toBe(1);
  });
});
