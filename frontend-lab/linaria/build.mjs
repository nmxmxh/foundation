// Linaria spike build: can wyw-in-js extract a component that imports
// Foundation's ui-minimal tokens from TypeScript source outside the project root?
import { fileURLToPath } from "node:url";
import { readdirSync, readFileSync } from "node:fs";
import { build } from "vite";
import wyw from "@wyw-in-js/vite";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));
const started = performance.now();
await build({
  root: here("."),
  logLevel: "warn",
  configFile: false,
  resolve: {
    alias: [
      { find: /^@ovasabi\/ui-minimal\/tokens$/, replacement: here("../../ui-minimal/ts/src/tokens.ts") },
      { find: /^@ovasabi\/ui-minimal$/, replacement: here("../../ui-minimal/ts/src/index.ts") },
    ],
    dedupe: ["react", "react-dom", "styled-components"],
  },
  plugins: [
    wyw({
      include: ["**/*.{ts,tsx}"],
      babelOptions: { presets: ["@babel/preset-typescript", ["@babel/preset-react", { runtime: "automatic" }]] },
    }),
  ],
  build: { outDir: here("../results/linaria-spike"), emptyOutDir: true, minify: false },
});
const assets = here("../results/linaria-spike/assets/");
const css = readdirSync(assets).filter((f) => f.endsWith(".css")).map((f) => readFileSync(assets + f, "utf8")).join("\n");
const js = readdirSync(assets).filter((f) => f.endsWith(".js")).map((f) => readFileSync(assets + f, "utf8")).join("\n");
console.log(`built in ${Math.round(performance.now() - started)} ms`);
console.log("--- extracted CSS ---\n" + css.slice(0, 1500));
console.log("--- runtime markers in JS ---");
for (const marker of ["styled-components", "stylis", "@linaria/react", "minimalVars", "--minimal-color-bg-surface"]) {
  console.log(`${marker}: ${js.includes(marker) ? "present" : "absent"}`);
}
