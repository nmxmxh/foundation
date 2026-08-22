#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${TMPDIR:-/tmp}/ovasabi-lifecycle-contract-generator"
OUT_FILE="$OUT_DIR/generated_lifecycle_test.go"

mkdir -p "$OUT_DIR"

node "$FOUNDATION_DIR/tooling/scripts/generate_lifecycle_contract_tests.mjs" \
  --proto-root "$FOUNDATION_DIR/templates/api/protos" \
  --out "$OUT_FILE" \
  --include-template >/tmp/ovasabi-lifecycle-generator.out

node "$FOUNDATION_DIR/tooling/scripts/generate_lifecycle_contract_tests.mjs" \
  --proto-root "$FOUNDATION_DIR/templates/api/protos" \
  --out "$OUT_FILE" \
  --include-template \
  --check >/tmp/ovasabi-lifecycle-generator-check.out

for expected in \
  "example:create_example:v1:requested" \
  "example:create_example:v1:success" \
  "example:create_example:v1:failed" \
  "example:update_example:v1:requested" \
  "example:delete_example:v1:success" \
  "verifyGeneratedLifecycleObservation" \
  "VerifyCommandLifecycle"; do
  if ! rg -n "$expected" "$OUT_FILE" >/dev/null; then
    cat "$OUT_FILE" >&2
    echo "missing generated lifecycle contract: $expected" >&2
    exit 1
  fi
done

echo "lifecycle contract generator test passed"

# Vocabulary sync guard: the mutating/read-only action sets are duplicated
# between the two lifecycle generators (contract tests + manifest). A word
# added to one and not the other makes generated tests treat an event as
# mutating while the manifest calls it read-only. This failed silently once
# (2026-08-22: 16 actions drifted); this check makes it a lint failure.
node - "$FOUNDATION_DIR/tooling/scripts" << 'NODE'
import fs from "node:fs";
import path from "node:path";

const dir = process.argv[2];
const grab = (file, decl) => {
  const src = fs.readFileSync(path.join(dir, file), "utf8");
  const match = src.match(new RegExp(decl + "[^\\[]*\\[([\\s\\S]*?)\\]"));
  if (!match) throw new Error(`cannot locate ${decl} in ${file}`);
  return new Set([...match[1].matchAll(/"([a-z]+)"/g)].map((m) => m[1]));
};

const testActions = grab("generate_lifecycle_contract_tests.mjs", "mutatingActions");
const manifestActions = grab("generate_lifecycle_manifest.mjs", "MUTATING_ACTIONS");
const testRead = grab("generate_lifecycle_contract_tests.mjs", "readOnlyActions");
const manifestRead = grab("generate_lifecycle_manifest.mjs", "READ_ONLY_ACTIONS");

const drift = (name, a, b) => {
  const missing = [...a].filter((word) => !b.has(word));
  const extra = [...b].filter((word) => !a.has(word));
  for (const word of missing) throw new Error(`${name}: "${word}" present in contract-tests generator, missing in manifest generator`);
  for (const word of extra) throw new Error(`${name}: "${word}" present in manifest generator, missing in contract-tests generator`);
};
drift("mutating", testActions, manifestActions);
drift("read-only", testRead, manifestRead);
console.log("[OK] lifecycle action vocabularies in sync (" + testActions.size + " mutating / " + testRead.size + " read-only)");
NODE
