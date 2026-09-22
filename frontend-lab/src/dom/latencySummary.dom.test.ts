import { describe, expect, it } from "vitest";
import { summarizeLatency } from "../latencySummary";

describe("lab latency evidence", () => {
  it("uses nearest rank at exact percentile boundaries", () => {
    const values = Array.from({ length: 100 }, (_, index) => 100 - index);
    expect(summarizeLatency(values)).toEqual({ n: 100, p50: 50, p95: 95, max: 100, p95UnderSampled: false });
    expect(values[0]).toBe(100);
  });

  it("marks small samples and retains their observed range", () => {
    expect(summarizeLatency([8, 2])).toEqual({ n: 2, p50: 2, p95: 8, max: 8, p95UnderSampled: true });
    expect(summarizeLatency([])).toEqual({ n: 0, p50: null, p95: null, max: null, p95UnderSampled: true });
  });

  it.each([NaN, Infinity, -1])("rejects invalid duration %s", (value) => {
    expect(() => summarizeLatency([value])).toThrow(RangeError);
  });
});
