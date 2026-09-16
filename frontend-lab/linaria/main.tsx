import { createElement } from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { cx } from "@linaria/core";
import { MinimalCard, MinimalThemeProvider } from "@ovasabi/ui-minimal";
import { LinariaCard, enterClass } from "./SpikeCard";

/*
 * Measurement page for the Linaria spike. One bundle, both engines:
 * `window.__lab.mount(kind, n)` renders n cards with either the Linaria
 * extracted card or the shipped styled-components MinimalCard, and returns the
 * script time to commit plus the style/layout time the engine then spends.
 */

type Kind = "linaria" | "styled";

const feeds: Record<Kind, (n: number) => ReturnType<typeof createElement>> = {
  linaria: (n) =>
    createElement(
      "main",
      null,
      Array.from({ length: n }, (_, i) => createElement(LinariaCard, { key: i, padding: "md", className: cx(enterClass) }, `Card ${i}`)),
    ),
  styled: (n) =>
    createElement(
      MinimalThemeProvider,
      null,
      createElement("main", null, Array.from({ length: n }, (_, i) => createElement(MinimalCard, { key: i, padding: "md" }, `Card ${i}`))),
    ),
};

const ruleCount = () =>
  Array.from(document.styleSheets).reduce((sum, sheet) => {
    try {
      return sum + sheet.cssRules.length;
    } catch {
      return sum;
    }
  }, 0);

declare global {
  interface Window {
    __lab: { ready: boolean; mount(kind: Kind, n: number): { scriptMs: number; styleMs: number; rulesAdded: number } };
  }
}

window.__lab = {
  ready: true,
  mount(kind, n) {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const root = createRoot(host);
    const rules = ruleCount();
    let t0 = performance.now();
    flushSync(() => root.render(feeds[kind](n)));
    const scriptMs = performance.now() - t0;
    t0 = performance.now();
    void host.offsetHeight;
    const styleMs = performance.now() - t0;
    const rulesAdded = ruleCount() - rules;
    flushSync(() => root.unmount());
    host.remove();
    return { scriptMs, styleMs, rulesAdded };
  },
};
