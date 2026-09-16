import { describe, expect, it } from "vitest";
import type { DeviceProfile } from "./deviceProfile";
import {
  DEMOTE_AFTER_WINDOWS,
  PROMOTE_AFTER_WINDOWS,
  UI_TIER_ATTRIBUTE,
  createUiQuality,
  type UiQualityOptions,
} from "./uiQuality";

const desktop: DeviceProfile = {
  coarsePointer: false,
  smallViewport: false,
  denseDisplay: false,
  lowConcurrency: false,
  lowMemory: false,
  reducedMotion: false,
};
/** The emulator this was measured on reported 4 cores and 2 GB. */
const floorPhone: DeviceProfile = {
  coarsePointer: true,
  smallViewport: true,
  denseDisplay: true,
  lowConcurrency: true,
  lowMemory: true,
  reducedMotion: false,
};
const flagshipPhone: DeviceProfile = { ...floorPhone, lowConcurrency: false, lowMemory: false };

const bad = { frames: 60, slowFrames: 20, degraded: false }; // 33%: the measured WebView
const clean = { frames: 60, slowFrames: 3, degraded: false }; // 5%: the plain-list control

const make = (profile: DeviceProfile, extra: Partial<UiQualityOptions> = {}) => {
  const attributes: Record<string, string> = {};
  const quality = createUiQuality({
    profile,
    saveData: null,
    mark: false,
    target: { setAttribute: (name, value) => (attributes[name] = value) },
    ...extra,
  });
  return { quality, attributes };
};

describe("uiQuality", () => {
  it("starts from the device prior and writes the attribute immediately", () => {
    expect(make(desktop).quality.tier()).toBe("high");
    expect(make(flagshipPhone).quality.tier()).toBe("balanced");
    const { quality, attributes } = make(floorPhone);
    expect(quality.tier()).toBe("low_power");
    expect(attributes[UI_TIER_ATTRIBUTE]).toBe("low_power");
  });

  it("caps a save-data session at balanced", () => {
    expect(make(desktop, { saveData: true }).quality.tier()).toBe("balanced");
  });

  it("honours reduced motion as a preference that frames cannot override", () => {
    const { quality } = make({ ...desktop, reducedMotion: true });
    expect(quality.tier()).toBe("reduced_motion");
    for (let i = 0; i < PROMOTE_AFTER_WINDOWS * 2; i++) quality.recordWindow(bad);
    expect(quality.tier()).toBe("reduced_motion");
  });

  it("does not demote on a single bad window, and demotes on a burst", () => {
    const { quality, attributes } = make(desktop);
    quality.recordWindow(bad);
    expect(quality.tier()).toBe("high");
    for (let i = 1; i < DEMOTE_AFTER_WINDOWS; i++) quality.recordWindow(bad);
    expect(quality.tier()).toBe("balanced");
    expect(attributes[UI_TIER_ATTRIBUTE]).toBe("balanced");
  });

  it("forgives an old bad window after a clean stretch", () => {
    const { quality } = make(desktop);
    quality.recordWindow(bad);
    quality.recordWindow(clean);
    quality.recordWindow(clean);
    quality.recordWindow(clean);
    quality.recordWindow(bad);
    expect(quality.tier()).toBe("high");
  });

  it("promotes only after a long clean run", () => {
    const { quality } = make(floorPhone);
    for (let i = 0; i < PROMOTE_AFTER_WINDOWS - 1; i++) quality.recordWindow(clean);
    expect(quality.tier()).toBe("low_power");
    quality.recordWindow(clean);
    expect(quality.tier()).toBe("balanced");
  });

  it("ignores windows that are degraded or too short to judge", () => {
    const { quality } = make(desktop);
    for (let i = 0; i < 10; i++) {
      quality.recordWindow({ frames: 60, slowFrames: 60, degraded: true });
      quality.recordWindow({ frames: 10, slowFrames: 10, degraded: false });
    }
    expect(quality.tier()).toBe("high");
  });

  it("holds a floor from a platform signal and releases it", () => {
    const { quality } = make(desktop);
    const seen: string[] = [];
    quality.subscribe((tier, reason) => seen.push(`${tier}|${reason}`));
    expect(quality.setFloor("low_power", "thermal headroom 0.9")).toBe("low_power");
    for (let i = 0; i < PROMOTE_AFTER_WINDOWS * 2; i++) quality.recordWindow(clean);
    expect(quality.tier()).toBe("low_power");
    expect(quality.setFloor("high", "thermal recovered")).toBe("high");
    expect(seen).toEqual(["low_power|floor low_power: thermal headroom 0.9", "high|floor high: thermal recovered"]);
  });

  it("logs every change with its reason, and stops after dispose", () => {
    const { quality } = make(desktop);
    const seen: string[] = [];
    const unsubscribe = quality.subscribe((tier, reason) => seen.push(`${tier}|${reason}`));
    quality.recordWindow(bad);
    quality.recordWindow(bad);
    expect(seen).toEqual(["balanced|demote: 33% slow frames"]);
    unsubscribe();
    quality.dispose();
    quality.recordWindow(bad);
    quality.recordWindow(bad);
    expect(seen).toHaveLength(1);
    expect(quality.tier()).toBe("balanced");
  });
});
