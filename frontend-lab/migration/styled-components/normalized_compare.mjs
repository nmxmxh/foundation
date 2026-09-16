// Re-compare cascaded snapshots with the migration's key-shape changes normalised:
// doubled classes (`&&` → .SCn.SCn), attribute qualifiers the migration added to an
// ancestor compound ([data-emphasis=…]), and whitespace around `/` in values.
import { readFileSync } from "node:fs";
const [dir, attrs] = process.argv.slice(2);
const before = JSON.parse(readFileSync(`${dir}/before.cascaded.json`, "utf8")).app;
const after = JSON.parse(readFileSync(`${dir}/after.cascaded.json`, "utf8")).app;
const attrRe = new RegExp(`\\[data-(${attrs})(="[^"]*")?\\]`, "g");
const notRe = new RegExp(`:not\\(\\[data-(${attrs})(="[^"]*")?\\]\\)`, "g");
const normKey = (k) => k.replace(/\.(SC\d+)(\.\1)+/g, ".$1").replace(notRe, "").replace(attrRe, "").replace(/:not\(\)/g, "");
// Whitespace-insensitive: the harness normalises away the space after `)` before it
// substitutes an element's variable, which re-glues tokens (`var(--x) infinite` → `0sinfinite`).
const normVal = (v) => (v ?? "").replace(/\s+/g, "").replace(/['"]/g, "");
let identical = 0; const remaining = [];
for (const [name, b] of Object.entries(before)) {
  const a = after[name];
  if (!a || "error" in a || "error" in b) { remaining.push(`${name}: error/missing`); continue; }
  const fa = {}; for (const [k, v] of Object.entries(a.flat)) { const nk = normKey(k); fa[nk] = v; }
  const fb = {}; for (const [k, v] of Object.entries(b.flat)) fb[normKey(k)] = v;
  const keys = new Set([...Object.keys(fb), ...Object.keys(fa)]);
  const bad = [...keys].filter((k) => normVal(fb[k]) !== normVal(fa[k]));
  const problems = [];
  if (bad.length) problems.push(bad.map((k) => `${k}: ${fb[k]} -> ${fa[k]}`).join(" | "));
  // Inlining repeats a keyframes block in every template that animates with it, so
  // compare the set, not the list.
  const kf = (x) => JSON.stringify([...new Set(x.keyframes)].sort());
  if (kf(b) !== kf(a)) problems.push("keyframes differ");
  if (b.html !== a.html) problems.push("html differs");
  if (problems.length) remaining.push(`${name}: ${problems.join("; ")}`); else identical++;
}
console.log(`normalised: identical ${identical}/${Object.keys(before).length}`);
console.log(remaining.join("\n"));
