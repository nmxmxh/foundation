#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
THRESHOLD="${COVERAGE_THRESHOLD:-90.0}"
OUT_DIR="${COVERAGE_DIR:-$ROOT_DIR/test-results/coverage}"
mkdir -p "$OUT_DIR"

modules=(
  "server-kit/go"
  "runtime-sdk/go"
  "runtime-transport/go"
  "config-contracts/go"
)

total_failed=0

run_module() {
  local module="$1"
  local name="${module//\//_}"
  local raw_profile="$OUT_DIR/$name.raw.out"
  local owned_profile="$OUT_DIR/$name.owned.out"
  local summary="$OUT_DIR/$name.summary.txt"

  echo "== coverage: $module =="
  (
    cd "$ROOT_DIR/$module"
    go test ./... -covermode=atomic -coverprofile="$raw_profile"
  )

  awk '
    NR == 1 { print; next }
    $0 !~ /\/generated\// &&
    $0 !~ /\/testprotos\// &&
    $0 !~ /\.pb\.go:/ {
      print
    }
  ' "$raw_profile" > "$owned_profile"

  (
    cd "$ROOT_DIR/$module"
    go tool cover -func="$owned_profile"
  ) | tee "$summary"

  local coverage
  coverage="$(awk '/^total:/ { gsub("%", "", $3); print $3 }' "$summary")"
  awk -v coverage="$coverage" -v threshold="$THRESHOLD" -v module="$module" '
    BEGIN {
      if (coverage + 0 < threshold + 0) {
        printf("coverage gate failed for %s: %.1f%% < %.1f%%\n", module, coverage, threshold) > "/dev/stderr"
        exit 1
      }
    }
  ' || total_failed=1
}

for module in "${modules[@]}"; do
  run_module "$module"
done

exit "$total_failed"
