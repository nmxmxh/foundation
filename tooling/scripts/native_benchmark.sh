#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "${1:-.}" && pwd)"
if [[ -d "$PROJECT_ROOT/foundation" ]]; then
  FOUNDATION_ROOT="$PROJECT_ROOT/foundation"
else
  FOUNDATION_ROOT="$PROJECT_ROOT"
fi

echo "== foundation native benchmark suite =="
echo "project root: $PROJECT_ROOT"
echo "foundation root: $FOUNDATION_ROOT"

if [[ "${FOUNDATION_NATIVE_SKIP_BASELINE:-0}" != "1" && -x "$FOUNDATION_ROOT/tooling/scripts/performance_check.sh" ]]; then
  echo "== baseline foundation performance ladder =="
  "$FOUNDATION_ROOT/tooling/scripts/performance_check.sh"
else
  echo "skip baseline foundation performance ladder"
fi

if command -v cargo >/dev/null 2>&1 && [[ -f "$FOUNDATION_ROOT/runtime-native/rust/Cargo.toml" ]]; then
  echo "== runtime-native Rust report-only benchmarks =="
  cargo run --manifest-path "$FOUNDATION_ROOT/runtime-native/rust/Cargo.toml" --release --bin native_bench
  echo "== runtime-native communication flow simulation =="
  cargo run --manifest-path "$FOUNDATION_ROOT/runtime-native/rust/Cargo.toml" --release --bin native_flow_sim
else
  echo "skip runtime-native Rust benchmarks: cargo or runtime-native crate missing"
fi

if [[ -x "$FOUNDATION_ROOT/runtime-native/ts/node_modules/vitest/dist/cli.js" || -f "$FOUNDATION_ROOT/runtime-native/ts/node_modules/vitest/dist/cli.js" ]]; then
  echo "== runtime-native TypeScript benchmarks =="
  (
    cd "$FOUNDATION_ROOT/runtime-native/ts"
    npm run bench -- --run
  )
else
  echo "skip runtime-native TypeScript benchmarks: node_modules not installed"
fi

if [[ -f "$PROJECT_ROOT/native/package.json" ]]; then
  echo "== native shell diagnostics =="
  (
    cd "$PROJECT_ROOT/native"
    npm run doctor || true
  )
else
  echo "skip native shell diagnostics: native/package.json not present"
fi
