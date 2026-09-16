import { commands } from "vitest/browser";
import { describe, expect, it } from "vitest";
import { createElement, type CSSProperties } from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import styled from "styled-components";
import { AnimatePresence, motion } from "framer-motion";
import { MinimalCard, MinimalThemeProvider } from "@ovasabi/ui-minimal";

/*
 * What the two runtime dependencies cost in a real engine, against what the
 * platform already does.
 *
 * Runs in the serial `gpu` project so measurements do not compete with other
 * test files. Every comparison is interleaved and reports the fastest of
 * several repetitions (noise only adds time) plus the cold first run, which is
 * the one a user pays on first navigation. Results go to results/runtime/.
 *
 * This is evidence for the styling ADR (research doc section 8) and P3; it is
 * not a gate.
 */

const N = 1000;
const REPEATS = 7;
const TONES = ["#2563eb", "#16a34a", "#dc2626", "#9333ea", "#ea580c", "#0891b2", "#4b5563", "#ca8a04"];

type Row = { tone: string; size: number; pad: number };
const rows = (shift: number): Row[] =>
  Array.from({ length: N }, (_, i) => ({
    tone: TONES[(i + shift) % TONES.length]!,
    size: 12 + ((i + shift) % 3) * 2,
    pad: 4 + ((i + shift) % 3) * 2,
  }));

/* A: interpolated props — every new combination is a new class and a new rule. */
const Interpolated = styled.button<{ $tone: string; $size: number; $pad: number }>`
  display: inline-flex;
  border: 0;
  border-radius: 8px;
  color: white;
  background: ${(p) => p.$tone};
  font-size: ${(p) => p.$size}px;
  padding: ${(p) => p.$pad}px ${(p) => p.$pad * 2}px;
`;

/* B: static template reading variables (ui-minimal's option B shape). One rule, ever. */
const Static = styled.button`
  display: inline-flex;
  border: 0;
  border-radius: 8px;
  color: white;
  background: var(--tone);
  font-size: var(--size);
  padding: var(--pad) calc(var(--pad) * 2);
`;

/* C: no CSS-in-JS: a class from a stylesheet injected once, variables on style. */
const PLAIN_CSS = `.lab-plain{display:inline-flex;border:0;border-radius:8px;color:white;background:var(--tone);font-size:var(--size);padding:var(--pad) calc(var(--pad) * 2)}`;

const vars = (r: Row) => ({ "--tone": r.tone, "--size": `${r.size}px`, "--pad": `${r.pad}px` }) as CSSProperties;

const renderers = {
  interpolated: (data: Row[]) =>
    data.map((r, i) => createElement(Interpolated, { key: i, $tone: r.tone, $size: r.size, $pad: r.pad }, "Add")),
  "static-vars": (data: Row[]) => data.map((r, i) => createElement(Static, { key: i, style: vars(r) }, "Add")),
  "plain-css": (data: Row[]) => data.map((r, i) => createElement("button", { key: i, className: "lab-plain", style: vars(r) }, "Add")),
};
type Variant = keyof typeof renderers;

const ruleCount = () => {
  let total = 0;
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      total += sheet.cssRules.length;
    } catch {
      // cross-origin sheet: not ours
    }
  }
  return total;
};

const measureOnce = (variant: Variant) => {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  const rulesBefore = ruleCount();

  let t0 = performance.now();
  flushSync(() => root.render(renderers[variant](rows(0))));
  const mountScript = performance.now() - t0;
  t0 = performance.now();
  void host.offsetHeight; // style + layout for what was just committed
  const mountStyle = performance.now() - t0;

  // Every button changes tone, size and padding.
  t0 = performance.now();
  flushSync(() => root.render(renderers[variant](rows(1))));
  const updateScript = performance.now() - t0;
  t0 = performance.now();
  void host.offsetHeight;
  const updateStyle = performance.now() - t0;

  const rulesAdded = ruleCount() - rulesBefore;
  flushSync(() => root.unmount());
  host.remove();
  return { mountScript, mountStyle, updateScript, updateStyle, rulesAdded };
};

const round = (value: number) => Math.round(value * 10) / 10;

