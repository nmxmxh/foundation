#!/usr/bin/env node
// precompress-static writes brotli (.br) and gzip (.gz) copies of built frontend assets.
//
// nginx serves them through brotli_static and gzip_static, so a request costs a file
// read instead of an on-the-fly compression. Build time can afford the slow levels:
// brotli 11 is 11 percent smaller than nginx's on-the-fly brotli 6 on pronto_v1's
// dist (977 KB to 865 KB), and it costs about 5 s once per build.
// A copy is written only when it is smaller than the original; otherwise nginx
// falls back to the original or to on-the-fly compression.
//
// Usage: node precompress-static.mjs <dist-dir>
// It uses only Node's zlib, so the build needs no package install.

import { brotliCompressSync, constants, gzipSync } from "node:zlib";
import { readdirSync, readFileSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Matches nginx gzip_types and brotli_types in templates/config/nginx.conf.
const COMPRESSIBLE = new Set([
  ".html", ".js", ".mjs", ".css", ".json", ".map", ".svg", ".txt", ".xml",
  ".wasm", ".webmanifest", ".ico",
]);
const TEXT = new Set([".html", ".js", ".mjs", ".css", ".json", ".map", ".svg", ".txt", ".xml", ".webmanifest"]);
// nginx gzip_min_length and brotli_min_length are 256: smaller files are not worth a copy.
const MIN_BYTES = 256;
// Bounds keep a misconfigured dist directory from running unbounded (CP-02).
const MAX_FILES = 50_000;
const MAX_FILE_BYTES = 64 * 1024 * 1024;

/**
 * Writes .br and .gz copies for every compressible file under dir.
 * @param {string} dir
 * @returns {{ files: number, rawBytes: number, brBytes: number, gzBytes: number, skipped: number }}
 */
export function precompressDirectory(dir) {
  const summary = { files: 0, rawBytes: 0, brBytes: 0, gzBytes: 0, skipped: 0 };
  const stack = [resolve(dir)];
  let visited = 0;
  while (stack.length > 0) {
    const current = stack.pop();
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(path);
        continue;
      }
      if (!entry.isFile()) continue;
      visited += 1;
      if (visited > MAX_FILES) {
        throw new Error(`precompress-static: more than ${MAX_FILES} files under ${dir}`);
      }
      const ext = extname(entry.name).toLowerCase();
      if (!COMPRESSIBLE.has(ext)) continue;
      const stat = statSync(path);
      if (stat.size < MIN_BYTES || stat.size > MAX_FILE_BYTES) {
        summary.skipped += 1;
        continue;
      }
      const data = readFileSync(path);
      const br = brotliCompressSync(data, {
        params: {
          [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
          [constants.BROTLI_PARAM_MODE]: TEXT.has(ext) ? constants.BROTLI_MODE_TEXT : constants.BROTLI_MODE_GENERIC,
          [constants.BROTLI_PARAM_SIZE_HINT]: data.length,
        },
      });
      const gz = gzipSync(data, { level: constants.Z_BEST_COMPRESSION });
      summary.files += 1;
      summary.rawBytes += data.length;
      summary.brBytes += writeIfSmaller(`${path}.br`, br, data.length, stat);
      summary.gzBytes += writeIfSmaller(`${path}.gz`, gz, data.length, stat);
    }
  }
  return summary;
}

// writeIfSmaller writes the copy only when it saves bytes, with the source mtime so
// nginx sends the same Last-Modified for every encoding. It returns the bytes served.
function writeIfSmaller(path, bytes, rawLength, stat) {
  if (bytes.length >= rawLength) return rawLength;
  writeFileSync(path, bytes);
  utimesSync(path, stat.atime, stat.mtime);
  return bytes.length;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dir = process.argv[2];
  if (!dir) {
    console.error("usage: precompress-static.mjs <dist-dir>");
    process.exit(2);
  }
  let s;
  try {
    s = precompressDirectory(dir);
  } catch (error) {
    console.error(`precompress-static: cannot read ${dir}: ${error instanceof Error ? error.message : error}`);
    process.exit(1);
  }
  const pct = (part) => (s.rawBytes === 0 ? 0 : (100 * (1 - part / s.rawBytes)).toFixed(1));
  console.log(
    `precompress-static: ${s.files} files, ${s.rawBytes} B -> br ${s.brBytes} B (-${pct(s.brBytes)}%), gz ${s.gzBytes} B (-${pct(s.gzBytes)}%), ${s.skipped} skipped`
  );
}
