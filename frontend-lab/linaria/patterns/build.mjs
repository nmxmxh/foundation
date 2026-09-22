import { fileURLToPath } from "node:url";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { build } from "vite";
import wyw from "@wyw-in-js/vite";
const here = (p) => fileURLToPath(new URL(p, import.meta.url));
writeFileSync(here("entry.tsx"), `import { renderToStaticMarkup } from "react-dom/server";\nimport { render, globals } from "./Patterns";\nexport const html = () => renderToStaticMarkup(render());\nexport { globals };\n`);
await build({
  root: here("."), logLevel: "warn", configFile: false,
  resolve: { alias: [{ find: /^@ovasabi\/ui-minimal\/tokens$/, replacement: here("../../../ui-minimal/ts/src/tokens.ts") }] },
  plugins: [wyw({ include: ["**/*.{ts,tsx}"] })],
  build: { outDir: here("../../results/linaria-patterns"), emptyOutDir: true, minify: false, ssr: false, lib: { entry: here("entry.tsx"), formats: ["es"], fileName: "entry" }, rollupOptions: { external: [/^react/] } },
});
const dir = here("../../results/linaria-patterns/");
for (const f of readdirSync(dir)) if (f.endsWith(".css")) console.log("--- CSS ---\n" + readFileSync(dir + f, "utf8"));
const { html } = await import(dir + "entry.js");
console.log("--- HTML ---\n" + html());
