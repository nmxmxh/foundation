import { afterEach, describe, expect, it, vi } from "vitest";
import { readDeviceProfile, startingTier, type DeviceProfile } from "./deviceProfile";

/** A ladder shaped like a real one: five rungs, best first. */
const RUNGS = 5;

const profile = (overrides: Partial<DeviceProfile> = {}): DeviceProfile => ({
  coarsePointer: false,
  smallViewport: false,
  denseDisplay: false,
  lowConcurrency: false,
  lowMemory: false,
  reducedMotion: false,
  ...overrides,
});

describe("choosing an opening rung", () => {
  it("opens a desktop at the best rung, as it always did", () => {
    expect(startingTier(profile(), RUNGS)).toBe(0);
  });

  /*
   * The case the naive implementation gets wrong.
   *
   * A current phone trips every display signal there is — coarse pointer, small
   * viewport, DPR 3 — and holds rungs a laptop would struggle with. Counting
   * constraints and dividing by the total would pin it near the bottom and
   * leave it there for the six seconds a promotion takes.
   */
  it("stops a fast phone at the midpoint rather than the bottom", () => {
    const phone = profile({ coarsePointer: true, smallViewport: true, denseDisplay: true });
    expect(startingTier(phone, RUNGS)).toBe(2);
  });

  it("pushes past the midpoint only when the device says it is actually slow", () => {
    const cheapPhone = profile({
      coarsePointer: true,
      smallViewport: true,
      denseDisplay: true,
      lowConcurrency: true,
      lowMemory: true,
    });
    expect(startingTier(cheapPhone, RUNGS)).toBe(RUNGS - 1);
  });

  it("moves a slow wide machine off the top without treating it as a phone", () => {
    const oldLaptop = profile({ lowConcurrency: true, lowMemory: true });
    expect(startingTier(oldLaptop, RUNGS)).toBe(2);
  });

  /*
   * An unread signal is not an unconstrained one. `deviceMemory` is Chromium
   * only, so scoring its absence as "plenty of memory" would make every Safari
   * user look faster than they are — a browser-sniff wearing a heuristic's
   * clothes.
   */
  it("scores an unreadable signal as unknown rather than as capable", () => {
    const partial = profile({ lowConcurrency: true, lowMemory: null });
    const known = profile({ lowConcurrency: true, lowMemory: false });
    expect(startingTier(partial, RUNGS)).toBeGreaterThan(startingTier(known, RUNGS));
  });

  it("sends a stated preference for less motion straight to the cheapest rung", () => {
    expect(startingTier(profile({ reducedMotion: true }), RUNGS)).toBe(RUNGS - 1);
  });

  it("never indexes off a short ladder", () => {
    const worst = profile({
      coarsePointer: true,
      smallViewport: true,
      denseDisplay: true,
      lowConcurrency: true,
      lowMemory: true,
    });
    expect(startingTier(worst, 1)).toBe(0);
    expect(startingTier(worst, 0)).toBe(0);
    expect(startingTier(worst, 2)).toBe(1);
  });
});

describe("reading the device", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reports null for every signal it could not ask for", () => {
    vi.stubGlobal("matchMedia", undefined);
    vi.stubGlobal("navigator", {});
    vi.stubGlobal("devicePixelRatio", undefined);

    const read = readDeviceProfile();
    expect(read).toEqual({
      coarsePointer: null,
      smallViewport: null,
      denseDisplay: null,
      lowConcurrency: null,
      lowMemory: null,
      reducedMotion: null,
    });
    // Nothing readable must behave exactly as the SDK did before the prior
    // existed: open at the best rung and let the ladder find its way down.
    expect(startingTier(read, RUNGS)).toBe(0);
  });

  it("survives a matchMedia that throws on a query it does not know", () => {
    vi.stubGlobal("matchMedia", () => {
      throw new Error("unsupported media feature");
    });
    vi.stubGlobal("navigator", { hardwareConcurrency: 8 });
    expect(readDeviceProfile().coarsePointer).toBeNull();
  });

  it("reads the signals a phone-shaped global reports", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("coarse") || query.includes("48rem"),
    }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 8, deviceMemory: 8 });
    vi.stubGlobal("devicePixelRatio", 3);

    expect(readDeviceProfile()).toEqual({
      coarsePointer: true,
      smallViewport: true,
      denseDisplay: true,
      lowConcurrency: false,
      lowMemory: false,
      reducedMotion: false,
    });
  });
});
