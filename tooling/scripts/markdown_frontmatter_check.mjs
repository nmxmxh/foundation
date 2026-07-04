#!/usr/bin/env node
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const target = path.resolve(process.argv[2] ?? ".");
const skipDirNames = new Set([".git", "node_modules", "target"]);
const skipRelPaths = new Set([".claude/worktrees"]);

const failures = [];

for (const file of walkMarkdown(target)) {
  checkFrontmatter(file);
}

if (failures.length > 0) {
  for (const failure of failures) {
    console.error(`[FAIL] ${failure.label}`);
    for (const detail of failure.details) {
      console.error(`  ${detail}`);
    }
  }
  console.error("markdown frontmatter check failed");
  process.exit(1);
}

console.log("markdown frontmatter check passed");

function* walkMarkdown(dir) {
  for (const entry of readdirSync(dir).sort()) {
    const full = path.join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      if (skipDirNames.has(entry) || skipRelPaths.has(rel(full))) {
        continue;
      }
      yield* walkMarkdown(full);
      continue;
    }
    if (entry.endsWith(".md")) {
      yield full;
    }
  }
}

function checkFrontmatter(file) {
  const relFile = rel(file);
  const content = readFileSync(file, "utf8").replace(/^\uFEFF/, "");
  const lines = content.split(/\r?\n/);
  if (lines[0]?.trimEnd() !== "---") {
    return;
  }

  let closing = -1;
  for (let i = 1; i < lines.length; i += 1) {
    const marker = lines[i].trimEnd();
    if (marker === "---" || marker === "...") {
      closing = i;
      break;
    }
  }
  if (closing === -1) {
    failures.push({
      label: "unterminated frontmatter block",
      details: [`${relFile}:1: opening --- has no closing --- line`],
    });
    return;
  }

  for (const error of validateYamlLines(lines.slice(1, closing))) {
    failures.push({
      label: "invalid YAML in frontmatter",
      details: [`${relFile}:${error.line + 2}: ${error.message}`],
    });
  }
}

// Line-oriented validation of the YAML mapping subset used in frontmatter:
// key/value pairs, nested mappings, lists, quoted strings, block scalars, and
// flow collections. Flags only high-confidence errors so exotic-but-valid
// YAML does not fail the check.
function validateYamlLines(lines) {
  const errors = [];
  let blockScalarIndent = -1;
  let lastKeyIndent = -1;

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    if (line.trim() === "") {
      continue;
    }

    const indent = line.length - line.trimStart().length;
    if (blockScalarIndent >= 0) {
      if (indent > blockScalarIndent) {
        continue;
      }
      blockScalarIndent = -1;
    }

    if (/^[ ]*\t/.test(line)) {
      errors.push({ line: i, message: "tab used for YAML indentation" });
      continue;
    }
    let rest = line.trimStart();
    if (rest.startsWith("#")) {
      continue;
    }

    let entryIndent = indent;
    while (rest === "-" || rest.startsWith("- ")) {
      if (rest === "-") {
        rest = "";
        break;
      }
      const trimmed = rest.slice(2).trimStart();
      entryIndent += rest.length - trimmed.length;
      rest = trimmed;
    }
    if (rest === "" || rest.startsWith("#")) {
      lastKeyIndent = entryIndent;
      continue;
    }

    const keyMatch = matchKey(rest);
    if (keyMatch === null) {
      if (lastKeyIndent >= 0 && entryIndent > lastKeyIndent) {
        continue; // multi-line plain scalar continuation
      }
      errors.push({
        line: i,
        message: `not a valid YAML mapping entry: ${rest}`,
      });
      continue;
    }

    lastKeyIndent = entryIndent;
    const error = validateScalar(keyMatch.value);
    if (error === "block") {
      blockScalarIndent = entryIndent;
      continue;
    }
    if (error) {
      errors.push({ line: i, message: `${error}: ${rest}` });
    }
  }

  return errors;
}

function matchKey(text) {
  let keyEnd = -1;
  if (text.startsWith('"') || text.startsWith("'")) {
    const close = findClosingQuote(text, 0);
    if (close === -1 || !text.slice(close + 1).trimStart().startsWith(":")) {
      return null;
    }
    keyEnd = text.indexOf(":", close);
  } else {
    for (let i = 0; i < text.length; i += 1) {
      if (text[i] !== ":") {
        continue;
      }
      const next = text[i + 1];
      if (next === undefined || next === " " || next === "\t") {
        keyEnd = i;
        break;
      }
    }
  }
  if (keyEnd === -1) {
    return null;
  }
  return { value: text.slice(keyEnd + 1).trim() };
}

function validateScalar(value) {
  if (value === "" || value.startsWith("#")) {
    return null;
  }
  if (/^[|>][0-9+-]{0,2}$/.test(value.split(/\s+#/, 1)[0].trim())) {
    return "block";
  }
  if (value.startsWith("|") || value.startsWith(">")) {
    return "invalid block scalar header";
  }
  if (value.startsWith('"') || value.startsWith("'")) {
    const close = findClosingQuote(value, 0);
    if (close === -1) {
      return "unterminated quoted value";
    }
    const trailer = value.slice(close + 1).trim();
    if (trailer !== "" && !trailer.startsWith("#")) {
      return "unexpected content after closing quote";
    }
    return null;
  }
  if (value.startsWith("[") || value.startsWith("{")) {
    return validateFlow(value);
  }
  if (/^[&*!]/.test(value)) {
    return null; // anchor, alias, or tag: assume valid
  }
  const plain = value.split(/\s+#/, 1)[0].trim();
  if (/:(\s|$)/.test(plain)) {
    return "unquoted colon in value (quote the string)";
  }
  return null;
}

function validateFlow(value) {
  let depth = 0;
  let quote = null;
  for (let i = 0; i < value.length; i += 1) {
    const ch = value[i];
    if (quote !== null) {
      if (ch === "\\" && quote === '"') {
        i += 1;
      } else if (ch === quote) {
        quote = null;
      }
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === "[" || ch === "{") {
      depth += 1;
    } else if (ch === "]" || ch === "}") {
      depth -= 1;
    }
  }
  if (depth !== 0 || quote !== null) {
    return "unterminated flow collection";
  }
  return null;
}

function findClosingQuote(text, start) {
  const quote = text[start];
  for (let i = start + 1; i < text.length; i += 1) {
    if (quote === '"' && text[i] === "\\") {
      i += 1;
      continue;
    }
    if (text[i] === quote) {
      if (quote === "'" && text[i + 1] === "'") {
        i += 1;
        continue;
      }
      return i;
    }
  }
  return -1;
}

function rel(file) {
  return path.relative(target, file).split(path.sep).join("/") || ".";
}
