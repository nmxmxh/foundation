/**
 * Render every exported `Minimal*` component across a sweep of prop values and
 * record the CSS and markup each case produces.
 *
 * Ported unchanged in behaviour from the scratch harness that proved option B
 * (2,240/2,240 cases equivalent before and after, 2026-09-11/14). The sweep is
 * deliberately wide and a little absurd — a Card given `tone`, a Badge given
 * `anchor` — because components ignore props they do not own, and a case that
 * a component *does* start reading is exactly the change this eval exists to
 * notice.
 */

import { createElement, type ComponentType } from "react";
import { renderToString } from "react-dom/server";
import { ServerStyleSheet } from "styled-components";

export type RenderedCase = { css: string; html: string } | { error: string };
export type RenderSnapshot = Record<string, Record<string, RenderedCase>>;

/**
 * `var(--minimal-token, fallback)` → `fallback`, so a rule that reads a token
 * compares equal to the literal value it replaced. A `var()` without a fallback
 * was hand-written and is left alone.
 */
export const resolveMinimalVars = (input: string): string => {
  let s = input;
  let from = 0;
  for (;;) {
    const i = s.indexOf("var(--minimal-", from);
    if (i < 0) return s;
    let depth = 0;
    let comma = -1;
    let end = -1;
    for (let j = i + 3; j < s.length; j++) {
      const c = s[j];
      if (c === "(") depth++;
      else if (c === ")") {
        depth--;
        if (depth === 0) {
          end = j;
          break;
        }
      } else if (c === "," && depth === 1 && comma < 0) comma = j;
    }
    if (end < 0 || comma < 0) {
      from = i + 4;
      continue;
    }
    s = s.slice(0, i) + s.slice(comma + 1, end).trim() + s.slice(end + 1);
  }
};

const day = new Date(2026, 0, 15);

const base: Record<string, unknown> = {
  children: "Content",
  label: "Label",
  title: "Title",
  description: "Description",
  hint: "Hint",
  eyebrow: "Eyebrow",
  value: "a",
  placeholder: "Pick",
  options: [
    { value: "a", label: "A" },
    { value: "b", label: "B" },
  ],
  tabs: [
    { value: "a", label: "A", content: "Panel A" },
    { value: "b", label: "B", content: "Panel B" },
  ],
  items: [{ id: "1", label: "One", value: "1" }],
  columns: [{ key: "a", header: "A", label: "A" }],
  rows: [{ id: "1", a: "x" }],
  data: [],
  stats: [],
  onChange() {},
  onClick() {},
  onClose() {},
  onSelect() {},
  onOpenChange() {},
  open: true,
  isOpen: true,
  trigger: "Trigger",
  content: "Tip",
  name: "field",
  checked: true,
  date: day,
  month: day,
  selected: day,
};

/** Components whose contract the generic props break. */
const overrides: Record<string, Record<string, unknown>> = {
  MinimalInput: { children: undefined },
  MinimalTable: {
    columns: [{ key: "a", header: "A", label: "A", cell: (row: { a: string }) => row.a }],
    getRowKey: (row: { id: string }) => row.id,
  },
};

const sweeps: Record<string, readonly unknown[]> = {
  variant: ["primary", "secondary", "ghost", "quiet", "default", "muted", "raised", "outlined", "neutral", "brand"],
  tone: ["neutral", "brand", "info", "success", "warning", "danger"],
  size: ["sm", "md", "lg"],
  emphasis: ["soft", "solid", "outline"],
  density: ["compact", "comfortable", "relaxed"],
  align: ["start", "center", "end", "between"],
  anchor: ["center", "stacked", "left-visual", "right-visual", "top-left", "bottom-left", "bottom-right"],
  visualMode: ["background", "canvas", "media", "none"],
  intensity: ["calm", "balanced", "statement"],
  layout: ["stack", "row", "split"],
  state: ["default", "invalid", "locked"],
  disabled: [true],
  loading: [true],
  fullWidth: [true],
  error: ["Error text"],
  invalid: [true],
  hoverable: [true],
  interactive: [true],
  mobile: [true],
  mobileSheet: [true],
  selected: [true],
  open: [false],
  compact: [true],
  locked: [true],
  readOnly: [true],
};

export const sweepCases = (): Array<[string, Record<string, unknown>]> => {
  const cases: Array<[string, Record<string, unknown>]> = [["base", {}]];
  for (const [key, values] of Object.entries(sweeps)) {
    for (const value of values) cases.push([`${key}=${String(value)}`, { [key]: value }]);
  }
  return cases;
};

const SKIP = new Set(["MinimalThemeProvider", "MinimalGlobalStyles", "MinimalThemeScope"]);

/**
 * The same sweep for a kit whose styles were extracted at build time (Linaria).
 *
 * There is no runtime stylesheet to collect per case: every case is rendered
 * from the built module and paired with the one stylesheet the build emitted,
 * which is exactly what a user downloads. `cascadeOf` keeps only the rules that
 * apply to each case's markup, so the shared sheet is not a shortcut — it is
 * the stricter test, since a rule that leaks onto the wrong element shows up.
 */
export const renderSweepExtracted = (ui: Record<string, unknown>, css: string): RenderSnapshot => {
  const Provider = ui.MinimalThemeProvider as ComponentType<{ children?: unknown }>;
  const sheet = resolveMinimalVars(css);
  const cases = sweepCases();
  const result: RenderSnapshot = {};
  const silenced = console.error;
  console.error = () => {};
  try {
    for (const [name, Component] of Object.entries(ui)) {
      if (!/^Minimal[A-Z]/.test(name) || SKIP.has(name)) continue;
      if (typeof Component !== "function" && typeof Component !== "object") continue;
      result[name] = {};
      for (const [caseName, extra] of cases) {
        try {
          const html = renderToString(
            createElement(
              Provider,
              null,
              createElement(Component as ComponentType<Record<string, unknown>>, {
                ...base,
                ...(overrides[name] ?? {}),
                ...extra,
              }),
            ),
          );
          result[name][caseName] = { css: sheet, html };
        } catch (thrown) {
          result[name][caseName] = { error: String((thrown as { message?: string })?.message ?? thrown).split("\n")[0] };
        }
      }
    }
  } finally {
    console.error = silenced;
  }
  return result;
};

export const renderSweep = (ui: Record<string, unknown>): RenderSnapshot => {
  const Provider = ui.MinimalThemeProvider as ComponentType<{ children?: unknown }>;
  const cases = sweepCases();
  const result: RenderSnapshot = {};
  const silenced = console.error;
  console.error = () => {};
  try {
    for (const [name, Component] of Object.entries(ui)) {
      if (!/^Minimal[A-Z]/.test(name) || SKIP.has(name)) continue;
      if (typeof Component !== "function" && typeof Component !== "object") continue;
      result[name] = {};
      for (const [caseName, extra] of cases) {
        const sheet = new ServerStyleSheet();
        let html = "";
        let error: string | null = null;
        try {
          html = renderToString(
            sheet.collectStyles(
              createElement(
                Provider,
                null,
                createElement(Component as ComponentType<Record<string, unknown>>, {
                  ...base,
                  ...(overrides[name] ?? {}),
                  ...extra,
                }),
              ),
            ),
          );
        } catch (thrown) {
          error = String((thrown as { message?: string })?.message ?? thrown).split("\n")[0];
        }
        const css = sheet.instance.toString();
        sheet.seal();
        result[name][caseName] = error ? { error } : { css: resolveMinimalVars(css), html };
      }
    }
  } finally {
    console.error = silenced;
  }
  return result;
};
