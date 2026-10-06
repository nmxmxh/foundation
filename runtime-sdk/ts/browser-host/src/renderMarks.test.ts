import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { PASS, markLane, renderFacts } from "./renderMarks";

describe("render performance markers", () => {
  beforeEach(() => {
    (globalThis as unknown as { window: unknown }).window = globalThis;
  });

  afterEach(() => {
    delete (globalThis as unknown as { window?: unknown }).window;
  });

  it("records marks into the facts snapshot", () => {
    markLane(PASS.blackHole, {
      lane: "webgpu",
      tier: 1,
      cadence: 40,
      scale: 0.62,
      fallback: false,
      reason: "primary",
    });

    const facts = renderFacts();
    expect(facts[PASS.blackHole]).toBeDefined();
    expect(facts[PASS.blackHole]?.lane).toBe("webgpu");
    expect(facts[PASS.blackHole]?.tier).toBe(1);
    expect(facts[PASS.blackHole]?.cadence).toBe(40);
    expect(facts[PASS.blackHole]?.fallback).toBe(false);
    expect(typeof facts[PASS.blackHole]?.at).toBe("number");
  });

  it("attaches snapshot to global window when available", () => {
    markLane(PASS.paper, {
      lane: "webgl2",
      fallback: true,
      reason: "no-gpu",
    });

    const win = globalThis as unknown as { __ovasabiRender?: ReturnType<typeof renderFacts> };
    expect(win.__ovasabiRender).toBeDefined();
    expect(win.__ovasabiRender?.[PASS.paper]?.lane).toBe("webgl2");
  });

  it("carries a correlation to the snapshot, the mark detail and the window", () => {
    // The three readers `markLane` publishes to. A field that reached only one
    // of them would be exactly the silent widening this declaration prevents.
    const marks: Array<{ name: string; detail?: unknown }> = [];
    const original = performance.mark;
    performance.mark = ((name: string, options?: { detail?: unknown }) => {
      marks.push({ name, detail: options?.detail });
      return { name, entryType: "mark", startTime: 0, duration: 0 } as PerformanceMark;
    }) as typeof performance.mark;
    try {
      markLane(PASS.orbit, { lane: "twin", correlation: "corr_abc123" });

      expect(renderFacts()[PASS.orbit]?.correlation).toBe("corr_abc123");
      expect(marks.find((entry) => entry.name === PASS.orbit)?.detail).toMatchObject({
        correlation: "corr_abc123",
      });
      const win = globalThis as unknown as { __ovasabiRender?: ReturnType<typeof renderFacts> };
      expect(win.__ovasabiRender?.[PASS.orbit]?.correlation).toBe("corr_abc123");
    } finally {
      performance.mark = original;
    }
  });

  it("keeps a null correlation distinct from an absent one, and neither is an error", () => {
    // `null` means a pass that ran with no read behind it. Both must reach the
    // snapshot unchanged rather than being coerced away, because a reader
    // distinguishing "fixture" from "field missing" is the reason the field
    // exists.
    markLane(PASS.field, { lane: "2d", correlation: null });
    markLane(PASS.footer, { lane: "2d" });

    expect(renderFacts()[PASS.field]?.correlation).toBeNull();
    expect("correlation" in (renderFacts()[PASS.footer] ?? {})).toBe(false);
    // `reason` keeps its own meaning; correlation never lands in it.
    expect(renderFacts()[PASS.field]?.reason).toBeUndefined();
  });

  it("does not derive a correlation from reason when none is given", () => {
    // The failure mode this field exists to prevent: one string carrying two
    // meanings, where the reader during an incident has to guess which half is
    // an identifier.
    markLane(PASS.clock, { lane: "frames", fallback: true, reason: "no shared memory" });

    expect(renderFacts()[PASS.clock]?.reason).toBe("no shared memory");
    expect(renderFacts()[PASS.clock]?.correlation).toBeUndefined();
  });
});
