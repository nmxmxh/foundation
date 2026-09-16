#!/usr/bin/env node
// Applies the civic_watch_ng_v1-proven phase B config edits to a scaffold-shaped
// frontend: wyw plugin (kit + app src), drop styled-components/framer-motion
// aliases, dedupe and chunks, jest-dom/vitest, drop the two deps from package.json.
// Every edit is exact-string; anything not in scaffold shape is reported, not guessed.
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const root = process.argv[2];
// --keep-framer: the app still imports framer-motion itself; only styled-components goes.
const keepFramer = process.argv.includes("--keep-framer");
const log = (k, f, t) => console.log(`${k} ${f} ${t}`);

const PLUGIN = (cast) => `    wyw({
      // Styles are Linaria, extracted at build time — ui-minimal's and this app's own
      // (Foundation research doc 14.8). The kit is reached through node_modules,
      // hence transformLibraries.
      include: [/ui-minimal[\\\\/](ts[\\\\/])?src[\\\\/].*\\.[jt]sx?$/, /[\\\\/]src[\\\\/].*\\.[jt]sx?$/],
      transformLibraries: true,
      prefixer: false,
    })${cast},`;

function edit(name, steps) {
  const file = join(root, name);
  if (!existsSync(file)) return log("missing", name, "");
  let code = readFileSync(file, "utf8");
  for (const [from, to, what] of steps) {
    if (to && code.includes(to)) continue;
    if (!code.includes(from)) { log("manual", name, `not found: ${what}`); continue; }
    code = code.replace(from, to);
  }
  writeFileSync(file, code);
  log("done", name, "");
}

const F = keepFramer ? ", 'framer-motion'" : "";

const aliasSteps = [
  ["      'styled-components': path.resolve(__dirname, './node_modules/styled-components'),\n", "", "styled alias"],
  ...(keepFramer ? [] : [["      'framer-motion': path.resolve(__dirname, './node_modules/framer-motion'),\n", "", "framer alias"]]),
];

edit("vite.config.ts", [
  ["import react from '@vitejs/plugin-react'\n", "import react from '@vitejs/plugin-react'\nimport wyw from '@wyw-in-js/vite'\n", "react import"],
  ["  plugins: [react()],", `  plugins: [\n    react(),\n${PLUGIN("")}\n  ],`, "plugins"],
  ...aliasSteps,
  ["    dedupe: ['react', 'react-dom', 'styled-components', 'framer-motion', 'zustand'],", `    dedupe: ['react', 'react-dom', '@linaria/react'${F}, 'zustand'],`, "dedupe"],
  ["          ui: ['styled-components', 'framer-motion'],", `          ui: ['@linaria/react'${F}],`, "ui chunk"],
]);

edit("vitest.config.ts", [
  ["import react from '@vitejs/plugin-react'\n", "import react from '@vitejs/plugin-react'\nimport wyw from '@wyw-in-js/vite'\n", "react import"],
  ["  plugins: [react() as never],", `  plugins: [\n    react() as never,\n${PLUGIN(" as never")}\n  ],`, "plugins"],
  ...aliasSteps,
  ["    dedupe: ['react', 'react-dom', 'styled-components', 'framer-motion'],", `    dedupe: ['react', 'react-dom', '@linaria/react'${F}],`, "dedupe"],
]);

edit("src/test/setup.ts", [["import '@testing-library/jest-dom'\n", "import '@testing-library/jest-dom/vitest'\n", "jest-dom import"]]);

edit("package.json", [
  ['    "styled-components": "^6.1.0",\n', "", "styled dep"],
  ...(keepFramer ? [] : [['    "framer-motion": "^12.0.0",\n', "", "framer dep"]]),
]);
