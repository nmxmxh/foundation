import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { cascadeOf, compareCascaded, stripInvalidAttributes, type CascadedSnapshot } from "./cascade";
import { renderSweepExtracted, resolveMinimalVars } from "./renderSweep";

/*
 * The Linaria port's equivalence proof.
 *
 * `results/ui-minimal-cascade.styled-components.reference.json` is the full
 * cascaded snapshot of the kit as it was on styled-components (2,240 cases,
 * saved before the port). This builds the ported kit the way an app does
 * (`buildKit.mjs`: wyw-in-js, extracted CSS), renders the same sweep from the
 * built module against the one extracted stylesheet, and compares case by
 * case: the winning value of every property on every element, the keyframes
 * animations run, and the markup.
 *
 * Every difference is written to results/linaria-port.diffs.json so it can be
 * read in full, not just the first dozen.
 */

const LAB = new URL("../../", import.meta.url);
const path = (p: string) => fileURLToPath(new URL(p, LAB));
const REFERENCE = path("results/ui-minimal-cascade.styled-components.reference.json");

describe("Linaria port equivalence (ssr, built kit)", () => {
  it("renders every case as the styled-components kit did", async () => {
    expect(existsSync(REFERENCE), "styled-components reference snapshot missing").toBe(true);
    execFileSync(process.execPath, [path("src/ssr/buildKit.mjs")], { stdio: "pipe" });
    const css = readFileSync(path("results/kit-build/ui-minimal.css"), "utf8");
    const ui = (await import(/* @vite-ignore */ path("results/kit-build/ui-minimal.js"))) as Record<string, unknown>;

    const snapshot = renderSweepExtracted(ui, css);
    const current: CascadedSnapshot = {};
    for (const [name, cases] of Object.entries(snapshot)) {
      current[name] = {};
      for (const [caseName, rendered] of Object.entries(cases)) {
        current[name][caseName] = "error" in rendered ? { error: rendered.error } : cascadeOf({ css: resolveMinimalVars(rendered.css), html: rendered.html });
      }
    }
    const reference = JSON.parse(readFileSync(REFERENCE, "utf8")) as CascadedSnapshot;
    // The reference was cascaded before the harness normalised keyframe names and
    // Linaria's inline variables, so re-derive it through today's normaliser.
    const normalised = normaliseReference(reference);
    const comparison = compareCascaded(normalised, current);
    // Concrete examples of the two diff kinds compareCascaded only names.
    const htmlExamples: Array<{ case: string; reference: string; current: string }> = [];
    const errorExamples: Array<{ case: string; reference: string; current: string }> = [];
    for (const [name, cases] of Object.entries(normalised)) {
      for (const [caseName, ref] of Object.entries(cases)) {
        const now = current[name]?.[caseName];
        if (!now) continue;
        if ("error" in ref || "error" in now) {
          if (errorExamples.length < 12 && JSON.stringify(ref) !== JSON.stringify(now)) {
            errorExamples.push({ case: `${name} ${caseName}`, reference: "error" in ref ? ref.error : "ok", current: "error" in now ? now.error : "ok" });
          }
          continue;
        }
        if (ref.html !== now.html && htmlExamples.filter((e) => e.case.startsWith(name)).length < 1 && htmlExamples.length < 40) {
          htmlExamples.push({ case: `${name} ${caseName}`, reference: ref.html, current: now.html });
        }
      }
    }
    writeFileSync(path("results/linaria-port.current.json"), JSON.stringify(current));
    writeFileSync(
      path("results/linaria-port.diffs.json"),
      `${JSON.stringify({ identical: comparison.identical, sameError: comparison.sameError, diffCount: comparison.diffs.length, added: comparison.added, errorExamples, htmlExamples, diffs: comparison.diffs }, null, 2)}\n`,
    );
    console.info(`linaria port: ${comparison.identical} identical, ${comparison.sameError} same error, ${comparison.diffs.length} different`);
    /*
     * One accepted difference, and only this shape of it: MinimalFieldGrid given
     * the sweep's generic `columns` array (Table's column objects) where it
     * takes a number. styled-components serialised the object into bogus
     * declarations (`header: A; label: A; grid-template-columns: repeat(key:a`)
     * and Linaria stringifies it (`repeat([object Object], …)`). Both are
     * garbage for input the component never accepts; neither is a rendering of
     * a valid prop. Any other difference in FieldGrid still fails.
     */
    const accepted = (diff: string) =>
      diff.startsWith("MinimalFieldGrid ") &&
      diff
        .split(": ")
        .slice(1)
        .join(": ")
        .split(" | ")
        .every((part) => /:: (grid-template-columns: repeat\(key:a -> repeat\(\[object Object\]|header: A -> undefined|label: A -> undefined)/.test(part));
    const unexplained = comparison.diffs.filter((diff) => !accepted(diff));
    expect(unexplained.slice(0, 15), `${unexplained.length} case(s) differ (results/linaria-port.diffs.json)`).toEqual([]);
    expect(comparison.identical + comparison.diffs.length - unexplained.length).toBe(2240);
  }, 300_000);
});

/** Apply today's keyframe-name normalisation to a snapshot cascaded before it existed. */
const normaliseReference = (snapshot: CascadedSnapshot): CascadedSnapshot => {
  const out: CascadedSnapshot = {};
  for (const [name, cases] of Object.entries(snapshot)) {
    out[name] = {};
    for (const [caseName, entry] of Object.entries(cases)) {
      if ("error" in entry) {
        out[name][caseName] = entry;
        continue;
      }
      const flat = { ...entry.flat };
      const bodies = entry.keyframes.map((kf) => ({ name: kf.match(/^@keyframes\s*([\w-]+)/)?.[1], body: kf.replace(/^@keyframes\s*[\w-]+/, "@keyframes") }));
      for (const { name: kfName, body } of bodies) {
        if (!kfName || kfName === "{") continue;
        for (const key of Object.keys(flat)) if (/:: animation(-name)?$/.test(key)) flat[key] = flat[key].split(kfName).join(`KF[${body}]`);
      }
      out[name][caseName] = { flat, keyframes: bodies.map((b) => b.body).sort(), html: stripInvalidAttributes(entry.html) };
    }
  }
  return out;
};
