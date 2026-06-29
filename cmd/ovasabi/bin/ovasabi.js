#!/usr/bin/env node

const { spawnSync } = require("node:child_process");
const os = require("node:os");
const path = require("node:path");

const packageRoot = path.resolve(__dirname, "..");
const env = { ...process.env };
env.OVASABI_CALLER_CWD = process.cwd();

if (!env.GOCACHE) {
  env.GOCACHE = path.join(os.tmpdir(), "ovasabi-cli-go-build");
}

const result = spawnSync("go", ["run", ".", ...process.argv.slice(2)], {
  cwd: packageRoot,
  env,
  stdio: "inherit"
});

if (result.error) {
  console.error(`ovasabi: failed to launch Go CLI: ${result.error.message}`);
  console.error("ovasabi: install Go 1.26+ or use a prebuilt Ovasabi CLI release.");
  process.exit(1);
}

process.exit(result.status ?? 1);
