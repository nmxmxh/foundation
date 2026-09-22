#!/usr/bin/env node
/**
 * Build @ovasabi/ui-minimal the way a production app does — through wyw-in-js,
 * with styles extracted to a static CSS file — and write:
 *
 *   results/kit-build/ui-minimal.js    the built module (React externals)
 *   results/kit-build/ui-minimal.css   every rule the kit ships
 *
 * The cascade harness renders from this build and reads this CSS, because
 * after the Linaria port there is no runtime stylesheet to collect: the CSS a
 * user downloads is the CSS that must be proven equivalent.
 *
 *   node src/ssr/buildKit.mjs
 */
import { existsSync, readdirSync, renameSync, statSync } from "node:fs";
import { gzipSync, brotliCompressSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import wyw from "@wyw-in-js/vite";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));
const outDir = here("../../results/kit-build/");
const started = performance.now();

await build({
  configFile: false,
  logLevel: "warn",
  root: here("../.."),
  resolve: {
    alias: [
      { find: /^@ovasabi\/ui-minimal\/tokens$/, replacement: here("../../../ui-minimal/ts/src/tokens.ts") },
    ],
    dedupe: ["react", "react-dom"],
  },
  plugins: [
    wyw({
      include: ["**/ui-minimal/ts/src/**/*.{ts,tsx}"],
      prefixer: false,
    }),
  ],
  build: {
    outDir,
    emptyOutDir: true,
    minify: false,
    sourcemap: false,
    cssCodeSplit: false,
    lib: { entry: here("../../../ui-minimal/ts/src/index.ts"), formats: ["es"], fileName: () => "ui-minimal.js" },
    rollupOptions: { external: [/^react($|\/)/, /^react-dom($|\/)/, /^styled-components/] },
  },
});

// Vite names the single stylesheet after the package; give it a stable name.
for (const file of readdirSync(outDir)) {
  if (file.endsWith(".css") && file !== "ui-minimal.css") renameSync(outDir + file, outDir + "ui-minimal.css");
}
const cssPath = outDir + "ui-minimal.css";
if (!existsSync(cssPath)) throw new Error("kit build produced no CSS");
const css = readFileSync(cssPath);
const js = readFileSync(outDir + "ui-minimal.js");
const kb = (n) => `${(n / 1024).toFixed(1)} KB`;
console.log(
  `built in ${Math.round(performance.now() - started)} ms — css ${kb(css.length)} (gzip ${kb(gzipSync(css).length)}, br ${kb(brotliCompressSync(css).length)}), js ${kb(js.length)} (gzip ${kb(gzipSync(js).length)}, br ${kb(brotliCompressSync(js).length)})`,
);
console.log(`styled-components in js: ${js.includes("styled-components") ? "present" : "absent"}; stylis: ${js.includes("stylis") ? "present" : "absent"}; size check ${statSync(cssPath).size > 0}`);
