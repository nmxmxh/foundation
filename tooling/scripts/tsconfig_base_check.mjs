#!/usr/bin/env node
// tsconfig_base_check: language level comes from one place.
//
// Every foundation package, frontend-lab and the app frontend template extends
// tooling/tsconfig/base.json or dom.json. None may set the shared options
// itself. A package once targeted ES2022 while the app template targeted
// ES2020; String.replaceAll passed the package check, then failed every app
// build that compiled the package from source. One base makes that impossible.
//
// It also requires one TypeScript range across every package manifest.
//
// Usage: node tooling/scripts/tsconfig_base_check.mjs [foundation-root]

import { existsSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";

const root = resolve(process.argv[2] ?? ".");
const BASES = ["tooling/tsconfig/base.json", "tooling/tsconfig/dom.json"].map((p) => join(root, p));
const SHARED = ["target", "lib", "module", "moduleResolution", "strict"];

// Config file -> where its tooling/ lives (templates resolve in the scaffolded app).
const CONFIGS = [
  "config-contracts/ts/tsconfig.json",
  "frontend-kit/ts/tsconfig.json",
  "runtime-native/ts/tsconfig.json",
  "runtime-sdk/ts/browser-host/tsconfig.json",
  "runtime-transport/ts/tsconfig.json",
  "ui-minimal/ts/tsconfig.json",
  "frontend-lab/tsconfig.json",
  "templates/frontend/tsconfig.app.json",
  "templates/frontend/tsconfig.node.json",
];
const MANIFESTS = [
  "config-contracts/ts/package.json",
  "frontend-kit/ts/package.json",
  "runtime-native/ts/package.json",
  "runtime-sdk/ts/browser-host/package.json",
  "runtime-transport/ts/package.json",
  "ui-minimal/ts/package.json",
  "frontend-lab/package.json",
  "templates/frontend/package.json",
];

const failures = [];
const fail = (msg) => failures.push(msg);

for (const rel of CONFIGS) {
  const file = join(root, rel);
  if (!existsSync(file)) {
    fail(`${rel}: missing`);
    continue;
  }
  let config;
  try {
    config = JSON.parse(readFileSync(file, "utf8"));
  } catch (error) {
    fail(`${rel}: not plain JSON (${error.message}); comments are not allowed in governed configs`);
    continue;
  }
  if (typeof config.extends !== "string") {
    fail(`${rel}: must extend tooling/tsconfig/base.json or dom.json`);
    continue;
  }
  // Templates sit in an app's frontend/ beside the vendored foundation/ copy.
  const fromDir = rel.startsWith("templates/frontend/")
    ? join(root, "__app__/frontend")
    : dirname(file);
  const target = resolve(fromDir, config.extends).replace(join(root, "__app__/foundation"), root);
  if (!BASES.includes(target)) {
    fail(`${rel}: extends ${config.extends}, which is not tooling/tsconfig/base.json or dom.json`);
  }
  const local = Object.keys(config.compilerOptions ?? {}).filter((key) => SHARED.includes(key));
  if (local.length > 0) {
    fail(`${rel}: sets ${local.join(", ")} locally; these come from the shared base only`);
  }
  console.log(`[OK] ${rel} extends ${relative(root, target)}`);
}

const ranges = new Map();
for (const rel of MANIFESTS) {
  const file = join(root, rel);
  if (!existsSync(file)) continue;
  const pkg = JSON.parse(readFileSync(file, "utf8"));
  const range = pkg.devDependencies?.typescript ?? pkg.dependencies?.typescript;
  if (range) ranges.set(rel, range);
}
const distinct = new Set(ranges.values());
if (distinct.size > 1) {
  fail(`TypeScript ranges differ: ${[...ranges].map(([f, r]) => `${f}=${r}`).join(", ")}`);
} else {
  console.log(`[OK] one TypeScript range across ${ranges.size} manifests: ${[...distinct][0] ?? "none"}`);
}

if (failures.length > 0) {
  for (const message of failures) console.log(`[FAIL] ${message}`);
  console.log("tsconfig base check failed");
  process.exit(1);
}
console.log("tsconfig base check passed");
