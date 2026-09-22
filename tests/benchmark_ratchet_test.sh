#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-ratchet.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/fixture/tooling" "$TMP/fixture/module"
touch "$TMP/fixture/module/go.mod"
printf 'module\t./sample\t^Benchmark\n' > "$TMP/fixture/tooling/benchmark_ratchet_packages.tsv"
cat > "$TMP/bin/go" <<'SH'
#!/bin/bash
cat "$RATCHET_FIXTURE_OUTPUT"
if [[ "${RATCHET_FIXTURE_FAIL:-0}" == 1 ]]; then
  echo 'fixture compilation failed' >&2
  exit 2
fi
SH
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH" RATCHET_FIXTURE_OUTPUT="$TMP/output"
export FOUNDATION_GO_CACHE_DIR="$TMP/cache"
unset UPDATE HUMAN_SUPERVISED_CHECK_UPDATE
baseline="$TMP/fixture/tooling/benchmark_baseline.psv"

reset_fixture() {
  printf './sample|BenchmarkPresent|1|16|10\n./sample|BenchmarkMissing|0|0|5\n' > "$baseline"
  printf 'BenchmarkPresent-8 200 10 ns/op 16 B/op 1 allocs/op\n' > "$RATCHET_FIXTURE_OUTPUT"
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

check=(zsh "$ROOT/tooling/scripts/benchmark_ratchet_check.sh" "$TMP/fixture")
reset_fixture
reject 'missing recorded benchmark' "${check[@]}"
reject 'incomplete baseline update' env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write

printf 'BenchmarkMissing-8 200 5 ns/op 0 B/op 0 allocs/op\n' >> "$RATCHET_FIXTURE_OUTPUT"
reject 'failed command with complete output' env RATCHET_FIXTURE_FAIL=1 "${check[@]}"
grep -Fq 'fixture compilation failed' "$TMP/log"
reject 'failed command during update' env RATCHET_FIXTURE_FAIL=1 HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write
"${check[@]}" > "$TMP/log" 2>&1 || { cat "$TMP/log" >&2; exit 1; }

printf 'BenchmarkPresent-8 200 10 ns/op 32 B/op 2 allocs/op\nBenchmarkMissing-8 200 5 ns/op 0 B/op 0 allocs/op\n' > "$RATCHET_FIXTURE_OUTPUT"
reject 'allocation regression' "${check[@]}"

printf 'BenchmarkPresent-8 200 10 ns/op 32 B/op 1 allocs/op\nBenchmarkMissing-8 200 5 ns/op 0 B/op 0 allocs/op\n' > "$RATCHET_FIXTURE_OUTPUT"
reject 'byte regression' "${check[@]}"

reset_fixture
printf 'BenchmarkMissing-8 200 5 ns/op 0 B/op 0 allocs/op\n' >> "$RATCHET_FIXTURE_OUTPUT"
printf 'missing\t./other\t^Benchmark\n' >> "$TMP/fixture/tooling/benchmark_ratchet_packages.tsv"
reject 'missing configured module' "${check[@]}"
printf 'module\t./sample\t^Benchmark\n' > "$TMP/fixture/tooling/benchmark_ratchet_packages.tsv"

printf 'BenchmarkPresent-8 200 8 ns/op 8 B/op 0 allocs/op\nBenchmarkMissing-8 200 4 ns/op 0 B/op 0 allocs/op\n' > "$RATCHET_FIXTURE_OUTPUT"
env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write > "$TMP/log" 2>&1
grep -Fxq './sample|BenchmarkPresent|0|8|8' "$baseline"
grep -Fxq './sample|BenchmarkMissing|0|0|4' "$baseline"

printf 'BenchmarkPresent-8 200 20 ns/op 32 B/op 2 allocs/op\nBenchmarkMissing-8 200 8 ns/op 8 B/op 1 allocs/op\n' > "$RATCHET_FIXTURE_OUTPUT"
env HUMAN_SUPERVISED_CHECK_UPDATE=1 "${check[@]}" --write > "$TMP/log" 2>&1
grep -Fxq './sample|BenchmarkPresent|0|8|20' "$baseline"
grep -Fxq './sample|BenchmarkMissing|0|0|8' "$baseline"
echo 'benchmark ratchet regression tests passed'
