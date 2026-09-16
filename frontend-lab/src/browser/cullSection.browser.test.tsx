import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MinimalCullSection, MinimalThemeProvider } from "@ovasabi/ui-minimal";

/*
 * P4's invariants are engine behaviour: an offscreen section is skipped, comes
 * back when scrolled to, and the scroll height does not jump either way. Only a
 * real engine implements content-visibility, so this lane runs in Chromium.
 */

const frames = (count = 3) =>
  new Promise<void>((resolve) => {
    const step = (left: number) => (left === 0 ? resolve() : requestAnimationFrame(() => step(left - 1)));
    step(count);
  });

const SECTION_PX = 300;
const COUNT = 20;

const mount = (estimate: string) =>
  render(
    <MinimalThemeProvider>
      <div data-testid="scroller" style={{ height: "400px", overflowY: "auto" }}>
        {Array.from({ length: COUNT }, (_, index) => (
          <MinimalCullSection key={index} estimatedSize={estimate} data-testid={`section-${index}`}>
            <div style={{ height: `${SECTION_PX}px` }}>Section {index}</div>
          </MinimalCullSection>
        ))}
      </div>
    </MinimalThemeProvider>,
  );

/*
 * A content-visibility: auto element is never skipped itself — its *contents*
 * are. checkVisibility({ contentVisibilityAuto: true }) reports whether an
 * element sits inside an ancestor that is skipping, so the question is asked
 * of the section's content, not the section.
 */
const skipped = (section: Element) => {
  const content = section.firstElementChild;
  if (!content) throw new Error("cull section has no content to check");
  return !content.checkVisibility({ contentVisibilityAuto: true });
};

describe("MinimalCullSection (chromium)", () => {
  afterEach(() => cleanup());

  it("skips sections far outside the scroller and renders the ones in view", async () => {
    const { getByTestId } = mount(`${SECTION_PX}px`);
    await frames();
    expect(skipped(getByTestId("section-0"))).toBe(false);
    expect(skipped(getByTestId(`section-${COUNT - 1}`))).toBe(true);
  });

  it("restores a section when it is scrolled into view", async () => {
    const { getByTestId } = mount(`${SECTION_PX}px`);
    await frames();
    const scroller = getByTestId("scroller");
    scroller.scrollTop = scroller.scrollHeight;
    await frames(4);
    expect(skipped(getByTestId(`section-${COUNT - 1}`))).toBe(false);
    expect(skipped(getByTestId("section-0"))).toBe(true);
  });

  it("holds the scroll height steady when the estimate matches the content", async () => {
    const { getByTestId } = mount(`${SECTION_PX}px`);
    await frames();
    const scroller = getByTestId("scroller");
    const before = scroller.scrollHeight;
    expect(before).toBe(SECTION_PX * COUNT);
    scroller.scrollTop = scroller.scrollHeight;
    await frames(4);
    scroller.scrollTop = 0;
    await frames(4);
    expect(scroller.scrollHeight).toBe(before);
  });

  it("remembers a section's real size once rendered, even with a wrong estimate", async () => {
    const { getByTestId } = mount("50px");
    await frames();
    const scroller = getByTestId("scroller");
    // Walk the whole list so every section is laid out once.
    for (let top = 0; top <= scroller.scrollHeight; top += 200) {
      scroller.scrollTop = top;
      await frames(2);
    }
    scroller.scrollTop = 0;
    await frames(4);
    // Every section has now been rendered at its real 300px; `auto` keeps that
    // size while it is skipped again, so the total reflects real content.
    expect(scroller.scrollHeight).toBe(SECTION_PX * COUNT);
  });
});
