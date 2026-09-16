import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MinimalGlobalStyles, MinimalSkeleton, MinimalThemeProvider } from "@ovasabi/ui-minimal";

/*
 * MinimalSkeleton pauses its sweep while off screen (research ledger finding 8:
 * every always-running placeholder design left 55–71% of frames slow in the
 * profile; paused-off-screen matched no animation at all). Visibility is an
 * engine fact, so this lane runs in Chromium.
 */

const nextFrames = (count = 3) =>
  new Promise<void>((resolve) => {
    const tick = (left: number) => (left === 0 ? resolve() : requestAnimationFrame(() => tick(left - 1)));
    tick(count);
  });

const sweep = (element: HTMLElement) => getComputedStyle(element, "::after");

describe("MinimalSkeleton off-screen pausing (chromium)", () => {
  afterEach(() => {
    cleanup();
    window.scrollTo(0, 0);
  });

  it("runs the sweep on screen and pauses it far off screen, then resumes on scroll", async () => {
    const { getByTestId } = render(
      <MinimalThemeProvider>
        <MinimalGlobalStyles />
        <MinimalSkeleton data-testid="near" />
        <div style={{ height: "6000px" }} />
        <MinimalSkeleton data-testid="far" />
      </MinimalThemeProvider>,
    );
    await nextFrames();

    const near = getByTestId("near");
    const far = getByTestId("far");
    expect(near.hasAttribute("data-minimal-offscreen")).toBe(false);
    expect(sweep(near).animationPlayState).toBe("running");
    expect(far.hasAttribute("data-minimal-offscreen"), "an off-screen skeleton was never marked").toBe(true);
    expect(sweep(far).animationPlayState).toBe("paused");
    // Paused, not removed: it resumes mid-sweep rather than restarting.
    expect(sweep(far).animationName).not.toBe("none");

    far.scrollIntoView();
    await nextFrames(4);
    expect(far.hasAttribute("data-minimal-offscreen")).toBe(false);
    expect(sweep(far).animationPlayState).toBe("running");
    expect(near.hasAttribute("data-minimal-offscreen")).toBe(true);
  });
});
