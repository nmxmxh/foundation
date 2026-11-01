import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { randomBytes } from "node:crypto";
import { brotliDecompressSync, gunzipSync } from "node:zlib";
import { afterEach, describe, expect, it } from "vitest";
// @ts-expect-error -- plain .mjs build script, typed only through this test
import { precompressDirectory } from "../scripts/precompress-static.mjs";

const script = fileURLToPath(new URL("../scripts/precompress-static.mjs", import.meta.url));
const dirs: string[] = [];

const makeDist = () => {
  const dir = mkdtempSync(join(tmpdir(), "precompress-"));
  dirs.push(dir);
  mkdirSync(join(dir, "assets"));
  const js = "export const rows = [" + Array.from({ length: 400 }, (_, i) => `{id:"rec_${i}",status:"available"}`).join(",") + "];\n";
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><head><title>t</title></head><body>" + "<p>shell</p>".repeat(200) + "</body></html>");
  writeFileSync(join(dir, "assets", "index.js"), js);
  writeFileSync(join(dir, "tiny.css"), "a{}");
  writeFileSync(join(dir, "logo.png"), Buffer.alloc(4096, 7));
  // Random bytes do not compress, so no copy is written for this file.
  writeFileSync(join(dir, "noise.json"), randomBytes(2048));
  utimesSync(join(dir, "assets", "index.js"), new Date(1_700_000_000_000), new Date(1_700_000_000_000));
  return dir;
};

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

describe("precompress-static", () => {
  it("writes smaller .br and .gz copies that decode to the original", () => {
    const dir = makeDist();
    const summary = precompressDirectory(dir);
    for (const file of ["index.html", join("assets", "index.js")]) {
      const raw = readFileSync(join(dir, file));
      const br = readFileSync(join(dir, `${file}.br`));
      const gz = readFileSync(join(dir, `${file}.gz`));
      expect(br.length).toBeLessThan(raw.length);
      expect(gz.length).toBeLessThan(raw.length);
      expect(brotliDecompressSync(br).equals(raw)).toBe(true);
      expect(gunzipSync(gz).equals(raw)).toBe(true);
    }
    expect(summary.files).toBeGreaterThanOrEqual(2);
    expect(summary.brBytes).toBeLessThan(summary.rawBytes);
  });

  it("skips small, binary, and incompressible files", () => {
    const dir = makeDist();
    const summary = precompressDirectory(dir);
    expect(existsSync(join(dir, "tiny.css.br"))).toBe(false);
    expect(existsSync(join(dir, "logo.png.br"))).toBe(false);
    expect(existsSync(join(dir, "noise.json.br"))).toBe(false);
    expect(summary.skipped).toBeGreaterThanOrEqual(1);
  });

  it("keeps the source mtime so every encoding has the same Last-Modified", () => {
    const dir = makeDist();
    precompressDirectory(dir);
    const source = statSync(join(dir, "assets", "index.js")).mtimeMs;
    expect(statSync(join(dir, "assets", "index.js.br")).mtimeMs).toBe(source);
    expect(statSync(join(dir, "assets", "index.js.gz")).mtimeMs).toBe(source);
  });

  it("is idempotent: a second run does not compress the copies", () => {
    const dir = makeDist();
    precompressDirectory(dir);
    precompressDirectory(dir);
    expect(existsSync(join(dir, "index.html.br.br"))).toBe(false);
    expect(existsSync(join(dir, "index.html.gz.gz"))).toBe(false);
  });

  it("runs as the Dockerfile CLI and rejects a missing argument", () => {
    const dir = makeDist();
    const out = execFileSync(process.execPath, [script, dir], { encoding: "utf8" });
    expect(out).toMatch(/^precompress-static: \d+ files/);
    expect(() => execFileSync(process.execPath, [script], { stdio: "pipe" })).toThrow();
    expect(() => execFileSync(process.execPath, [script, join(dir, "missing")], { stdio: "pipe" })).toThrow(/cannot read/);
  });
});
