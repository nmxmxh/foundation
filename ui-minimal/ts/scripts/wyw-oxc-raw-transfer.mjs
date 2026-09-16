#!/usr/bin/env node
/**
 * Build patch: let a Linaria build opt out of oxc-parser raw transfer.
 *
 * ui-minimal's styles are extracted at build time by wyw-in-js, which parses
 * with oxc-parser. When the runtime supports it, wyw-in-js always enables oxc's
 * raw transfer, and raw transfer reserves ONE 6 GiB ArrayBuffer on the first
 * parse. That is virtual memory, but a Linux host with heuristic overcommit
 * (vm.overcommit_memory=0) refuses a single allocation larger than RAM + swap —
 * a 3 GiB, swapless build host fails with "Array buffer allocation failed"
 * before a single module is transformed. Neither package exposes a switch.
 *
 * This adds one: WYW_OXC_RAW_TRANSFER=0 makes wyw-in-js use oxc's JSON transfer,
 * which it already uses for mixed language/AST parses. Output is identical;
 * only parse speed differs. Unset, behaviour is unchanged.
 *
 * Idempotent and guarded on the exact line it replaces. A wyw-in-js whose shape
 * no longer matches is reported and left alone — the build still runs, it just
 * cannot opt out — so an upgrade surfaces here instead of silently.
 *
 *   node scripts/wyw-oxc-raw-transfer.mjs <frontend-root>
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const root = process.argv[2] ?? process.cwd();
const file = join(root, "node_modules/@wyw-in-js/transform/esm/utils/parseOxc.js");

const ORIGINAL = "const useRawTransfer = rawTransferSupported();";
const PATCHED =
  'const useRawTransfer = process.env.WYW_OXC_RAW_TRANSFER !== "0" && rawTransferSupported();';

if (!existsSync(file)) {
  console.log(`wyw-oxc-raw-transfer: skipped, ${file} not installed`);
  process.exit(0);
}
const source = readFileSync(file, "utf8");
if (source.includes(PATCHED)) {
  console.log("wyw-oxc-raw-transfer: already patched");
} else if (source.includes(ORIGINAL)) {
  writeFileSync(file, source.replace(ORIGINAL, PATCHED));
  console.log("wyw-oxc-raw-transfer: patched");
} else {
  console.warn(
    "wyw-oxc-raw-transfer: MANUAL — parseOxc.js no longer has the expected line; " +
      "WYW_OXC_RAW_TRANSFER has no effect until this patch is updated",
  );
}
