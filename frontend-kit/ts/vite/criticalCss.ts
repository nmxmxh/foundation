/**
 * The rules of a stylesheet that can style a given piece of markup.
 *
 * Used by prerenderShell to inline a first screen's CSS into its HTML, so the
 * browser paints without waiting for a stylesheet round trip. The selection is
 * a superset, never a subset, of what the markup needs: a rule is dropped only
 * when every selector in its list names a class the markup does not contain.
 * Attribute selectors, pseudo-classes and everything inside `:is()`, `:where()`,
 * `:not()` and `:has()` are ignored for that decision, so they can only keep a
 * rule, never lose one. At-rules that are not conditional (`@font-face`,
 * `@property`, `@import`, ...) are always kept; `@keyframes` are kept when a
 * kept rule names them.
 *
 * The full stylesheet must still load at its original place in the cascade
 * (prerenderShell preloads it and promotes the link in place). Its rules are
 * the same rules in the same order as the inlined copy, so they cannot change
 * which declaration wins among themselves; moving the sheet, though, changes
 * its order against every other stylesheet in the document.
 */

interface Block {
  /** Selector list or at-rule prelude, comments removed. */
  prelude: string;
  /** Block contents; null for a statement such as `@import url(x);`. */
  body: string | null;
}

const CONDITIONAL = /^@(media|supports|container|layer|scope|document|-moz-document)\b/i;
const KEYFRAMES = /^@(-webkit-|-moz-)?keyframes\s+/i;

/** Index just past a string or comment starting at `i`, or -1 when there is none. */
const skipAtom = (css: string, i: number): number => {
  const char = css[i];
  if (char === '"' || char === "'") {
    let j = i + 1;
    while (j < css.length && css[j] !== char) j += css[j] === "\\" ? 2 : 1;
    return j + 1;
  }
  if (char === "/" && css[i + 1] === "*") {
    const end = css.indexOf("*/", i + 2);
    return end === -1 ? css.length : end + 2;
  }
  return -1;
};

const stripComments = (text: string) => text.replace(/\/\*[\s\S]*?\*\//g, "");

/** Top-level blocks of a stylesheet, respecting strings, comments and parentheses. */
export function splitBlocks(css: string): Block[] {
  const blocks: Block[] = [];
  let i = 0;
  while (i < css.length) {
    const start = i;
    let parens = 0;
    while (i < css.length) {
      const next = skipAtom(css, i);
      if (next !== -1) {
        i = next;
        continue;
      }
      const char = css[i];
      if (char === "(") parens += 1;
      else if (char === ")") parens -= 1;
      else if (parens === 0 && (char === "{" || char === ";")) break;
      i += 1;
    }
    const prelude = stripComments(css.slice(start, i)).trim();
    if (i >= css.length) break;
    if (css[i] === ";") {
      if (prelude) blocks.push({ prelude, body: null });
      i += 1;
      continue;
    }
    let depth = 1;
    const bodyStart = (i += 1);
    while (i < css.length && depth > 0) {
      const next = skipAtom(css, i);
      if (next !== -1) {
        i = next;
        continue;
      }
      if (css[i] === "{") depth += 1;
      else if (css[i] === "}") depth -= 1;
      i += 1;
    }
    blocks.push({ prelude, body: css.slice(bodyStart, i - 1) });
  }
  return blocks;
}

/** Every class name that appears in the markup's class attributes. */
export function usedClasses(html: string): Set<string> {
  const used = new Set<string>();
  for (const match of html.matchAll(/\sclass=(?:"([^"]*)"|'([^']*)')/g)) {
    for (const name of (match[1] ?? match[2] ?? "").split(/\s+/)) if (name) used.add(name);
  }
  return used;
}

/** Whether some selector in the list could match markup that uses only `used` classes. */
export function selectorCanMatch(selectorList: string, used: Set<string>): boolean {
  let text = selectorList.replace(/"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'/g, "").replace(/\[[^\]]*\]/g, "");
  // Functional pseudo-classes can only widen a match; drop their arguments.
  for (let previous = ""; previous !== text; ) {
    previous = text;
    text = text.replace(/\([^()]*\)/g, "");
  }
  return text
    .split(",")
    .some((selector) =>
      [...selector.matchAll(/\.((?:\\.|[\w-])+)/g)].every((match) => used.has(match[1]!.replace(/\\(.)/g, "$1"))),
    );
}

const select = (blocks: Block[], used: Set<string>, keepKeyframes: (name: string) => boolean): string => {
  let out = "";
  for (const block of blocks) {
    const { prelude, body } = block;
    if (body === null) {
      out += `${prelude};`;
    } else if (KEYFRAMES.test(prelude)) {
      const name = prelude.replace(KEYFRAMES, "").trim().replace(/^["']|["']$/g, "");
      if (keepKeyframes(name)) out += `${prelude}{${body}}`;
    } else if (CONDITIONAL.test(prelude)) {
      const inner = select(splitBlocks(body), used, keepKeyframes);
      if (inner) out += `${prelude}{${inner}}`;
    } else if (prelude.startsWith("@") || body.includes("{") || selectorCanMatch(prelude, used)) {
      // Other at-rules (@font-face, @property, @page, ...) and nested rules are kept whole.
      out += `${prelude}{${body}}`;
    }
  }
  return out;
};

/** The subset of `css` that can style `html`. */
export function criticalCss(css: string, html: string): string {
  const used = usedClasses(html);
  const blocks = splitBlocks(css);
  const referencing = select(blocks, used, () => false);
  const named = (name: string) =>
    new RegExp(`(^|[^\\w-])${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}([^\\w-]|$)`).test(referencing);
  return select(blocks, used, named);
}
