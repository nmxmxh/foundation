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
});
