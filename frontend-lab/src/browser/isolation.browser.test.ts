import { describe, expect, it } from "vitest";

/*
 * The precondition for every worker-lane test in this lab. If the browser
 * lane is not cross-origin isolated, SharedArrayBuffer does not exist and the
 * frame clock, arenas and render-state channel all take their fallbacks — so
 * a worker-lane test would pass while testing the wrong lane. Fail loudly here
 * instead.
 */
describe("browser lane isolation (chromium)", () => {
  it("is cross-origin isolated, with SharedArrayBuffer and module workers", () => {
    expect(self.crossOriginIsolated).toBe(true);
    expect(typeof SharedArrayBuffer).toBe("function");
    expect(typeof Worker).toBe("function");
  });
});
