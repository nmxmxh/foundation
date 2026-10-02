#!/usr/bin/env node
// Test file naming contract.
//
// A test file belongs to exactly one source file, and its name says which:
// foo.go is tested by foo_test.go. Fragmenting one source's coverage across
// several ad-hoc files is what this refuses. pool_coverage2_test.go was the
// symptom: three files for one package, two of them numbered, none naming the
// source they exercised.
//
// A large test file is fine and is not what this check is about. Crowding a
// directory with fragments is.
//
// Allowed without a baseline entry:
//   - <source>_test.go, where <source>.go sits in the same directory
//   - bench_test.go, and any *_bench_test.go (the Go convention for benchmarks)
//   - any file in a directory that holds no non-test Go file, such as a
//     benchmarks-only package, because there is nothing for it to pair with
//   - a file carrying a build constraint, paired with its complement
//     (race_enabled/race_disabled, *_unix/*_windows, *_darwin/*_linux)
//   - *_test.go under templates/, which scaffold a project rather than ship Go
//
// Everything else must appear in tooling/test_naming_baseline.tsv. The baseline
// is the migration path: it makes today's exceptions visible and blocks new
// ones without forcing a single unreviewed mass rename.

const fs = require("node:fs");
const path = require("node:path");

const root = process.argv[2] || ".";
const baselinePath = path.join(root, "tooling/test_naming_baseline.tsv");

const SKIP_DIRS = new Set([
  "node_modules", ".git", "target", ".cache", ".gocache", "dist", "build",
  "coverage", "vendor", ".next", ".turbo",
]);

// A numeric suffix is never conventional. It marks a second file created for a
// reason the name does not state, which is the exact failure this forbids.
const NUMERIC_SUFFIX = /_\d+$/;

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      walk(path.join(dir, entry.name), out);
    } else if (entry.name.endsWith("_test.go")) {
      out.push(path.join(dir, entry.name));
    }
  }
  return out;
}

function hasBuildConstraint(file) {
  let head = "";
  try {
    head = fs.readFileSync(file, "utf8").slice(0, 512);
  } catch {
    return false;
  }
  return head.includes("//go:build") || head.includes("// +build");
}

function loadBaseline() {
  if (!fs.existsSync(baselinePath)) return new Set();
  const allowed = new Set();
  for (const line of fs.readFileSync(baselinePath, "utf8").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    allowed.add(trimmed.split("\t")[0]);
  }
  return allowed;
}

const files = walk(root);
const baseline = loadBaseline();
const failures = [];
const grandfathered = [];
const ok = { paired: 0, bench: 0, buildTag: 0, noSources: 0 };

// A directory with no non-test Go file has nothing to pair with. appbench is
// such a package: it holds only benchmarks and a probe against a running
// system, so there is no production file whose name a test could carry. The
// convention is about pairing, and there is nothing to pair to.
function dirHasSources(dir) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return true; // unreadable: let the naming rules judge it
  }
  return entries.some((e) => e.isFile() && e.name.endsWith(".go") && !e.name.endsWith("_test.go"));
}

for (const file of files) {
  const rel = path.relative(root, file);
  const base = rel.split("/").pop().replace(/_test\.go$/, "");

  if (NUMERIC_SUFFIX.test(base)) {
    failures.push(`${rel}: numeric suffix on a test file name`);
    continue;
  }

  if (fs.existsSync(file.replace(/_test\.go$/, ".go"))) {
    ok.paired++;
    continue;
  }

  if (!dirHasSources(path.dirname(file))) {
    ok.noSources++;
    continue;
  }

  if (base === "bench" || base.endsWith("_bench")) {
    ok.bench++;
    continue;
  }

  if (hasBuildConstraint(file)) {
    ok.buildTag++;
    continue;
  }

  if (rel.startsWith("templates/")) {
    ok.paired++;
    continue;
  }

  if (baseline.has(rel)) {
    grandfathered.push(rel);
    continue;
  }

  failures.push(`${rel}: no ${base}.go beside it, and not a benchmark, build-constrained file, or baselined exception`);
}

// A baseline entry that no longer names a real violation is stale and should be
// removed, so the file cannot silently become a permanent dumping ground.
const stale = [...baseline].filter(
  (rel) => !grandfathered.includes(rel) && !fs.existsSync(path.join(root, rel)),
);
for (const rel of stale) {
  failures.push(`tooling/test_naming_baseline.tsv: ${rel} no longer exists; drop the entry`);
}

if (failures.length) {
  for (const f of failures) console.error(`[FAIL] ${f}`);
  console.error(`test naming check failed (${failures.length} problem(s))`);
  process.exit(1);
}

console.log(
  `test naming check passed (${ok.paired} paired, ${ok.bench} benchmark, ` +
    `${ok.buildTag} build-constrained, ${ok.noSources} in source-less packages, ` +
    `${grandfathered.length} baselined of ${files.length} files)`,
);
