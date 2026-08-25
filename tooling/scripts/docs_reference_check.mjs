#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const target = path.resolve(process.argv[2] ?? ".");
const markdownRoots = [
  "docs",
  "tooling/docs",
  "README.md",
  "AGENTS.md",
  "CLAUDE.md",
];

const failures = [];

const ATTRIBUTION = /^[ \t]*(?:[-*][ \t]*|\d+\.[ \t]*)?(Sources?|References?|Origin|Adapted from|Derived from|Extracted from|Imported from)[ \t]*:[ \t]*(\S.*)$/;

// Foundation's `docs/` is copied wholesale into generated projects as
// `docs/foundation/`, so links inside that subtree must not escape it or they
// dangle once re-rooted. In the foundation repo the subtree is `docs/`; in a
// generated project it is `docs/foundation/`. Project-authored docs sitting
// alongside it are never relocated and may link freely into the project.
const docsRoot = path.join(target, "docs");
const vendoredDocsRoot = path.join(docsRoot, "foundation");
const relocatableRoot = existsSync(vendoredDocsRoot) ? vendoredDocsRoot : docsRoot;

for (const root of markdownRoots) {
  const full = path.join(target, root);
  if (!existsSync(full)) {
    continue;
  }
  if (statSync(full).isDirectory()) {
    for (const file of walkMarkdown(full)) {
      checkMarkdownFile(file);
    }
    continue;
  }
  if (full.endsWith(".md")) {
    checkMarkdownFile(full);
  }
}

if (failures.length > 0) {
  for (const failure of failures) {
    console.error(`[FAIL] ${failure.label}`);
    for (const detail of failure.details) {
      console.error(`  ${detail}`);
    }
  }
  console.error("docs reference check failed");
  process.exit(1);
}

console.log("docs reference check passed");

function* walkMarkdown(dir) {
  for (const entry of readdirSync(dir).sort()) {
    const full = path.join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      yield* walkMarkdown(full);
      continue;
    }
    if (entry.endsWith(".md")) {
      yield full;
    }
  }
}

function checkMarkdownFile(file) {
  const enforceContainment = !escapesRoot(file, relocatableRoot);
  const relFile = rel(file);
  const content = stripFencedBlocks(readFileSync(file, "utf8"));
  checkSourceAttributions(file, relFile, content);
  const linkPattern = /!?\[[^\]\n]*\]\(([^)\n]+)\)/g;
  let match;
  while ((match = linkPattern.exec(content)) !== null) {
    const rawTarget = normalizeLinkTarget(match[1]);
    if (!rawTarget || rawTarget.startsWith("#")) {
      continue;
    }
    if (/^file:/i.test(rawTarget)) {
      failures.push({
        label: "file URL is not portable documentation",
        details: [`${relFile}: ${rawTarget}`, "use a relative Markdown link instead"],
      });
      continue;
    }
    if (isExternal(rawTarget)) {
      continue;
    }

    const withoutFragment = rawTarget.split("#", 1)[0].split("?", 1)[0];
    if (!withoutFragment) {
      continue;
    }

    let decoded;
    try {
      decoded = decodeURI(withoutFragment);
    } catch {
      failures.push({
        label: "invalid URI escape in documentation link",
        details: [`${relFile}: ${rawTarget}`],
      });
      continue;
    }

    const resolved = decoded.startsWith("/")
      ? path.join(target, decoded.slice(1))
      : path.resolve(path.dirname(file), decoded);
    if (!existsSync(resolved)) {
      failures.push({
        label: "broken local documentation link",
        details: [`${relFile}: ${rawTarget}`, `missing: ${rel(resolved)}`],
      });
      continue;
    }
    if (enforceContainment && escapesRoot(resolved, relocatableRoot)) {
      failures.push({
        label: "documentation link escapes the relocatable docs tree",
        details: [
          `${relFile}: ${rawTarget}`,
          `resolves outside: ${rel(relocatableRoot)}`,
          "this subtree is re-rooted into generated projects; the link breaks there",
          "reference the path as inline code instead of a Markdown link",
        ],
      });
    }
  }
}

// A path cited in prose is invisible to the link check above, so a source that
// exists only on the author's machine — a sibling directory one level above the
// repository, say — resolves for them and for nobody else. The gap is narrow
// but load-bearing: attribution lines are exactly where such a path gets
// written, and exactly where nobody notices it is unverified.
//
// Scoped to attribution lines rather than all prose. Measured against
// Foundation's own docs, requiring every inline-code path to resolve produced
// 234 hits, nearly all legitimate (paths relative to the citing document, and
// paths that exist only in a generated project). Attribution lines produced one
// hit, and it was a real broken reference. Precision is the point.

function checkSourceAttributions(file, relFile, content) {
  const lines = content.split("\n");
  for (let index = 0; index < lines.length; index += 1) {
    const match = ATTRIBUTION.exec(lines[index]);
    if (!match) {
      continue;
    }
    // Markdown links are already verified by the link pass; drop them and look
    // at what is left.
    const remainder = match[2].replace(/!?\[[^\]\n]*\]\([^)\n]+\)/g, " ");
    for (const token of pathShapedTokens(remainder)) {
      if (resolvesFrom(file, token)) {
        continue;
      }
      failures.push({
        label: "source attribution cites an unresolvable path",
        details: [
          `${relFile}:${index + 1}: ${token}`,
          "resolves from neither the citing document nor the repository root",
          "a bare path in prose is not checked by the link pass, so it keeps resolving on the author's machine and nowhere else",
          "cite it as a Markdown link, or correct the path",
        ],
      });
    }
  }
}

function pathShapedTokens(value) {
  const tokens = [];
  for (const raw of value.split(/[\s,;]+/)) {
    const token = raw.replace(/[`'"]/g, "").replace(/[.,;:)\]]+$/, "");
    if (!token.includes("/") || token.includes("//")) {
      continue;
    }
    if (isExternal(token) || /^(and|or)\//i.test(token)) {
      continue;
    }
    // A placeholder segment is a shape, not a location.
    if (token.includes("<") || token.includes("*")) {
      continue;
    }
    tokens.push(token);
  }
  return tokens;
}

function resolvesFrom(file, token) {
  const bare = token.split("#", 1)[0].split("?", 1)[0];
  if (!bare) {
    return true;
  }
  const candidates = bare.startsWith("/")
    ? [path.join(target, bare.slice(1))]
    : [path.resolve(path.dirname(file), bare), path.resolve(target, bare)];
  return candidates.some((candidate) => existsSync(candidate));
}

function escapesRoot(resolved, root) {
  const relative = path.relative(root, resolved);
  return relative.startsWith("..") || path.isAbsolute(relative);
}

function normalizeLinkTarget(value) {
  let targetValue = value.trim();
  if (targetValue.startsWith("<") && targetValue.includes(">")) {
    targetValue = targetValue.slice(1, targetValue.indexOf(">"));
  } else {
    targetValue = targetValue.split(/\s+/, 1)[0];
  }
  return targetValue.trim();
}

function isExternal(value) {
  return /^(https?:|mailto:|tel:|app:|skill:|mcp:)/i.test(value);
}

function stripFencedBlocks(value) {
  return value.replace(/```[\s\S]*?```/g, "");
}

function rel(file) {
  return path.relative(target, file).split(path.sep).join("/") || ".";
}
