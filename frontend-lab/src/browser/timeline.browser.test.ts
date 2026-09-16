import { afterEach, describe, expect, it } from "vitest";
import { createMinimalTimeline, minimalExit, minimalKeyframes } from "@ovasabi/ui-minimal";

/*
 * createMinimalTimeline on a real Web Animations engine (Chromium).
 *
 * jsdom has no WAAPI, so these run in the browser lane. They pin the parts a
 * GSAP-shaped API promises — positions, labels, stagger, seek, reverse, repeat,
 * hold — and the part this module exists for: while it plays, the timeline
 * writes no style from JavaScript.
 */

const made: HTMLElement[] = [];
const box = () => {
  const el = document.createElement("div");
  el.style.cssText = "width:40px;height:40px;background:#16a34a";
  document.body.appendChild(el);
  made.push(el);
  return el;
};
const delays = (els: Element[]) => els.map((el) => el.getAnimations()[0]?.effect?.getTiming().delay);
const microtask = () => new Promise<void>((resolve) => queueMicrotask(resolve));
const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

afterEach(() => {
  for (const el of made.splice(0)) el.remove();
});

describe("minimal timeline (chromium)", () => {
  it("places tweens by GSAP-style position: append, <, >, +=, labels", () => {
    const [a, b, c, d, e] = [box(), box(), box(), box(), box()];
    const tl = createMinimalTimeline({ paused: true, defaults: { duration: 200 } });
    tl.add(a, minimalKeyframes.fadeIn) // 0..200
      .add(b, minimalKeyframes.fadeIn, {}, "<100") // starts 100 after a's start
      .add(c, minimalKeyframes.fadeIn, {}, ">") // at b's end: 300
      .addLabel("settle", "+=50") // 550
      .add(d, minimalKeyframes.fadeIn, {}, "settle+=25") // 575
      .add(e, minimalKeyframes.fadeIn, { duration: 100 }); // append: 775
    expect(delays([a, b, c, d, e])).toEqual([0, 100, 300, 575, 775]);
    expect(tl.labels.settle).toBe(550);
    expect(tl.duration).toBe(875);
    tl.cancel();
  });

  it("staggers across targets, from the start or from the centre", () => {
    const row = [box(), box(), box(), box(), box()];
    createMinimalTimeline({ paused: true }).add(row, minimalKeyframes.popIn, { stagger: 40 });
    expect(delays(row)).toEqual([0, 40, 80, 120, 160]);
    for (const el of row) el.getAnimations().forEach((a) => a.cancel());

    createMinimalTimeline({ paused: true }).add(row, minimalKeyframes.popIn, { stagger: { each: 40, from: "center" } });
    expect(delays(row)).toEqual([80, 40, 0, 40, 80]);
  });

  it("seeks and scrubs every child to one time", () => {
    const [a, b] = [box(), box()];
    const tl = createMinimalTimeline({ paused: true, defaults: { duration: 200, easing: "linear" } });
    tl.add(a, [{ opacity: 0 }, { opacity: 1 }]).add(b, [{ opacity: 0 }, { opacity: 1 }], {}, 200);
    tl.seek(100);
    expect(Number(getComputedStyle(a).opacity)).toBeCloseTo(0.5, 1);
    expect(Number(getComputedStyle(b).opacity)).toBe(0); // before its start, held by the backwards fill
    tl.progress(0.75); // 300 ms
    expect(Number(getComputedStyle(b).opacity)).toBeCloseTo(0.5, 1);
    expect(tl.time()).toBe(300);
    tl.cancel();
  });

  it("plays forward, then reverses back to the start, and resolves finished", async () => {
    const el = box();
    const tl = createMinimalTimeline({ defaults: { duration: 120, easing: "linear" }, repeat: 1, yoyo: true });
    tl.add(el, [{ opacity: 0 }, { opacity: 1 }]);
    await tl.finished;
    // One play plus one yoyo repeat ends where it started.
    expect(tl.time()).toBe(0);
  });

  it("calls back when the playhead passes a point", async () => {
    const hits: string[] = [];
    const tl = createMinimalTimeline({ defaults: { duration: 80 } });
    tl.add(box(), minimalKeyframes.fadeIn).call(() => hits.push("midway"), 40).call(() => hits.push("end"));
    await tl.finished;
    expect(hits).toEqual(["midway", "end"]);
  });

  it("jumps to the end state when reduced motion applies", async () => {
    const el = box();
    const tl = createMinimalTimeline({ reducedMotion: "always", defaults: { duration: 5000 } });
    tl.add(el, minimalKeyframes.fadeIn);
    const started = performance.now();
    await tl.finished;
    expect(performance.now() - started).toBeLessThan(200);
  });

  it("keeps an exit's end state by committing it, not by holding a fill", async () => {
    const el = box();
    await minimalExit(el, minimalKeyframes.fadeOut, { duration: 60 });
    expect(el.style.opacity).toBe("0");
    expect(el.getAnimations()).toHaveLength(0);
  });

  it("reports properties the compositor cannot run", () => {
    const tl = createMinimalTimeline({ paused: true });
    tl.add(box(), [{ height: "0px", opacity: 0 }, { height: "40px", opacity: 1 }]);
    expect(tl.issues).toEqual(['"height" is not a compositor property; it animates on the main thread']);
    tl.cancel();
  });

  it("writes no style from JavaScript while it plays", async () => {
    const row = [box(), box(), box(), box()];
    let writes = 0;
    const observer = new MutationObserver((records) => (writes += records.length));
    for (const el of row) observer.observe(el, { attributes: true, attributeFilter: ["style"] });
    const tl = createMinimalTimeline({ defaults: { duration: 150 } });
    tl.add(row, minimalKeyframes.slideUpIn, { stagger: 30 }).add(row, minimalKeyframes.popOut, { stagger: 30 }, ">");
    await microtask();
    await frame();
    await tl.finished;
    observer.disconnect();
    expect(writes).toBe(0);
  });
});
