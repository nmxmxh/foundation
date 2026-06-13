#!/bin/zsh
set -euo pipefail

# check_lifecycle_manifest.sh
# CI wrapper for generate_lifecycle_manifest.mjs --check
# Verifies that docs/references/lifecycle/lifecycle_contract.json and
# lifecycle_contract_guide.md are present and match what would be generated
# from the current proto definitions.
#
# Usage: tooling/scripts/check_lifecycle_manifest.sh [target-dir]

target="${1:-.}"
if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi
target="${target%/}"

failed=0
ok()   { echo "[OK] $1"; }
fail() { echo "[FAIL] $1"; shift; for d in "$@"; do [[ -n "$d" ]] && echo "  $d"; done; failed=1; }

first_existing_file() {
  local path
  for path in "$@"; do
    if [[ -f "$path" ]]; then
      echo "$path"
      return 0
    fi
  done
  return 1
}

generator="$(first_existing_file \
  "$target/tooling/scripts/generate_lifecycle_manifest.mjs" \
  "$target/scripts/checks/generate_lifecycle_manifest.mjs" \
  "$target/foundation/tooling/scripts/generate_lifecycle_manifest.mjs" || true)"

if [[ -z "${generator:-}" ]]; then
  fail "lifecycle manifest generator missing" \
    "expected one of:" \
    "$target/tooling/scripts/generate_lifecycle_manifest.mjs" \
    "$target/scripts/checks/generate_lifecycle_manifest.mjs" \
    "$target/foundation/tooling/scripts/generate_lifecycle_manifest.mjs"
  echo "lifecycle manifest check failed"
  exit 1
fi

# Determine proto root
proto_root="$target/api/protos"
if [[ ! -d "$proto_root" ]]; then
  proto_root="$target/templates/api/protos"
fi

# Determine output paths. Prefer app-owned lifecycle artifacts when present;
# otherwise validate the copied Foundation reference under docs/foundation for
# scaffolded projects.
manifest_out="$target/docs/references/lifecycle/lifecycle_contract.json"
guide_out="$target/docs/references/lifecycle/lifecycle_contract_guide.md"

if [[ ! -d "$target/docs/references/lifecycle" && -d "$target/docs/foundation/references/lifecycle" ]]; then
  manifest_out="$target/docs/foundation/references/lifecycle/lifecycle_contract.json"
  guide_out="$target/docs/foundation/references/lifecycle/lifecycle_contract_guide.md"
fi

# Run the generator in check mode
if node "$generator" \
    --proto-root "$proto_root" \
    --manifest-out "$manifest_out" \
    --guide-out "$guide_out" \
    --check 2>&1; then
  ok "lifecycle manifest and guide are current"
else
  fail "lifecycle manifest or guide is stale or missing" \
    "run: make lifecycle-manifest" \
    "manifest: $manifest_out" \
    "guide: $guide_out"
  failed=1
fi

# Validate manifest JSON is valid
if [[ -f "$manifest_out" ]]; then
  if node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" "$manifest_out" 2>/dev/null; then
    ok "lifecycle manifest is valid JSON"
  else
    fail "lifecycle manifest contains invalid JSON" "file: $manifest_out"
  fi
fi

# Validate manifest schema field
if [[ -f "$manifest_out" ]]; then
  if grep -q '"foundation/lifecycle-contract/v1"' "$manifest_out"; then
    ok "lifecycle manifest schema version present"
  else
    fail "lifecycle manifest missing schema version" \
      'expected: "$schema": "foundation/lifecycle-contract/v1"' \
      "file: $manifest_out"
  fi
fi

if [[ -f "$manifest_out" ]]; then
  if node - "$manifest_out" <<'NODE'
const fs = require("fs");
const manifest = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const requiredTop = ["$schema", "schema_version", "generated_by", "doc", "source", "invariants", "review_vector_ids", "contracts"];
for (const field of requiredTop) {
  if (!(field in manifest)) {
    throw new Error(`missing top-level field: ${field}`);
  }
}
if (manifest.$schema !== "foundation/lifecycle-contract/v1") {
  throw new Error(`unexpected schema: ${manifest.$schema}`);
}
if (manifest.schema_version !== 1) {
  throw new Error(`unexpected schema_version: ${manifest.schema_version}`);
}
const requiredVectors = new Set(manifest.review_vector_ids);
const seen = new Set();
for (const contract of manifest.contracts) {
  if (seen.has(contract.id)) {
    throw new Error(`duplicate contract: ${contract.id}`);
  }
  seen.add(contract.id);
  for (const state of ["requested", "success", "failed"]) {
    const expected = `${contract.domain}:${contract.action}:${contract.version}:${state}`;
    if (!contract.events || contract.events[state] !== expected) {
      throw new Error(`${contract.id} invalid ${state} event`);
    }
  }
  const contractVectors = new Set((contract.review_vectors || []).map((item) => item.id));
  for (const vector of requiredVectors) {
    if (!contractVectors.has(vector)) {
      throw new Error(`${contract.id} missing review vector ${vector}`);
    }
  }
}
NODE
  then
    ok "lifecycle manifest structure is valid"
  else
    fail "lifecycle manifest structure is invalid" "file: $manifest_out"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "lifecycle manifest check failed"
  exit 1
fi

echo "lifecycle manifest check passed"
