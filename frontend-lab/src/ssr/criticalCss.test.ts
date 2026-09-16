import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { criticalCss, selectorCanMatch, splitBlocks, usedClasses } from "../../../frontend-kit/ts/vite/criticalCss.ts";

/*
 * criticalCss must keep a superset of what markup needs: dropping a rule that
 * can match would paint the first screen wrong until the full stylesheet lands.
 */

describe("criticalCss", () => {
  const html = `<main class="a b"><p class='c'>x</p></main>`;

  it("reads classes from both quote styles", () => {
    expect([...usedClasses(html)].sort()).toEqual(["a", "b", "c"]);
  });

  it("keeps a rule when any selector in its list can match, ignoring attributes and functional pseudo-classes", () => {
    const used = usedClasses(html);
    expect(selectorCanMatch(".a", used)).toBe(true);
    expect(selectorCanMatch(".zz", used)).toBe(false);
    expect(selectorCanMatch(".zz, .a .c", used)).toBe(true);
    expect(selectorCanMatch(".a.zz", used)).toBe(false);
    // :not(.zz) matches markup without zz; :where(.zz, .a) might match. Neither may drop the rule.
    expect(selectorCanMatch(".a:not(.zz)", used)).toBe(true);
    expect(selectorCanMatch(":root:where([data-ui-tier=low_power]) .a", used)).toBe(true);
    expect(selectorCanMatch(".a[data-x=\".zz\"]", used)).toBe(true);
    expect(selectorCanMatch("html, body", used)).toBe(true);
    expect(selectorCanMatch(".sm\\:a", new Set(["sm:a"]))).toBe(true);
  });

  it("splits past strings, comments and braces inside values", () => {
    const blocks = splitBlocks(`/* {x} */.a{content:"}{"}@import url("x;y.css");@media (min-width:1px){.b{c:d}}`);
    expect(blocks.map((b) => b.prelude)).toEqual([".a", '@import url("x;y.css")', "@media (min-width:1px)"]);
    expect(blocks[0]!.body).toBe('content:"}{"');
  });

  it("filters inside conditional at-rules, keeps referenced keyframes and unconditional at-rules", () => {
    const css =
      ".a{animation:spin-a 1s}.zz{animation:spin-zz 1s}@keyframes spin-a{to{rotate:1turn}}@keyframes spin-zz{to{rotate:1turn}}" +
      "@media (prefers-reduced-motion:reduce){.a{animation:none}.zz{color:red}}@media print{.zz{color:red}}" +
      "@font-face{font-family:X;src:url(x.woff2)}:root{--x:1}";
    expect(criticalCss(css, html)).toBe(
      ".a{animation:spin-a 1s}@keyframes spin-a{to{rotate:1turn}}@media (prefers-reduced-motion:reduce){.a{animation:none}}" +
        "@font-face{font-family:X;src:url(x.woff2)}:root{--x:1}",
    );
  });

  it("on the real kit stylesheet, keeps every rule whose classes the markup uses", () => {
    let css: string;
    try {
      css = readFileSync(new URL("../../results/kit-build/ui-minimal.css", import.meta.url), "utf8");
    } catch {
      return; // kit build absent (node src/ssr/buildKit.mjs); the unit cases above still ran
    }
    const classes = [...css.matchAll(/\.([a-z0-9]{6,9})\{/g)].map((m) => m[1]!).slice(0, 5);
    const markup = `<div class="${classes.join(" ")}"></div>`;
    const critical = criticalCss(css, markup);
    for (const name of classes) expect(critical).toContain(`.${name}{`);
    expect(critical.length).toBeLessThan(css.length);
  });
});
