#!/usr/bin/env node
/**
 * Managed patch: bring an existing app's Tauri shell up to the scaffold shape.
 *
 * `native/package.json` and the three `src-tauri/tauri*.conf.json` files are
 * create-mode — written once at seed, then project-owned — so an ordinary
 * update cannot deliver a template change to an app that already exists. The
 * template moved and `project_scaffold_check.sh` tightened with it, leaving ten
 * of eleven apps failing four rules with no delivery path. This patch is that
 * path.
 *
 * Two fixes, both mechanical:
 *
 * 1. The before-commands run from `native/`, not from `native/src-tauri/`, so
 *    `cd ../../frontend` walks one level too far and the build fails to find
 *    the frontend. Only the `cd` segment is rewritten — an app is free to put
 *    its own environment in the rest of the command, and ChooseChow does.
 * 2. Tauri's mobile build phases shell out to `npm run tauri`, so the script
 *    has to exist. The remaining ios/android scripts are added when missing,
 *    matching the template; an existing script is never overwritten.
 *
 * Idempotent: each edit is guarded on the text it inserts. Nothing else in
 * either file is touched, so `frontendDist` stays whatever the app set it to
 * (ovasabi builds to `dist/client`).
 *
 *   node tooling/scripts/native_shell_patch.mjs <native-root>
 *
 * Output: one line per file, `patched <path> <what>` or `manual <path> <why>`.
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";

const root = process.argv[2];
if (!root || !existsSync(root)) process.exit(0);
const out = (kind, file, text) => console.log(`${kind} ${relative(join(root, ".."), file)} ${text}`);

/* ── the before-commands run from native/, one level up from src-tauri/ ── */

for (const name of ["tauri.conf.json", "tauri.dev.conf.json", "tauri.prod.conf.json"]) {
  const file = join(root, "src-tauri", name);
  if (!existsSync(file)) continue;
  const code = readFileSync(file, "utf8");
  if (!code.includes("cd ../../frontend")) continue;
  writeFileSync(file, code.split("cd ../../frontend").join("cd ../frontend"));
  out("patched", file, "resolves the frontend from native/, not native/src-tauri/");
}

/* ── the scripts Tauri's mobile build phases call ────────────────────── */

// Order matters only for readability; `tauri` goes first the way the template
// has it, the rest are appended in template order.
const REQUIRED = [
  ["tauri", "tauri"],
  ["ios:init", "tauri ios init"],
  ["ios:dev", "tauri ios dev --config src-tauri/tauri.dev.conf.json"],
  ["ios:build", "tauri ios build --config src-tauri/tauri.prod.conf.json"],
  ["android:init", "tauri android init"],
  ["android:dev", "tauri android dev --config src-tauri/tauri.dev.conf.json"],
  ["android:build", "tauri android build --config src-tauri/tauri.prod.conf.json"],
];

const pkg = join(root, "package.json");
if (existsSync(pkg)) {
  let code = readFileSync(pkg, "utf8");
  const open = code.indexOf('"scripts": {');
  if (open === -1) {
    out("manual", pkg, 'no "scripts" block: add "tauri": "tauri" so Tauri\'s mobile build phases can call it');
  } else {
    // Text insertion rather than a JSON round trip, so an app's own formatting,
    // key order and comments-by-convention survive the patch.
    const missing = REQUIRED.filter(([key]) => !new RegExp(`"${key.replace(":", "\\:")}"\\s*:`).test(code));
    if (missing.length) {
      const head = open + '"scripts": {'.length;
      // An empty block has nothing to carry the trailing comma.
      const empty = /^\s*\}/.test(code.slice(head));
      const lines = missing.map(([key, value]) => `\n    "${key}": "${value}"`);
      const added = empty ? lines.join(",") + "\n  " : lines.join(",") + ",";
      const next = code.slice(0, head) + added + code.slice(head);
      // Never leave a package.json that will not parse: the app's whole npm
      // surface depends on it, and a text edit is only as good as its result.
      try {
        JSON.parse(next);
      } catch (error) {
        out("manual", pkg, `add ${missing.map(([k]) => k).join(", ")} by hand: a patched file would not parse (${error.message})`);
        process.exit(0);
      }
      writeFileSync(pkg, next);
      out("patched", pkg, `adds ${missing.map(([k]) => k).join(", ")} for Tauri's mobile build phases`);
    }
  }
}
