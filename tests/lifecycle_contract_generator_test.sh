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
  "example:create:v1:requested" \
  "example:create:v1:success" \
  "example:create:v1:failed" \
  "example:update:v1:requested" \
  "example:delete:v1:success" \
  "verifyGeneratedLifecycleObservation" \
  "VerifyCommandLifecycle"; do
  if ! rg -n "$expected" "$OUT_FILE" >/dev/null; then
    cat "$OUT_FILE" >&2
    echo "missing generated lifecycle contract: $expected" >&2
    exit 1
  fi
done

echo "lifecycle contract generator test passed"
