import { mkdtempSync, readFileSync, existsSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { PRERENDER_CSS_LOADER, inlineStylesheets } from "../../../frontend-kit/ts/vite/prerenderShell.ts";

/*
 * The full stylesheet must keep its place in the cascade. Moving it after a
 * CSS-in-JS library's <style> tags lets its rules win ties they used to lose,
 * so subset mode preloads it where the link was and promotes it in place.
 */

const page = (head: string) =>
  `<!doctype html><html><head>${head}</head><body><div id="root"><p class="a">x</p></div></body></html>`;
const LINK = '<link rel="stylesheet" crossorigin href="/assets/app.css">';
const ENTRY = '<script type="module" crossorigin src="/assets/app.js"></script>';

const fixture = () => {
  const outDir = mkdtempSync(join(tmpdir(), "prerender-css-"));
  mkdirSync(join(outDir, "assets"));
  writeFileSync(join(outDir, "assets/app.css"), ".a{color:red}.zz{color:blue}");
  return outDir;
};

describe("prerenderShell stylesheet inlining", () => {
  it("subset: inlines matching rules, preloads the full sheet at the link's place, promotes it before the app entry", async () => {
    const outDir = fixture();
    const html = page(`${ENTRY}${LINK}<style data-styled="true">.app{}</style>`);
    const result = await inlineStylesheets(html, '<p class="a">x</p>', outDir, "/", "subset");

    expect(result.html).toContain('<style data-prerender-css="subset">.a{color:red}</style>');
    expect(result.html).not.toContain(".zz{color:blue}");
    const preload = result.html.indexOf('rel="preload" as="style" data-prerender-css');
    expect(preload).toBeGreaterThan(-1);
    // Same place in the cascade: before the CSS-in-JS styles, as the link was.
    expect(preload).toBeLessThan(result.html.indexOf("<style data-styled"));
    expect(result.html).toContain(`<noscript>${LINK}</noscript>`);
    // Nothing moved to the end of <body>.
    expect(result.html.slice(result.html.indexOf("<body>"))).not.toContain("<link");
    // The promoter runs before the app's entry module (deferred modules run in document order).
    expect(result.html.indexOf(`src="/${PRERENDER_CSS_LOADER}"`)).toBeLessThan(result.html.indexOf('src="/assets/app.js"'));
    expect(readFileSync(join(outDir, PRERENDER_CSS_LOADER), "utf8")).toContain('link.rel = "stylesheet"');
    expect([result.inlined, result.full]).toEqual([".a{color:red}".length, ".a{color:red}.zz{color:blue}".length]);
  });

  it("all: inlines the whole sheet in the link's place and drops the link, with no loader", async () => {
    const outDir = fixture();
    const result = await inlineStylesheets(page(`${ENTRY}${LINK}`), '<p class="a">x</p>', outDir, "/", "all");
    expect(result.html).toContain('<style data-prerender-css="all">.a{color:red}.zz{color:blue}</style>');
    expect(result.html).not.toContain("<link");
    expect(result.html).not.toContain(PRERENDER_CSS_LOADER);
    expect(existsSync(join(outDir, PRERENDER_CSS_LOADER))).toBe(false);
  });

  it("leaves external stylesheets alone and escapes a closing style tag inside CSS", async () => {
    const outDir = fixture();
    writeFileSync(join(outDir, "assets/app.css"), '.a{content:"</style>"}');
    const external = '<link rel="stylesheet" href="https://fonts.example/x.css">';
    const result = await inlineStylesheets(page(`${external}${LINK}`), '<p class="a">x</p>', outDir, "/", "all");
    expect(result.html).toContain(external);
    expect(result.html).toContain('content:"<\\/style>"');
  });
});
