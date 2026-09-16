/**
 * Cascade equivalence for server-rendered styled-components output.
 *
 * For each rendered case: keep only the rules whose selectors apply to the
 * markup, then record the winning value per (at-rule context, element or
 * global selector state, property). Two renders are equivalent when those
 * winners, the keyframes an applicable animation names, and the markup (minus
 * generated classes and data-minimal-* variant attributes) all match.
 *
 * Ported from the scratch harness that proved option B, including every
 * correction that harness needed on the way — each is a trap a naive diff falls
 * into, and each is recorded where it is handled:
 *
 * - styled-components' SSR registry rows are not styles;
 * - a `:where([data-minimal-*])` guard adds no specificity, so a variant rule
 *   and the inlined block it replaced must land on the same key;
 * - generated class hashes change with rule text, and component ids are a
 *   module-order counter, so both are canonicalised by order of appearance;
 * - a static sheet carries every variant's keyframes, so only used ones count;
 * - `calc(400 + 1)` and `401` are the same z-index;
 * - rules gated on `data-ui-tier` never apply to an untiered render.
 */

import isPropValid from "@emotion/is-prop-valid";
import { resolveMinimalVars, type RenderedCase, type RenderSnapshot } from "./renderSweep";

/*
 * styled-components forwarded any prop to the DOM, so the sweep's generic props
 * (`options`, `columns`, `eyebrow`…) leaked out as junk attributes such as
 * `options="[object Object]"`. Linaria's `styled` forwards only valid HTML
 * props. Neither is the component's contract, so both sides drop attributes
 * that are not valid HTML props before markup is compared.
 */
