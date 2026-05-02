#!/usr/bin/env node
import { mkdir, readdir, rename, stat, writeFile } from "node:fs/promises";
import path from "node:path";

const args = new Map();
for (let i = 2; i < process.argv.length; i += 1) {
  const arg = process.argv[i];
  if (!arg.startsWith("--")) continue;
  const key = arg.slice(2);
  const next = process.argv[i + 1];
  if (next && !next.startsWith("--")) {
    args.set(key, next);
    i += 1;
  } else {
    args.set(key, "true");
  }
}

const publicDir = path.resolve(args.get("public-dir") ?? "frontend/public");
const outPath = path.resolve(args.get("out") ?? path.join(publicDir, "runtime", "wasm-manifest.json"));

async function fileExists(filePath) {
  try {
    const entry = await stat(filePath);
    return entry.isFile();
  } catch {
    return false;
  }
}

async function artifactFor(filePath, role) {
  const entry = await stat(filePath);
  const rel = path.relative(publicDir, filePath).split(path.sep).join("/");
  const brotliPath = `${filePath}.br`;
  return {
    id: path.basename(filePath, ".wasm"),
    role,
    kind: "wasm",
    url: `/${rel}`,
    brotliUrl: (await fileExists(brotliPath)) ? `/${rel}.br` : undefined,
    bytes: entry.size,
  };
}

const artifacts = [];
for (const [fileName, role] of [
  ["main.wasm", "go-compat"],
  ["kernel.wasm", "kernel"],
]) {
  const filePath = path.join(publicDir, fileName);
  if (await fileExists(filePath)) {
    artifacts.push(await artifactFor(filePath, role));
  }
}

const moduleDir = path.join(publicDir, "modules");
try {
  const moduleEntries = await readdir(moduleDir, { withFileTypes: true });
  for (const entry of moduleEntries) {
    if (!entry.isFile() || !entry.name.endsWith(".wasm")) continue;
    artifacts.push(await artifactFor(path.join(moduleDir, entry.name), "rust-module"));
  }
} catch {
  // Projects without Rust/WASM modules still get a manifest for capability probing.
}

const manifest = {
  schemaVersion: 1,
  generatedAt: new Date().toISOString(),
  artifacts,
};

await mkdir(path.dirname(outPath), { recursive: true });
await writeFile(`${outPath}.tmp`, `${JSON.stringify(manifest, null, 2)}\n`);
await rename(`${outPath}.tmp`, outPath);
console.log(`Wrote ${path.relative(process.cwd(), outPath)} with ${artifacts.length} artifact(s)`);
