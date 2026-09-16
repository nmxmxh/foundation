// Template literals containing a blank line, outside CSS templates, in files wyw transforms.
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
const apps = process.argv.slice(2);
for (const app of apps) {
  const files = execSync(`find ${JSON.stringify(app + "/frontend/src")} -type f \\( -name '*.ts' -o -name '*.tsx' \\) -not -name '*.test.*' -not -name '__style_*' -not -path '*/generated/*' -not -path '*/types/protos/*'`, { encoding: "utf8" }).trim().split("\n").filter(Boolean);
  const hits = [];
  for (const f of files) {
    const src = readFileSync(f, "utf8");
    let i = 0;
    while ((i = src.indexOf("`", i)) >= 0) {
      // find closing backtick, skipping ${...} (balanced) and escapes
      let j = i + 1, depth = 0;
      for (; j < src.length; j++) {
        const ch = src[j];
        if (ch === "\\") { j++; continue; }
        if (depth === 0 && ch === "`") break;
        if (ch === "$" && src[j + 1] === "{") { depth++; j++; continue; }
        if (depth > 0 && ch === "}") depth--;
      }
      const body = src.slice(i + 1, j);
      const before = src.slice(Math.max(0, i - 40), i);
      const isCss = /(styled(\.\w+|\([^)]*\))(<[^>]*>)?|css|keyframes|createGlobalStyle|injectGlobal)\s*$/.test(before);
      if (!isCss && /\n[ \t]*\n/.test(body)) hits.push(`${f.replace(app + "/frontend/", "")}:${src.slice(0, i).split("\n").length}`);
      i = j + 1;
    }
  }
  console.log(`${app}: ${hits.length}${hits.length ? "\n  " + hits.join("\n  ") : ""}`);
}
