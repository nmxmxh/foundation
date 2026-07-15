#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d /tmp/foundation-core-validation.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "core validation contract failed: $1" >&2
  exit 1
}

workflow="$ROOT/.github/workflows/core-ci.yml"
[[ -f "$workflow" ]] || fail "Core CI workflow is missing"
for contract in \
  'FOUNDATION_REQUIRE_TS_DEPS: "1"' \
  'GOVULNCHECK_STRICT: "1"' \
  'RUST_RUNTIME_LOOM: "1"' \
  'make install-ts-deps' \
  'make audit-ts-deps' \
  'make verify'; do
  grep -Fq -- "$contract" "$workflow" || fail "Core CI is missing: $contract"
done

mkdir -p "$TMP/ts/runtime-transport/ts"
printf '%s\n' '{"name":"fixture","sideEffects":false}' >"$TMP/ts/runtime-transport/ts/package.json"
printf '%s\n' '{}' >"$TMP/ts/runtime-transport/ts/package-lock.json"
CI=0 FOUNDATION_REQUIRE_TS_DEPS=0 "$ROOT/tooling/scripts/ts_static_analysis_check.sh" "$TMP/ts" >/dev/null
if CI=0 FOUNDATION_REQUIRE_TS_DEPS=1 "$ROOT/tooling/scripts/ts_static_analysis_check.sh" "$TMP/ts" >/dev/null 2>&1; then
  fail "strict TypeScript analysis accepted missing dependencies"
fi

mkdir -p "$TMP/vitest"
CI=0 FOUNDATION_REQUIRE_TS_DEPS=0 "$ROOT/tooling/scripts/run_vitest.sh" "$TMP/vitest" run >/dev/null
if CI=true FOUNDATION_REQUIRE_TS_DEPS=0 "$ROOT/tooling/scripts/run_vitest.sh" "$TMP/vitest" run >/dev/null 2>&1; then
  fail "CI TypeScript tests accepted missing dependencies"
fi

mkdir -p \
  "$TMP/go/app" \
  "$TMP/go/.claude/worktrees/foreign" \
  "$TMP/go/.codex/worktrees/foreign" \
  "$TMP/go/.worktrees/foreign"
touch \
  "$TMP/go/app/go.mod" \
  "$TMP/go/.claude/worktrees/foreign/go.mod" \
  "$TMP/go/.codex/worktrees/foreign/go.mod" \
  "$TMP/go/.worktrees/foreign/go.mod"
modules="$(GO_ANALYSIS_DISCOVERY_ONLY=1 "$ROOT/tooling/scripts/go_static_analysis_check.sh" "$TMP/go")"
printf '%s\n' "$modules" | grep -Fxq 'app' || fail "primary Go module was not discovered"
if printf '%s\n' "$modules" | grep -Fq 'worktrees'; then
  fail "nested worktree leaked into Go analysis discovery"
fi

echo "Foundation Core validation contract passed"
