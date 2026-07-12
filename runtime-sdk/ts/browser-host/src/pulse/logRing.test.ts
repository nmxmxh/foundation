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

  it("wraps bounded storage and counts corrupt encoded entries", () => {
    const ring = LogRingBuffer.create(48);
    ring.write({ level: "info", component: "a", message: "first", timestamp: 1, correlationId: "c" });
    ring.readAll();
    ring.write({ level: "info", component: "b", message: "second", timestamp: 2, correlationId: "c" });
    ring.write({ level: "info", component: "c", message: "third", timestamp: 3, correlationId: "c" });
    expect(ring.diagnostics().wrapCount).toBeGreaterThan(0);

    const sab = new SharedArrayBuffer(128);
    const words = new Uint32Array(sab);
    Atomics.store(words, 2, 64);
    const corrupt = new LogRingBuffer(sab);
    corrupt.writeRaw(new TextEncoder().encode("info\x1fruntime\x1f%ZZ\x1f1\x1fc\x1f"));
    expect(corrupt.readAll()).toEqual([]);
    expect(corrupt.diagnostics().corruptReads).toBe(1);
  });

  it("round-trips entries without optional metadata", () => {
    const ring = LogRingBuffer.create(256);
    ring.write({ level: "debug", component: "runtime", message: "plain", timestamp: 0, correlationId: "" });
    expect(ring.readAll()[0]).toMatchObject({ level: "debug", message: "plain", extra: undefined });
  });
});
