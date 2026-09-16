// Lists `${…}` interpolations at block position inside styled-components templates:
// where a declaration or rule starts, not inside a value. Under Linaria these must be
// rewritten (data-* rules, value-position functions, or plain string fragments).
//   node blockpos_scan.mjs <frontend/src>
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
const root = process.argv[2];
const files = execSync(`grep -rlE "from ['\\"]styled-components['\\"]" ${JSON.stringify(root)} --include='*.ts' --include='*.tsx'`, { encoding: "utf8" })
  .trim().split("\n").filter((f) => f && !/\.test\.tsx?$/.test(f));
const TAG = /(styled(\.\w+|\([^()]*(\([^()]*\))?[^()]*\))(\.attrs\([^)]*\))?(\s*<[^`]*>)?|css(<[^`]*>)?|createGlobalStyle(<[^`]*>)?|keyframes)\s*$/;
let total = 0;
for (const f of files) {
  const src = readFileSync(f, "utf8");
  const hits = [];
  // Walk template literals (with nesting) and inspect interpolations of tagged ones.
  const walk = (start) => {
    // start = index of opening backtick; returns index of closing backtick
    let i = start + 1;
    const tagged = TAG.test(src.slice(Math.max(0, start - 160), start));
    while (i < src.length) {
      const ch = src[i];
      if (ch === "\\") { i += 2; continue; }
      if (ch === "`") return i;
      if (ch === "$" && src[i + 1] === "{") {
        const before = src.slice(start + 1, i).replace(/\/\*[\s\S]*?\*\//g, "");
        const lastSig = before.replace(/\s+$/, "").slice(-1);
        let depth = 1, j = i + 2;
        while (j < src.length && depth) {
          const c = src[j];
          if (c === "`") { j = walk(j) + 1; continue; }
          if (c === "'" || c === '"') { let k = j + 1; while (k < src.length && src[k] !== c) { if (src[k] === "\\") k++; k++; } j = k + 1; continue; }
          if (c === "{") depth++; else if (c === "}") depth--;
          j++;
        }
        if (tagged && (lastSig === "" || lastSig === ";" || lastSig === "{" || lastSig === "}")) {
          const expr = src.slice(i + 2, j - 1).replace(/\s+/g, " ").trim();
          hits.push(`${src.slice(0, i).split("\n").length}: \${${expr.slice(0, 110)}${expr.length > 110 ? "…" : ""}}`);
        }
        i = j; continue;
      }
      i++;
    }
    return i;
  };
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === "/" && src[i + 1] === "/") { const n = src.indexOf("\n", i); i = n < 0 ? src.length : n; continue; }
    if (ch === "/" && src[i + 1] === "*") { const n = src.indexOf("*/", i + 2); i = n < 0 ? src.length : n + 2; continue; }
    if (ch === "'" || ch === '"') { let k = i + 1; while (k < src.length && src[k] !== ch) { if (src[k] === "\\") k++; k++; } i = k + 1; continue; }
    if (ch === "`") { i = walk(i) + 1; continue; }
    i++;
  }
  if (hits.length) { total += hits.length; console.log(`${f.replace(root + "/", "")}\n  ${hits.join("\n  ")}`); }
}
console.log(`\nblock-position interpolations: ${total}`);
