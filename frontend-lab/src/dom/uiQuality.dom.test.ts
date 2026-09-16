import { afterEach, describe, expect, it, vi } from "vitest";
import { UI_TIER_ATTRIBUTE, createUiQuality, type DeviceProfile } from "@ovasabi/runtime-browser";

const desktop: DeviceProfile = {
  coarsePointer: false,
  smallViewport: false,
  denseDisplay: false,
  lowConcurrency: false,
  lowMemory: false,
  reducedMotion: false,
};

const bad = { frames: 60, slowFrames: 20, degraded: false };

describe("uiQuality against a real document (dom)", () => {
  afterEach(() => {
    document.documentElement.removeAttribute(UI_TIER_ATTRIBUTE);
    vi.unstubAllGlobals();
  });

  it("writes the tier to document.documentElement by default and moves it", () => {
    const quality = createUiQuality({ profile: desktop, saveData: null, mark: false });
    expect(document.documentElement.getAttribute(UI_TIER_ATTRIBUTE)).toBe("high");
    quality.recordWindow(bad);
    quality.recordWindow(bad);
    expect(document.documentElement.getAttribute(UI_TIER_ATTRIBUTE)).toBe("balanced");
    quality.dispose();
  });

  it("reads the device itself when no profile is given, honouring reduced motion", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(prefers-reduced-motion: reduce)",
      media: query,
      addEventListener() {},
      removeEventListener() {},
    }));
    const quality = createUiQuality({ saveData: null, mark: false });
    expect(quality.tier()).toBe("reduced_motion");
    expect(document.documentElement.getAttribute(UI_TIER_ATTRIBUTE)).toBe("reduced_motion");
    quality.dispose();
  });

  it("caps a save-data connection at balanced when read from navigator", () => {
    vi.stubGlobal("navigator", { ...navigator, connection: { saveData: true } });
    const quality = createUiQuality({ profile: desktop, mark: false });
    expect(quality.tier()).toBe("balanced");
    quality.dispose();
  });
});
