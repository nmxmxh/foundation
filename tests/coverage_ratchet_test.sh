#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-coverage-ratchet.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/fixture/tooling" "$TMP/fixture/server-kit/go"
printf 'module example.com/fixture\n' > "$TMP/fixture/server-kit/go/go.mod"
cat > "$TMP/bin/go" <<'SH'
#!/bin/bash
cat "$COVERAGE_FIXTURE_OUTPUT"
if [[ "${COVERAGE_FIXTURE_FAIL:-0}" == 1 ]]; then
  echo 'fixture test failed' >&2
  exit 1
fi
SH
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH" COVERAGE_FIXTURE_OUTPUT="$TMP/output"
export FOUNDATION_GO_CACHE_DIR="$TMP/cache"
unset UPDATE HUMAN_SUPERVISED_CHECK_UPDATE
baseline="$TMP/fixture/tooling/coverage_baseline.psv"
check=(zsh "${COVERAGE_RATCHET_SCRIPT:-$ROOT/tooling/scripts/coverage_ratchet_check.sh}" "$TMP/fixture")

reset_fixture() {
  printf 'example.com/fixture/core|95.0|95.0\nexample.com/fixture/service|80.0|service\n' > "$baseline"
  printf 'ok example.com/fixture/core 0.1s coverage: 96.0%% of statements\n' > "$COVERAGE_FIXTURE_OUTPUT"
  cp "$baseline" "$TMP/expected"
}

reject() {
  local reason="$1"
  shift
  if "$@" > "$TMP/log" 2>&1; then
    echo "[FAIL] accepted $reason" >&2
    cat "$TMP/log" >&2
    exit 1
  fi
  cmp -s "$baseline" "$TMP/expected" || { echo '[FAIL] rewrote baseline after failure' >&2; exit 1; }
}

reset_fixture
reject 'missing recorded package' "${check[@]}"
reject 'incomplete baseline update' env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write
printf 'ok example.com/fixture/service 0.1s coverage: 81.0%% of statements\n' >> "$COVERAGE_FIXTURE_OUTPUT"
reject 'failed command with complete coverage' env COVERAGE_FIXTURE_FAIL=1 "${check[@]}"
grep -Fq 'fixture test failed' "$TMP/log"
reject 'failed command during update' env COVERAGE_FIXTURE_FAIL=1 HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write
"${check[@]}" > "$TMP/log" 2>&1 || { cat "$TMP/log" >&2; exit 1; }

printf 'ok example.com/fixture/core 0.1s coverage: 92.0%% of statements\nok example.com/fixture/service 0.1s coverage: 81.0%% of statements\n' > "$COVERAGE_FIXTURE_OUTPUT"
reject 'coverage regression' "${check[@]}"
reset_fixture
printf 'ok example.com/fixture/service 0.1s coverage: 81.0%% of statements\nok example.com/fixture/new 0.1s coverage: 90.0%% of statements\n' >> "$COVERAGE_FIXTURE_OUTPUT"
reject 'new package below target' "${check[@]}"

reset_fixture
printf 'ok example.com/fixture/service 0.1s coverage: 81.0%% of statements\n' >> "$COVERAGE_FIXTURE_OUTPUT"
env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write > "$TMP/log" 2>&1
grep -Fxq 'example.com/fixture/core|96.0|95.0' "$baseline"
grep -Fxq 'example.com/fixture/service|81.0|service' "$baseline"
printf 'ok example.com/fixture/core 0.1s coverage: 92.0%% of statements\nok example.com/fixture/service 0.1s coverage: 78.0%% of statements\n' > "$COVERAGE_FIXTURE_OUTPUT"
env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write > "$TMP/log" 2>&1
grep -Fxq 'example.com/fixture/core|96.0|95.0' "$baseline"
grep -Fxq 'example.com/fixture/service|81.0|service' "$baseline"

reset_fixture
rm "$TMP/fixture/server-kit/go/go.mod"
reject 'missing module' "${check[@]}"
echo 'coverage ratchet regression tests passed'