describe("runtime cost of the styling and motion dependencies (real engine)", () => {
  it("styled-components interpolation vs static variables vs a plain class", async () => {
    const style = document.createElement("style");
    style.textContent = PLAIN_CSS;
    document.head.appendChild(style);

    const variants = Object.keys(renderers) as Variant[];
    const cold: Record<string, ReturnType<typeof measureOnce>> = {};
    const best: Record<string, Record<string, number>> = {};
    for (let repeat = 0; repeat < REPEATS + 1; repeat += 1) {
      // Rotate the order so no variant always runs first.
      const order = variants.map((_, i) => variants[(i + repeat) % variants.length]!);
      for (const variant of order) {
        const sample = measureOnce(variant);
        if (repeat === 0) {
          cold[variant] = sample;
          continue;
        }
        const slot = (best[variant] ??= {});
        for (const [key, value] of Object.entries(sample)) {
          slot[key] = key === "rulesAdded" ? value : Math.min(slot[key] ?? Number.POSITIVE_INFINITY, value);
        }
      }
    }

    const report = {
      measuredAt: new Date().toISOString(),
      userAgent: navigator.userAgent,
      workload: `${N} buttons, ${TONES.length * 3 * 3} distinct tone/size/padding combinations, mount then change every button`,
      note: "ms; cold = first run in a fresh page (the one a user pays), warm = fastest of the interleaved repeats",
      cold: Object.fromEntries(Object.entries(cold).map(([k, v]) => [k, Object.fromEntries(Object.entries(v).map(([m, n]) => [m, round(n)]))])),
      warm: Object.fromEntries(Object.entries(best).map(([k, v]) => [k, Object.fromEntries(Object.entries(v).map(([m, n]) => [m, round(n)]))])),
    };
    await commands.writeFile("results/runtime/styling.json", `${JSON.stringify(report, null, 2)}\n`);
    style.remove();

    // Facts the numbers rest on, asserted so a broken harness cannot report quietly.
    expect(cold.interpolated!.rulesAdded, "interpolation injected no rules: harness broken").toBeGreaterThan(0);
    expect(cold["plain-css"]!.rulesAdded).toBe(0);
  });

  /*
   * Which lane each motion shape actually runs on.
   *
   * `document.getAnimations()` alone cannot answer it: a framer-motion element
   * animating opacity *and* y may run opacity as WAAPI and write transform from
   * JavaScript every frame. So each shape also counts writes to the element's
   * `style` attribute while it animates — a JS-driven animation writes once per
   * frame, a compositor animation writes nothing.
   *
   * Not measured here: whether an animation keeps time through a main-thread
   * stall. A timeline's currentTime read during a synchronous block does not
   * advance on the main thread whatever the compositor is doing, so that probe
   * reported false for every shape and was removed; it needs a trace.
   */
  it("motion shapes: which run on the compositor and which write style from JS", async () => {
    const css = document.createElement("style");
    css.textContent = `
.lab-box { width: 240px; background: #16a34a; border-radius: 12px; overflow: hidden; }
.lab-css-slide { transition: opacity 240ms ease, translate 240ms ease; }
.lab-css-slide:not([data-open]) { opacity: 0; translate: 0 24px; }
.lab-css-height { interpolate-size: allow-keywords; height: 0; transition: height 240ms ease; }
.lab-css-height[data-open] { height: auto; }`;
    document.head.appendChild(css);
    const content = () => createElement("div", { style: { height: 160 } }, "panel");

    type Shape = { writes: number; animations: string[] };
    const shapes: Record<string, Shape> = {};

    const observe = async (name: string, target: () => HTMLElement | null, start: () => void) => {
      let writes = 0;
      const observer = new MutationObserver((records) => (writes += records.length));
      start();
      await new Promise((resolve) => requestAnimationFrame(resolve));
      const el = target();
      if (!el) throw new Error(`${name}: no element`);
      observer.observe(el, { attributes: true, attributeFilter: ["style"] });
      const animations = el.getAnimations().map((a) => {
        const keyframes = (a.effect as KeyframeEffect | null)?.getKeyframes() ?? [];
        return [...new Set(keyframes.flatMap((k) => Object.keys(k).filter((p) => !["offset", "easing", "composite", "computedOffset"].includes(p))))].join("+");
      });
      await new Promise((resolve) => setTimeout(resolve, 500));
      observer.disconnect();
      shapes[name] = {
        writes,
        animations,
      };
    };

    const host = document.createElement("div");
    document.body.appendChild(host);
    const root = createRoot(host);
    const framerCase = async (name: string, props: Record<string, unknown>) => {
      flushSync(() => root.render(null));
      await observe(
        name,
        () => host.querySelector<HTMLElement>("[data-lab-framer]"),
        () => flushSync(() => root.render(createElement(motion.div, { "data-lab-framer": "", className: "lab-box", ...props }, content()))),
      );
    };
    await framerCase("framer opacity+scale", { initial: { opacity: 0, scale: 0.97 }, animate: { opacity: 1, scale: 1 }, transition: { duration: 0.4 } });
    await framerCase("framer opacity+y (ChooseChow modal shape)", { initial: { opacity: 0, y: 24 }, animate: { opacity: 1, y: 0 }, transition: { duration: 0.4 } });
    await framerCase("framer height:auto (MinimalExplainer shape)", { initial: { height: 0, opacity: 0 }, animate: { height: "auto", opacity: 1 }, transition: { duration: 0.4 } });
    await framerCase("framer spring scale (layout-ish)", { initial: { scale: 0.9 }, animate: { scale: 1 }, transition: { type: "spring", stiffness: 120, damping: 8 } });
    flushSync(() => root.unmount());
    host.remove();

    const cssCase = async (name: string, className: string) => {
      const el = document.createElement("div");
      el.className = `lab-box ${className}`;
      el.appendChild(Object.assign(document.createElement("div"), { style: "height:160px", textContent: "panel" }));
      document.body.appendChild(el);
      void el.offsetHeight;
      await observe(name, () => el, () => el.setAttribute("data-open", ""));
      el.remove();
    };
    await cssCase("css transition opacity+translate", "lab-css-slide");
    await cssCase("css interpolate-size height:auto", "lab-css-height");

    css.remove();
    await commands.writeFile(
      "results/runtime/motion-lanes.json",
      `${JSON.stringify({ measuredAt: new Date().toISOString(), userAgent: navigator.userAgent, note: "writes = style-attribute mutations while animating (JS frames); animations = properties carried by WAAPI/CSS animations", shapes }, null, 2)}\n`,
    );
    expect(Object.keys(shapes).length).toBe(6);
  });

  /*
   * MinimalCard is a framer-motion element that fades in on mount, so a feed of
   * cards pays framer-motion's per-instance setup once per card. Measured as
   * the same styled template with and without motion, mounting a feed.
   */
  it("a feed of motion cards vs plain cards: mount cost and style writes", async () => {
    const CARDS = 300;
    const template = `
      display: block;
      padding: 16px;
      border-radius: 12px;
      background: #fff;
      box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
    `;
    const MotionCard = styled(motion.section)`${template}`;
    const PlainCard = styled.section`${template}`;
    const fade = { initial: { opacity: 0 }, animate: { opacity: 1, transition: { duration: 0.2 } } };
    const feeds = {
      "motion card (MinimalCard shape)": () =>
        Array.from({ length: CARDS }, (_, i) => createElement(MotionCard, { key: i, ...fade }, `Card ${i}`)),
      // The shipped primitive, after framer-motion was removed (2026-09-15).
      "MinimalCard (shipped, CSS entrance)": () =>
        createElement(
          MinimalThemeProvider,
          null,
          Array.from({ length: CARDS }, (_, i) => createElement(MinimalCard, { key: i }, `Card ${i}`)),
        ),
      "plain card + CSS @starting-style": () =>
        Array.from({ length: CARDS }, (_, i) => createElement(PlainCard, { key: i, className: "lab-fade" }, `Card ${i}`)),
    };
    const css = document.createElement("style");
    css.textContent = `.lab-fade { transition: opacity 200ms ease; } @starting-style { .lab-fade { opacity: 0; } }`;
    document.head.appendChild(css);

    const results: Record<string, { coldMountMs: number; warmMountMs: number; styleWrites: number }> = {};
    const names = Object.keys(feeds) as (keyof typeof feeds)[];
    const samples: Record<string, number[]> = {};
    const writes: Record<string, number> = {};
    for (let repeat = 0; repeat < REPEATS + 1; repeat += 1) {
      for (const name of repeat % 2 ? [...names].reverse() : names) {
        const host = document.createElement("div");
        document.body.appendChild(host);
        const root = createRoot(host);
        let count = 0;
        const observer = new MutationObserver((records) => (count += records.length));
        observer.observe(host, { attributes: true, attributeFilter: ["style"], subtree: true });
        const t0 = performance.now();
        flushSync(() => root.render(feeds[name]()));
        (samples[name] ??= []).push(performance.now() - t0);
        await new Promise((resolve) => setTimeout(resolve, 350));
        observer.disconnect();
        writes[name] = Math.max(writes[name] ?? 0, count);
        flushSync(() => root.unmount());
        host.remove();
      }
    }
    for (const name of names) {
      const [cold, ...warm] = samples[name]!;
      results[name] = { coldMountMs: round(cold!), warmMountMs: round(Math.min(...warm)), styleWrites: writes[name]! };
    }
    css.remove();
    await commands.writeFile(
      "results/runtime/cards.json",
      `${JSON.stringify({ measuredAt: new Date().toISOString(), userAgent: navigator.userAgent, cards: CARDS, note: "mount = render+commit script ms; styleWrites = style-attribute mutations across the feed during the fade", results }, null, 2)}\n`,
    );
    expect(results["plain card + CSS @starting-style"]!.styleWrites).toBe(0);
  });

  it("enter/exit motion: framer-motion vs CSS @starting-style vs WAAPI", async () => {
    const css = document.createElement("style");
    css.textContent = `
.lab-css-panel { opacity: 1; transform: none; transition: opacity 200ms ease, transform 200ms ease, display 200ms allow-discrete, overlay 200ms allow-discrete; }
.lab-css-panel:not([data-open]) { display: none; opacity: 0; transform: scale(0.97); }
@starting-style { .lab-css-panel[data-open] { opacity: 0; transform: scale(0.97); } }
.lab-panel { width: 240px; height: 160px; background: #2563eb; border-radius: 12px; }`;
    document.head.appendChild(css);

    const CYCLES = 12;
    const results: Record<string, { openScriptMs: number; closeScriptMs: number; runningAnimations: number; compositorEligible: boolean }> = {};

    const nextFrame = () => new Promise<number>((resolve) => requestAnimationFrame(resolve));
    const settle = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

    const run = async (name: string, open: () => void, close: () => void) => {
      let openBest = Number.POSITIVE_INFINITY;
      let closeBest = Number.POSITIVE_INFINITY;
      let running = 0;
      let eligible = false;
      for (let cycle = 0; cycle < CYCLES; cycle += 1) {
        await nextFrame();
        let t0 = performance.now();
        open();
        await nextFrame(); // the frame the animation starts in
        openBest = Math.min(openBest, performance.now() - t0);
        await settle(30);
        const animations = document.getAnimations();
        running = Math.max(running, animations.length);
        // A CSS or WAAPI animation on opacity/transform is one the compositor can run
        // through a main-thread stall; a per-frame JS style write is not.
        eligible ||= animations.some((a) => {
          const keyframes = (a.effect as KeyframeEffect | null)?.getKeyframes() ?? [];
          return keyframes.some((k) => "opacity" in k || "transform" in k);
        });
        await settle(220);
        t0 = performance.now();
        close();
        await nextFrame();
        closeBest = Math.min(closeBest, performance.now() - t0);
        await settle(260);
      }
      results[name] = { openScriptMs: round(openBest), closeScriptMs: round(closeBest), runningAnimations: running, compositorEligible: eligible };
    };

    // framer-motion
    {
      const host = document.createElement("div");
      document.body.appendChild(host);
      const root = createRoot(host);
      const view = (open: boolean) =>
        createElement(
          AnimatePresence,
          null,
          open
            ? createElement(motion.div, {
                key: "panel",
                className: "lab-panel",
                initial: { opacity: 0, scale: 0.97 },
                animate: { opacity: 1, scale: 1, transition: { duration: 0.2 } },
                exit: { opacity: 0, scale: 0.97, transition: { duration: 0.2 } },
              })
            : null,
        );
      flushSync(() => root.render(view(false)));
      await run("framer-motion", () => flushSync(() => root.render(view(true))), () => flushSync(() => root.render(view(false))));
      flushSync(() => root.unmount());
      host.remove();
    }

    // CSS: @starting-style + allow-discrete display, toggled by an attribute
    {
      const panel = document.createElement("div");
      panel.className = "lab-panel lab-css-panel";
      document.body.appendChild(panel);
      await run("css-starting-style", () => panel.setAttribute("data-open", ""), () => panel.removeAttribute("data-open"));
      panel.remove();
    }

    // WAAPI on a plain element
    {
      const panel = document.createElement("div");
      panel.className = "lab-panel";
      panel.style.display = "none";
      document.body.appendChild(panel);
      const frames = [
        { opacity: 0, transform: "scale(0.97)" },
        { opacity: 1, transform: "none" },
      ];
      await run(
        "waapi",
        () => {
          panel.style.display = "";
          panel.animate(frames, { duration: 200, easing: "ease" });
        },
        () => {
          const exit = panel.animate([...frames].reverse(), { duration: 200, easing: "ease" });
          exit.onfinish = () => (panel.style.display = "none");
        },
      );
      panel.remove();
    }

    css.remove();
    await commands.writeFile(
      "results/runtime/motion.json",
      `${JSON.stringify({ measuredAt: new Date().toISOString(), userAgent: navigator.userAgent, cycles: CYCLES, note: "script ms = fastest open/close from call to the next animation frame", results }, null, 2)}\n`,
    );
    expect(Object.keys(results)).toHaveLength(3);
  });
});
