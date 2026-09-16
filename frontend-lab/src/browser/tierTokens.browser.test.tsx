import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  MinimalCard,
  MinimalGlobalStyles,
  MinimalSkeleton,
  MinimalThemeProvider,
} from "@ovasabi/ui-minimal";

/*
 * The tier contract is a computed-style contract: data-ui-tier on :root must
 * change what the engine actually draws. jsdom does not resolve custom
 * properties through a cascade, so this lane runs in Chromium.
 */

const nextFrames = () =>
  new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));

const setTier = async (tier: string | null) => {
  if (tier === null) document.documentElement.removeAttribute("data-ui-tier");
  else document.documentElement.setAttribute("data-ui-tier", tier);
  await nextFrames();
};

const mount = () =>
  render(
    <MinimalThemeProvider>
      <MinimalGlobalStyles />
      <MinimalCard data-testid="card">Card</MinimalCard>
      <MinimalSkeleton data-testid="skeleton" />
      <div data-testid="backdrop" style={{ backdropFilter: "var(--minimal-backdrop-filter, blur(4px))" }} />
    </MinimalThemeProvider>,
  );

describe("quality tier tokens (chromium)", () => {
  afterEach(async () => {
    cleanup();
    await setTier(null);
  });

  it("draws the full shadow, blur and shimmer when no tier is set", async () => {
    const { getByTestId } = mount();
    await nextFrames();
    expect(getComputedStyle(getByTestId("card")).boxShadow).toBe("rgba(28, 28, 30, 0.22) 0px 10px 28px -18px");
    expect(getComputedStyle(getByTestId("backdrop")).backdropFilter).toBe("blur(4px)");
    // The sweep lives on ::after so the compositor can run it alone.
    expect(getComputedStyle(getByTestId("skeleton"), "::after").animationName).not.toBe("none");
  });

  it("swaps to cheap shadows, drops blur and stops the shimmer on low_power", async () => {
    const { getByTestId } = mount();
    await setTier("low_power");
    expect(getComputedStyle(getByTestId("card")).boxShadow).toBe("rgba(28, 28, 30, 0.12) 0px 1px 2px 0px");
    expect(getComputedStyle(getByTestId("backdrop")).backdropFilter).toBe("none");
    expect(getComputedStyle(getByTestId("skeleton"), "::after").animationName).toBe("none");
  });

  it("keeps shadows but drops blur and shimmer on reduced_motion", async () => {
    const { getByTestId } = mount();
    await setTier("reduced_motion");
    expect(getComputedStyle(getByTestId("card")).boxShadow).toBe("rgba(28, 28, 30, 0.22) 0px 10px 28px -18px");
    expect(getComputedStyle(getByTestId("backdrop")).backdropFilter).toBe("none");
    expect(getComputedStyle(getByTestId("skeleton"), "::after").animationName).toBe("none");
  });

  it("changes nothing on balanced, which has no CSS yet", async () => {
    const { getByTestId } = mount();
    await setTier("balanced");
    expect(getComputedStyle(getByTestId("card")).boxShadow).toBe("rgba(28, 28, 30, 0.22) 0px 10px 28px -18px");
    expect(getComputedStyle(getByTestId("backdrop")).backdropFilter).toBe("blur(4px)");
  });
});
