#!/usr/bin/env node
/**
 * Managed patch: deliver ui-minimal's Linaria build requirements to an
 * existing app (Foundation research doc 14.8).
 *
 * ui-minimal's styles are extracted at build time. An app that syncs the kit
 * without the wyw-in-js plugin in its Vite build *and* its Vitest runner gets a
 * Linaria `styled` that throws at runtime. `vite.config.ts` is project-owned
 * (create-mode) and `vitest.config.ts` is force-managed, so an ordinary update
 * cannot deliver the plugin — this patch does, in place.
 *
 * Idempotent: each edit is guarded on the text it inserts. A file that does not
 * have the scaffold's shape is reported as a manual step, never rewritten.
 *
 *   node tooling/scripts/frontend_linaria_patch.mjs <frontend-root>
 *
 * Output: one line per file, `patched <path> <what>` or `manual <path> <why>`.
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";

const root = process.argv[2];
if (!root || !existsSync(root)) process.exit(0);
const out = (kind, file, text) => console.log(`${kind} ${relative(join(root, ".."), file)} ${text}`);

// Must stay byte-identical in intent to templates/frontend/vite.config.ts: both the
// kit's sources and the app's own `src/` are Linaria. Omitting the app pattern leaves
// a file that imports `styled` from @linaria/react untransformed, and the runtime tag
// throws on first render rather than failing the build.
const PLUGIN =
  `wyw({ include: [/ui-minimal[\\\\/](ts[\\\\/])?src[\\\\/].*\\.[jt]sx?$/, /[\\\\/]src[\\\\/].*\\.[jt]sx?$/], transformLibraries: true, prefixer: false })`;

const REACT_IMPORT = /^import react from ['"]@vitejs\/plugin-react['"];?\s*$/m;

for (const name of ["vite.config.ts", "vitest.config.ts"]) {
  const file = join(root, name);
  if (!existsSync(file)) continue;
  let code = readFileSync(file, "utf8");
  if (code.includes("@wyw-in-js/vite")) continue;
  const importMatch = code.match(REACT_IMPORT);
  const pluginsMatch = code.match(/plugins:\s*\[\s*react\(\)(\s+as\s+never)?/);
  if (!importMatch || !pluginsMatch) {
    out("manual", file, "add the @wyw-in-js/vite plugin (ui-minimal is Linaria; see Foundation research doc 14.8)");
    continue;
  }
  const semi = importMatch[0].trimEnd().endsWith(";") ? ";" : "";
  code = code.replace(REACT_IMPORT, (line) => `${line.trimEnd()}\nimport wyw from '@wyw-in-js/vite'${semi}`);
  const cast = pluginsMatch[1] ?? "";
  code = code.replace(pluginsMatch[0], `${pluginsMatch[0]}, ${PLUGIN}${cast}`);
  writeFileSync(file, code);
  out("patched", file, "adds the @wyw-in-js/vite plugin for ui-minimal's extracted styles");
}
