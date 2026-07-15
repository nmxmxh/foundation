#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { copyFile, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const expectedNpmLicense = "SEE LICENSE IN LICENSE";
const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const rootLicensePath = path.join(repoRoot, "LICENSE");
const sync = process.argv.includes("--sync");
const verifyPack = process.argv.includes("--verify-pack");
const npmCache =
  process.env.FOUNDATION_NPM_CACHE_DIR ??
  path.join(process.env.TMPDIR ?? "/tmp", "ovasabi-foundation-npm-cache");

const npmManifests = [
  "cmd/ovasabi/package.json",
  "config-contracts/ts/package.json",
  "frontend-kit/ts/package.json",
  "runtime-native/ts/package.json",
  "runtime-sdk/ts/browser-host/package.json",
  "runtime-transport/ts/package.json",
  "ui-minimal/ts/package.json",
];

const cargoExpectations = new Map([
  ["runtime-native/rust/Cargo.toml", 'license-file = "../../LICENSE"'],
  ["runtime-sdk/rust/Cargo.toml", 'license-file = "../../LICENSE"'],
  [
    "runtime-sdk/rust/crates/ovrt-browser/Cargo.toml",
    "license-file.workspace = true",
  ],
  [
    "runtime-sdk/rust/crates/ovrt-core/Cargo.toml",
    "license-file.workspace = true",
  ],
  [
    "runtime-sdk/rust/crates/ovrt-ffi/Cargo.toml",
    "license-file.workspace = true",
  ],
  [
    "runtime-sdk/rust/crates/ovrt-native/Cargo.toml",
    "license-file.workspace = true",
  ],
  [
    "runtime-sdk/rust/crates/ovrt-unit/Cargo.toml",
    "license-file.workspace = true",
  ],
  [
    "runtime-sdk/rust/crates/ovrt-wasm-host/Cargo.toml",
    "license-file.workspace = true",
  ],
]);
const cargoPackageManifests = [
  "runtime-native/rust/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-browser/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-core/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-ffi/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-native/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-unit/Cargo.toml",
  "runtime-sdk/rust/crates/ovrt-wasm-host/Cargo.toml",
];

const failures = [];
const rootLicense = await readFile(rootLicensePath);

for (const manifestRelative of npmManifests) {
  const manifestPath = path.join(repoRoot, manifestRelative);
  const packageDir = path.dirname(manifestPath);
  const packageLicensePath = path.join(packageDir, "LICENSE");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));

  if (manifest.license !== expectedNpmLicense) {
    failures.push(
      `${manifestRelative}: license must be ${JSON.stringify(expectedNpmLicense)}`,
    );
  }

  if (sync) {
    await copyFile(rootLicensePath, packageLicensePath);
  }

  let packageLicense;
  try {
    packageLicense = await readFile(packageLicensePath);
  } catch {
    failures.push(`${manifestRelative}: package-level LICENSE is missing`);
    continue;
  }

  if (!packageLicense.equals(rootLicense)) {
    failures.push(`${manifestRelative}: package-level LICENSE differs from /LICENSE`);
  }

  const lockPath = path.join(packageDir, "package-lock.json");
  try {
    const lock = JSON.parse(await readFile(lockPath, "utf8"));
    if (lock.packages?.[""]?.license !== expectedNpmLicense) {
      failures.push(
        `${path.relative(repoRoot, lockPath)}: root package license metadata is stale`,
      );
    }
  } catch (error) {
    if (error?.code !== "ENOENT") {
      failures.push(`${path.relative(repoRoot, lockPath)}: ${error.message}`);
    }
  }

  if (verifyPack) {
    try {
      const output = execFileSync(
        "npm",
        ["pack", "--dry-run", "--json", "--ignore-scripts"],
        {
          cwd: packageDir,
          encoding: "utf8",
          env: { ...process.env, npm_config_cache: npmCache },
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      const pack = JSON.parse(output);
      const files = pack[0]?.files ?? [];
      if (!files.some((file) => file.path === "LICENSE")) {
        failures.push(`${manifestRelative}: npm package omits LICENSE`);
      }
    } catch (error) {
      const detail = error.stderr?.toString().trim() || error.message;
      failures.push(`${manifestRelative}: npm pack verification failed: ${detail}`);
    }
  }
}

for (const [manifestRelative, expectedLine] of cargoExpectations) {
  const manifest = await readFile(path.join(repoRoot, manifestRelative), "utf8");
  if (!manifest.split("\n").includes(expectedLine)) {
    failures.push(`${manifestRelative}: missing ${expectedLine}`);
  }
}

if (verifyPack) {
  for (const manifestRelative of cargoPackageManifests) {
    try {
      const files = execFileSync(
        "cargo",
        [
          "package",
          "--list",
          "--allow-dirty",
          "--offline",
          "--manifest-path",
          manifestRelative,
        ],
        {
          cwd: repoRoot,
          encoding: "utf8",
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      if (!files.split("\n").includes("LICENSE")) {
        failures.push(`${manifestRelative}: Cargo package omits LICENSE`);
      }
    } catch (error) {
      const detail = error.stderr?.toString().trim() || error.message;
      failures.push(`${manifestRelative}: Cargo package verification failed: ${detail}`);
    }
  }
}

if (failures.length > 0) {
  for (const failure of failures) {
    console.error(`[FAIL] ${failure}`);
  }
  process.exitCode = 1;
} else {
  const mode = verifyPack
    ? "metadata, files, npm tarballs, and Cargo crates"
    : "metadata and files";
  console.log(`[OK] Package license ${mode} match /LICENSE`);
}