export const stripInvalidAttributes = (html: string): string =>
  html.replace(/<([a-zA-Z][\w-]*)((?:\s+[^\s=>/]+(?:="[^"]*")?)*)(\s*\/?)>/g, (_, tag: string, attrs: string, close: string) => {
    const kept = [...attrs.matchAll(/\s+([^\s=>/]+)(?:="([^"]*)")?/g)]
      .filter(([, name]) => isPropValid(name) || name === "class" || name.startsWith("data-") || name.startsWith("aria-"))
      .map(([whole]) => whole)
      .join("");
    return `<${tag}${kept}${close}>`;
  });

/** Attributes that predate the variant migration and are part of the markup contract. */
const PRE_EXISTING = new Set(["data-minimal-main-scroll", "data-minimal-theme-scope"]);

type Attrs = Record<string, string>;
type Rule = { ctx: string; selector: string; decls: Array<[string, string]> };

export type CascadedCase = { flat: Record<string, string>; keyframes: string[]; html: string };

const parseHtml = (html: string): Attrs[] => {
  const elements: Attrs[] = [];
  for (const m of html.matchAll(/<([a-zA-Z][\w-]*)((?:\s+[^\s=>/]+(?:="[^"]*")?)*)\s*\/?>/g)) {
    const attrs: Attrs = {};
    for (const at of m[2].matchAll(/([^\s=]+)(?:="([^"]*)")?/g)) attrs[at[1]] = at[2] ?? "";
    elements.push(attrs);
  }
  return elements;
};

const norm = (s: string) => s.replace(/\s+/g, " ").replace(/\s*([,:;(){}>+~])\s*/g, "$1").trim();

/*
 * Parsing is memoised by stylesheet text: an extracted (Linaria) kit shares one
 * stylesheet across every case, and re-parsing 60 KB for each of 2,240 cases
 * would dominate the run.
 */
const parsed = new Map<string, { rules: Rule[]; keyframes: string[] }>();
const parseCss = (input: string): { rules: Rule[]; keyframes: string[] } => {
  const hit = parsed.get(input);
  if (hit) return hit;
  const result = parseCssUncached(input);
  if (parsed.size > 64) parsed.clear();
  parsed.set(input, result);
  return result;
};

const parseCssUncached = (input: string): { rules: Rule[]; keyframes: string[] } => {
  const css = input.replace(/\/\*[\s\S]*?\*\//g, "");
  const rules: Rule[] = [];
  const keyframes: string[] = [];
  const walk = (text: string, ctx: string) => {
    let i = 0;
    while (i < text.length) {
      const open = text.indexOf("{", i);
      if (open < 0) break;
      const prelude = text.slice(i, open).trim();
      let depth = 1;
      let j = open + 1;
      for (; j < text.length && depth; j++) {
        if (text[j] === "{") depth++;
        else if (text[j] === "}") depth--;
      }
      const body = text.slice(open + 1, j - 1);
      if (/^@keyframes/.test(prelude)) keyframes.push(norm(`${prelude}{${body}}`));
      else if (/^@(media|supports|container)/.test(prelude)) walk(body, `${ctx}${norm(prelude)} `);
      else if (!prelude.startsWith("data-styled")) {
        const decls: Array<[string, string]> = [];
        for (const d of body.split(";")) {
          const k = d.indexOf(":");
          if (k > 0) {
            decls.push([
              d.slice(0, k).trim(),
              norm(d.slice(k + 1)).replace(/calc\((-?\d+)\+(-?\d+)\)/g, (_, x, y) => String(Number(x) + Number(y))),
            ]);
          }
        }
        rules.push({ ctx, selector: prelude, decls });
      }
      i = j;
    }
  };
  walk(css, "");
  return { rules, keyframes: keyframes.sort() };
};

const splitTop = (s: string, seps: string[]): string[] => {
  const out: string[] = [];
  let depth = 0;
  let cur = "";
  for (const c of s) {
    if (c === "(" || c === "[") depth++;
    if (c === ")" || c === "]") depth--;
    if (depth === 0 && seps.includes(c)) {
      out.push(cur);
      cur = "";
      continue;
    }
    cur += c;
  }
  out.push(cur);
  return out.map((x) => x.trim()).filter(Boolean);
};

type AttrTest = [string, string | undefined];

const positiveAttrs = (compound: string): AttrTest[] => {
  const stripped = compound.replace(/:(not|is|has)\((?:[^()]|\([^()]*\))*\)/g, "");
  return [...stripped.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)].map((m) => [m[1], m[2]]);
};

export const cascadeOf = (entry: { css: string; html: string }): CascadedCase => {
  const elements = parseHtml(entry.html);
  const classesOf = elements.map((el) => new Set((el.class ?? "").split(/\s+/).filter(Boolean)));

  /*
   * A component's identity class, engine-neutral. styled-components marks it
   * `sc-…`; Linaria has one class per styled component and nothing else, so
   * the element's first class is its identity. Both are numbered in order of
   * appearance, so the same component tree canonicalises to the same names
   * whichever engine rendered it.
   */
  const identityOf = (own: Set<string>) => [...own].find((c) => c.startsWith("sc-")) ?? [...own][0];
  const ordinalOf = new Map<string, string>();
  for (const own of classesOf) {
    const id = identityOf(own);
    if (id && !ordinalOf.has(id)) ordinalOf.set(id, `SC${ordinalOf.size}`);
  }
  const componentOf = new Map<string, string>();
  for (const own of classesOf) {
    const id = identityOf(own);
    const stable = id ? ordinalOf.get(id)! : "plain";
    for (const c of own) if (!componentOf.has(c)) componentOf.set(c, stable);
  }

  /*
   * Linaria turns a prop interpolation into a custom property set inline on the
   * element (`style="--byhw8n4-0: 6px 10px"`) and a rule that reads it
   * (`padding: var(--byhw8n4-0)`). styled-components wrote the literal. Resolve
   * each element's own inline custom properties so both compare as values.
   */
  const inlineVars = elements.map((el) => {
    const vars = new Map<string, string>();
    for (const part of (el.style ?? "").split(";")) {
      const k = part.indexOf(":");
      // Raw, not normalised: `norm` strips the space after `)`, so normalising
      // before resolving `var(--minimal-x, fallback)` fused `#2b303b 52%` into
      // `#2b303b52%`. Resolve first, normalise once (as the sheet path does).
      if (k > 0 && part.trim().startsWith("--")) vars.set(part.slice(0, k).trim(), part.slice(k + 1).trim());
    }
    return vars;
  });
  /*
   * Custom properties inherit: Linaria sets a component's variables on its root
   * element and a descendant rule (`.table th { padding: var(--x-0) }`) reads
   * them there. Generated names are unique per component and slot, so the union
   * of every element's inline variables resolves any rule in the case. An inline
   * value can itself be a token reference; resolve it the same way the sheet is.
   */
  const caseVars = new Map<string, string>();
  for (const vars of inlineVars) for (const [k, v] of vars) if (!caseVars.has(k)) caseVars.set(k, v);
  /*
   * An element's own inline variable wins: repeated instances of one component
   * (every button of a segmented control) share a variable name with different
   * values, so a case-wide union would hand one instance's value to all of
   * them. The union is only the fallback, for descendant rules that read a
   * variable set on the component's root (`i < 0`).
   */
  const resolveInline = (i: number, value: string) =>
    value.replace(/var\((--[\w-]+)\)/g, (match, name: string) => {
      const inline = (i >= 0 ? inlineVars[i]?.get(name) : undefined) ?? caseVars.get(name);
      return inline === undefined ? match : norm(resolveMinimalVars(inline));
    });

  const hasAttr = (i: number, [n, v]: AttrTest) => n in elements[i] && (v === undefined || elements[i][n] === v);
  const satisfies = (i: number, classes: string[], attrs: AttrTest[], negated: AttrTest[][][] = []) =>
    classes.every((c) => classesOf[i].has(c)) &&
    attrs.every((at) => hasAttr(i, at)) &&
    negated.every((alternatives) => !alternatives.some((list) => list.length > 0 && list.every((at) => hasAttr(i, at))));
  const negations = (compound: string): AttrTest[][][] =>
    [...compound.matchAll(/:not\(((?:[^()]|\([^()]*\))*)\)/g)].map((m) =>
      splitTop(m[1], [","]).map((alt) =>
        [...alt.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)].map((x): AttrTest => [x[1], x[2]]),
      ),
    );
  const parts = (compound: string) => ({
    classes: [...compound.replace(/:(not|is|has)\((?:[^()]|\([^()]*\))*\)/g, "").matchAll(/\.([\w-]+)/g)].map((m) => m[1]),
    attrs: positiveAttrs(compound),
    negated: negations(compound),
  });
  const stripGuards = (s: string) => {
    let out = "";
    for (let i = 0; i < s.length; ) {
      if (s.startsWith(":where(", i)) {
        let depth = 0;
        let j = i + 6;
        for (; j < s.length; j++) {
          if (s[j] === "(") depth++;
          else if (s[j] === ")" && --depth === 0) break;
        }
        if (s.slice(i, j + 1).includes("data-minimal-")) {
          i = j + 1;
          continue;
        }
      }
      out += s[i++];
    }
    return out;
  };
  const canonical = (s: string) => stripGuards(norm(s)).replace(/\.([\w-]+)/g, (_, c) => `.${componentOf.get(c) ?? "?"}`);

  const { rules, keyframes } = parseCss(entry.css);
  const state = new Map<string, Map<string, string>>();
  const assign = (key: string, decls: Array<[string, string]>) => {
    const props = state.get(key) ?? new Map<string, string>();
    for (const [p, v] of decls) {
      const prev = props.get(p);
      if (prev && /!important$/.test(prev) && !/!important$/.test(v)) continue;
      props.set(p, v);
    }
    state.set(key, props);
  };

  for (const rule of rules) {
    for (const sel of splitTop(rule.selector, [","])) {
      if (sel.includes("data-ui-tier")) continue;
      // A selector with no class is the kit's global sheet (reset, :root tokens):
      // it is not part of any component's render, and the styled-components
      // reference never rendered it per case.
      if (!sel.includes(".")) continue;
      const compounds = splitTop(sel.replace(/\s*([>+~])\s*/g, " $1 "), [" "]);
      const subject = compounds[compounds.length - 1];
      const ancestors = compounds.slice(0, -1);
      const ancestorsOk = ancestors.every((compound) => {
        if (/^[>+~]$/.test(compound)) return true;
        const { classes, attrs } = parts(compound);
        return classes.length === 0 || elements.some((_, i) => satisfies(i, classes, attrs));
      });
      if (!ancestorsOk) continue;
      const { classes, attrs, negated } = parts(subject);
      const subjectState = canonical(subject).replace(/\.[\w-]+|\[[\w-]+(?:="[^"]*")?\]/g, "");
      const prefix = rule.ctx + ancestors.map(canonical).join(" ");
      if (classes.length === 0 && attrs.length === 0) {
        assign(
          `G|${prefix} ${canonical(subject)}`,
          rule.decls.map(([p, v]) => [p, resolveInline(-1, v)] as [string, string]),
        );
        continue;
      }
      elements.forEach((_, i) => {
        if (satisfies(i, classes, attrs, negated)) {
          assign(
            `E${i}|${prefix} &${subjectState}`,
            rule.decls.map(([p, v]) => [p, resolveInline(i, v)] as [string, string]),
          );
        }
      });
    }
  }

  const flat: Record<string, string> = {};
  for (const [key, props] of state) for (const [p, v] of props) flat[`${key} :: ${p}`] = v;
  const animated = Object.entries(flat)
    .filter(([key]) => /:: animation(-name)?$/.test(key))
    .map(([, v]) => v)
    .join(" ");
  const used = keyframes
    .map((kf) => ({ kf, name: kf.match(/^@keyframes\s*([\w-]+)/)?.[1] }))
    .filter(({ name }) => Boolean(name) && animated.includes(name!));
  /*
   * Keyframe names are generated (a hash in styled-components, a suffixed name
   * in Linaria), so names are replaced by the keyframes' bodies: an animation
   * is the same animation when it runs the same frames.
   */
  const bodyOf = (kf: string) => kf.replace(/^@keyframes\s*[\w-]+/, "@keyframes");
  for (const { kf, name } of used) {
    const body = bodyOf(kf);
    for (const key of Object.keys(flat)) {
      if (/:: animation(-name)?$/.test(key)) flat[key] = flat[key].split(name!).join(`KF[${body}]`);
    }
  }
  const html = stripInvalidAttributes(entry.html)
    .replace(/ class="[^"]*"/g, "")
    .replace(/ (data-minimal-[\w-]+)="[^"]*"/g, (match, attr: string) => (PRE_EXISTING.has(attr) ? match : ""))
    // Linaria's per-element custom properties are resolved above; the rest of the style attribute is the contract.
    .replace(/ style="([^"]*)"/g, (_, style: string) => {
      const kept = style
        .split(";")
        .map((part) => part.trim())
        .filter((part) => part && !/^--[a-z0-9]+-\d+\s*:/i.test(part))
        .join(";");
      return kept ? ` style="${kept}"` : "";
    });
  return { flat, keyframes: used.map(({ kf }) => bodyOf(kf)).sort(), html };
};

export type CascadedCaseOrError = CascadedCase | { error: string };
export type CascadedSnapshot = Record<string, Record<string, CascadedCaseOrError>>;

export const cascadeSnapshot = (snapshot: RenderSnapshot): CascadedSnapshot => {
  const out: CascadedSnapshot = {};
  for (const [name, cases] of Object.entries(snapshot)) {
    out[name] = {};
    for (const [caseName, rendered] of Object.entries(cases) as Array<[string, RenderedCase]>) {
      out[name][caseName] = "error" in rendered ? { error: rendered.error } : cascadeOf(rendered);
    }
  }
  return out;
};

export type Comparison = { identical: number; sameError: number; diffs: string[]; added: string[] };

/** Compare a baseline against a fresh render. Components new since the baseline are reported, not failed. */
export const compareCascaded = (baseline: CascadedSnapshot, current: CascadedSnapshot): Comparison => {
  let identical = 0;
  let sameError = 0;
  const diffs: string[] = [];
  for (const name of Object.keys(baseline)) {
    for (const caseName of Object.keys(baseline[name])) {
      const x = baseline[name][caseName];
      const y = current[name]?.[caseName];
      if (!y) {
        diffs.push(`${name} ${caseName}: missing now`);
        continue;
      }
      if ("error" in x || "error" in y) {
        if ("error" in x && "error" in y && x.error === y.error) sameError++;
        else diffs.push(`${name} ${caseName}: error baseline=${"error" in x ? x.error : "-"} now=${"error" in y ? y.error : "-"}`);
        continue;
      }
      const problems: string[] = [];
      const keys = new Set([...Object.keys(x.flat), ...Object.keys(y.flat)]);
      const bad = [...keys].filter((k) => x.flat[k] !== y.flat[k]);
      if (bad.length) problems.push(bad.slice(0, 3).map((k) => `${k}: ${x.flat[k]} -> ${y.flat[k]}`).join(" | "));
      if (JSON.stringify(x.keyframes) !== JSON.stringify(y.keyframes)) problems.push("keyframes differ");
      if (x.html !== y.html) problems.push("html differs");
      if (problems.length) diffs.push(`${name} ${caseName}: ${problems.join("; ")}`);
      else identical++;
    }
  }
  const added = Object.keys(current).filter((name) => !(name in baseline));
  return { identical, sameError, diffs, added };
};
