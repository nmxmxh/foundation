import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { cascadeSnapshot, compareCascaded, type CascadedSnapshot } from "./cascade";
import { renderSweepExtracted } from "./renderSweep";

/*
 * The styling regression eval for ui-minimal.
 *
 * Every Minimal* component is rendered across the prop sweep and reduced to the
 * styles that actually win on each element. Each case's result must hash to the
 * committed digest. A difference is either a regression, or an intended visual
 * change — in which case regenerate deliberately:
 *
 *   UPDATE_BASELINE=1 npx vitest run --project ssr
 *
 * and say in the change why the rendering moved. The baseline was seeded from
 * sources already proven equivalent to the pre-option-B render (research doc
 * section 13), so it carries that proof forward.
 *
 * Why digests: the full cascaded snapshot is ~7.5 MB. The committed baseline
 * holds one SHA-256 per case (~200 KB). Every run also writes the full snapshot
 * to results/ (gitignored); keep the one from before a change and the eval
 * prints exactly which styles moved, via compareCascaded.
 */

const LAB = new URL("../../", import.meta.url);
const BASELINE = fileURLToPath(new URL("baselines/ui-minimal-cascade.digests.json", LAB));
const RESULTS_DIR = fileURLToPath(new URL("results/", LAB));
const FULL_SNAPSHOT = fileURLToPath(new URL("results/ui-minimal-cascade.full.json", LAB));
const PREVIOUS_SNAPSHOT = fileURLToPath(new URL("results/ui-minimal-cascade.previous.json", LAB));

type Digests = Record<string, Record<string, string>>;

const digest = (value: unknown) => createHash("sha256").update(JSON.stringify(value)).digest("hex");

const digestsOf = (snapshot: CascadedSnapshot): Digests => {
  const out: Digests = {};
  for (const [name, cases] of Object.entries(snapshot)) {
    out[name] = {};
    for (const [caseName, value] of Object.entries(cases)) out[name][caseName] = digest(value);
  }
  return out;
};

describe("ui-minimal cascade eval (ssr)", () => {
  it("renders every component case identically to the committed baseline", async () => {
    /*
     * The kit is extracted at build time (Linaria, research doc 14.7), so the
     * eval renders the production build and reads the stylesheet it emitted —
     * the CSS a user downloads — rather than collecting a runtime sheet.
     */
    execFileSync(process.execPath, [fileURLToPath(new URL("src/ssr/buildKit.mjs", LAB))], { stdio: "pipe" });
    const css = readFileSync(fileURLToPath(new URL("results/kit-build/ui-minimal.css", LAB)), "utf8");
    const ui = (await import(/* @vite-ignore */ fileURLToPath(new URL("results/kit-build/ui-minimal.js", LAB)))) as Record<string, unknown>;
    const current = cascadeSnapshot(renderSweepExtracted(ui, css));
    const cases = Object.values(current).reduce((sum, perCase) => sum + Object.keys(perCase).length, 0);
    expect(cases).toBeGreaterThan(2000);

    mkdirSync(RESULTS_DIR, { recursive: true });
    if (existsSync(FULL_SNAPSHOT)) writeFileSync(PREVIOUS_SNAPSHOT, readFileSync(FULL_SNAPSHOT));
    writeFileSync(FULL_SNAPSHOT, JSON.stringify(current));

    const currentDigests = digestsOf(current);
    if (process.env.UPDATE_BASELINE === "1" || !existsSync(BASELINE)) {
      mkdirSync(fileURLToPath(new URL("baselines/", LAB)), { recursive: true });
      writeFileSync(BASELINE, `${JSON.stringify(currentDigests, null, 1)}\n`);
      if (process.env.UPDATE_BASELINE !== "1") {
        throw new Error(`No baseline existed; wrote ${BASELINE}. Review and commit it, then re-run.`);
      }
      return;
    }

    const baseline = JSON.parse(readFileSync(BASELINE, "utf8")) as Digests;
    const changed: string[] = [];
    const missing: string[] = [];
    for (const [name, perCase] of Object.entries(baseline)) {
      for (const [caseName, expected] of Object.entries(perCase)) {
        const actual = currentDigests[name]?.[caseName];
        if (actual === undefined) missing.push(`${name} ${caseName}`);
        else if (actual !== expected) changed.push(`${name} ${caseName}`);
      }
    }
    const added = Object.keys(currentDigests).filter((name) => !(name in baseline));
    if (added.length) console.info(`components new since the baseline (not compared): ${added.join(", ")}`);

    if (changed.length && existsSync(PREVIOUS_SNAPSHOT)) {
      const previous = JSON.parse(readFileSync(PREVIOUS_SNAPSHOT, "utf8")) as CascadedSnapshot;
      const { diffs } = compareCascaded(previous, current);
      if (diffs.length) console.info(`style changes against the previous run:\n  ${diffs.slice(0, 12).join("\n  ")}`);
    }

    expect(missing, `${missing.length} baseline case(s) no longer render`).toEqual([]);
    expect(changed.slice(0, 12), `${changed.length} case(s) differ from the baseline`).toEqual([]);
  });
});
