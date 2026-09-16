#!/usr/bin/env node
/**
 * Size-adjusted fallback faces for an app's web fonts, measured in the browser.
 *
 * `font-display: swap` paints text in a local fallback first and swaps to the
 * web font when it arrives. Different metrics wrap lines differently, so the
 * swap moves everything below the text: on ChooseChow's landing it is the
 * largest layout shift (hero moves 34 px, the CTA grows 66 → 99 px, CLS ≈ 0.06
 * per swap). A fallback face whose `size-adjust`, `ascent-override` and
 * `descent-override` match the web font wraps and sits the same, so the swap
 * changes glyph shapes, not layout.
 *
 * Measurement, not font-file parsing: each woff2 is loaded with FontFace and
 * measured with canvas measureText at 100 px — advance width of a sample, and
 * the font's ascent/descent (fontBoundingBox*) — against local fallbacks at the
 * same weight. Regular and bold are separate local faces (Arial vs Arial Bold),
 * so each family gets a regular and a bold fallback face.
 *
 *   LOAD_APP=profile/apps/choosechow.mjs node profile/fontFallbackMetrics.mjs
 *
 * Writes results/fonts/<app>-fallbacks.json and .css: an Arial/Helvetica-tuned
 * and a Roboto-tuned family per web font, each measured against the local font
 * it names (a group is skipped when that font is not installed here). Values
 * are Chromium's metrics for those local fonts; verify on a device.
 */
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "playwright";

const lab = fileURLToPath(new URL("..", import.meta.url));
const appPath = process.env.LOAD_APP;
if (!appPath) throw new Error("LOAD_APP=profile/apps/<app>.mjs is required");
const app = (await import(pathToFileURL(isAbsolute(appPath) ? appPath : resolve(lab, appPath)).href)).default;
if (!app.fonts) throw new Error(`${app.name} declares no fonts`);

// Mixed-case prose with digits and punctuation: widths track real copy, not caps or a pangram alone.
const SAMPLE = "Homemade food, delivered with love. The quick brown fox jumps over the lazy dog — 0123456789 ChooseChow";
const WEIGHTS = { regular: 400, bold: 700 };
const LOCALS = {
  regular: { Arial: ['local("Arial")', 'local("ArialMT")'], Helvetica: ['local("Helvetica")'], Roboto: ['local("Roboto")', 'local("Roboto-Regular")'] },
  bold: {
    Arial: ['local("Arial Bold")', 'local("Arial-BoldMT")'],
    Helvetica: ['local("Helvetica Bold")', 'local("Helvetica-Bold")'],
    Roboto: ['local("Roboto Bold")', 'local("Roboto-Bold")'],
  },
};

const faces = app.fonts.faces.map((face) => ({ family: face.family, data: readFileSync(join(app.fonts.dir, face.file)).toString("base64") }));
const browser = await chromium.launch({ headless: true });
let measured;
try {
  const page = await browser.newPage();
  await page.setContent("<!doctype html><canvas></canvas>");
  measured = await page.evaluate(
    async ({ faces, sample, weights, fallbacks }) => {
      for (const face of faces) {
        const bytes = Uint8Array.from(atob(face.data), (c) => c.charCodeAt(0));
        const loaded = await new FontFace(face.family, bytes, { weight: "100 900" }).load();
        document.fonts.add(loaded);
      }
      const context = document.querySelector("canvas").getContext("2d");
      const measure = (font) => {
        context.font = font;
        const m = context.measureText(sample);
        return { width: m.width, ascent: m.fontBoundingBoxAscent, descent: m.fontBoundingBoxDescent };
      };
      // A missing local family falls back to the default face: same width as a name that exists nowhere.
      const nowhere = measure('400 100px "No Such Font 7f3a"').width;
      const available = Object.fromEntries(fallbacks.map((name) => [name, measure(`400 100px "${name}"`).width !== nowhere]));
      const result = { available, faces: {} };
      for (const face of faces) {
        result.faces[face.family] = {};
        for (const [cut, weight] of Object.entries(weights)) {
          result.faces[face.family][cut] = {
            web: measure(`${weight} 100px "${face.family}"`),
            fallback: Object.fromEntries(fallbacks.map((name) => [name, measure(`${weight} 100px "${name}"`).width])),
          };
        }
      }
      return result;
    },
    { faces, sample: SAMPLE, weights: WEIGHTS, fallbacks: Object.keys(LOCALS.regular) },
  );
} finally {
  await browser.close();
}

/*
 * One fallback family per platform default, because one size-adjust cannot fit
 * both: Arial-tuned values measured 2.5% wide on Roboto regular and 9% on
 * Roboto Bold — enough to re-wrap bold headings on Android. "<family> Fallback"
 * uses Arial/Helvetica (macOS, iOS, Windows); "<family> Fallback Roboto" uses
 * Roboto (Android, ChromeOS). A platform without a face's local fonts skips it,
 * so each stack lists both names after the web family.
 */
const GROUPS = [
  { suffix: "Fallback", references: ["Arial", "Helvetica"] },
  { suffix: "Fallback Roboto", references: ["Roboto"] },
];
const pct = (n) => `${(n * 100).toFixed(2)}%`;
const results = {};
let css =
  `/* Size-adjusted fallbacks (${new Date().toISOString().slice(0, 10)}), generated by frontend-lab/profile/fontFallbackMetrics.mjs.\n` +
  `   In each font stack, list "<family> Fallback" then "<family> Fallback Roboto" right after the web family. */\n`;
for (const group of GROUPS) {
  const reference = group.references.find((name) => measured.available[name]);
  if (!reference) {
    console.warn(`skipping "${group.suffix}": none of ${group.references.join(", ")} is installed here to measure against`);
    continue;
  }
  for (const [family, cuts] of Object.entries(measured.faces)) {
    const name = `${family} ${group.suffix}`;
    results[name] = { reference };
    for (const [cut, { web, fallback }] of Object.entries(cuts)) {
      const sizeAdjust = web.width / fallback[reference];
      const entry = {
        weight: WEIGHTS[cut],
        sizeAdjust: pct(sizeAdjust),
        ascentOverride: pct(web.ascent / (100 * sizeAdjust)),
        descentOverride: pct(web.descent / (100 * sizeAdjust)),
      };
      results[name][cut] = entry;
      css +=
        `@font-face {\n  font-family: "${name}";\n  font-weight: ${cut === "regular" ? "100 549" : "550 900"};\n` +
        `  src: ${group.references.flatMap((local) => LOCALS[cut][local]).join(", ")};\n  size-adjust: ${entry.sizeAdjust};\n` +
        `  ascent-override: ${entry.ascentOverride};\n  descent-override: ${entry.descentOverride};\n  line-gap-override: 0%;\n}\n`;
    }
  }
}

const dir = join(lab, "results/fonts/");
mkdirSync(dir, { recursive: true });
writeFileSync(join(dir, `${app.name}-fallbacks.json`), `${JSON.stringify({ available: measured.available, sample: SAMPLE, results }, null, 2)}\n`);
writeFileSync(join(dir, `${app.name}-fallbacks.css`), css);
console.log(JSON.stringify({ available: measured.available, results }, null, 2));
console.log(`css: ${join(dir, `${app.name}-fallbacks.css`)}`);
