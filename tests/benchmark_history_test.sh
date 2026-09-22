#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-history.XXXXXX")"
TMP="$(cd "$TMP" && pwd)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/fixture/tooling/scripts"
cat > "$TMP/fixture/tooling/scripts/performance_check.sh" <<'SH'
#!/bin/bash
[[ "$PROFILE_DIR" == /* ]] || exit 9
cat <<'LOG'
BenchmarkCodec-8 200 12.5 ns/op 16 B/op 1 allocs/op
native buffer roundtrip 34 ns/op
   name                 hz     min     max    mean     p75     p99    p995    p999     rme   samples
   · legacy row  20,000,000.00  0.0000  0.0100  0.0001  0.0000  0.0000  0.0000  0.0001  ±0.50%  1000
   current fastest row  25,000,000.00  0.0000  0.0100  0.0000  0.0000  0.0000  0.0000  0.0001  ±0.50%  1000   fastest
   current plain row  10,000,000.00  0.0000  0.0100  0.0001  0.0000  0.0000  0.0000  0.0001  ±0.50%  1000
   current slowest row  5,000,000.00  0.0000  0.0100  0.0002  0.0000  0.0000  0.0000  0.0001  ±0.50%  1000   slowest
skip service-backed Redis/Postgres benchmarks: no fixture services
LOG
printf 'PROFILE\tretained-heap\t128\tbytes\n'
exit "${HISTORY_FIXTURE_STATUS:-0}"
SH
chmod +x "$TMP/fixture/tooling/scripts/performance_check.sh"
export BENCHMARK_HISTORY_DIR="$TMP/results"
zsh "$ROOT/tooling/scripts/benchmark_history.sh" "$TMP/fixture" > "$TMP/log" 2>&1
summary=("$TMP"/results/*.tsv)
printf 'current fastest row\t40.000\t\t\tts-throughput-estimate\n' > "$TMP/expected"
grep -Fxf "$TMP/expected" "${summary[0]}" >/dev/null || { echo '[FAIL] lost Vitest 5 fastest row' >&2; exit 1; }
printf 'legacy row\t100.000\t\t\tts\ncurrent plain row\t100.000\t\t\tts\ncurrent slowest row\t200.000\t\t\tts\n' > "$TMP/expected"
[[ "$(grep -Fxf "$TMP/expected" "${summary[0]}" | wc -l | tr -d ' ')" == 3 ]]
[[ "$(wc -l < "${summary[0]}" | tr -d ' ')" == 8 ]]
grep -Fq 'skip service-backed' "$TMP"/results/*.log
grep -Fq 'exit_status=0' "$TMP"/results/*.meta.txt

(
  cd "$TMP"
  BENCHMARK_HISTORY_DIR=relative-results zsh "$ROOT/tooling/scripts/benchmark_history.sh" "$TMP/fixture" > "$TMP/log" 2>&1
)
grep -Fq "profile_dir=$TMP/relative-results/" "$TMP"/relative-results/*.meta.txt

export BENCHMARK_HISTORY_DIR="$TMP/failed-results"
set +e
HISTORY_FIXTURE_STATUS=7 zsh "$ROOT/tooling/scripts/benchmark_history.sh" "$TMP/fixture" > "$TMP/log" 2>&1
result=$?
set -e
[[ "$result" == 7 ]]
grep -Fq 'exit_status=7' "$TMP"/failed-results/*.meta.txt
[[ -s "$TMP"/failed-results/*.tsv ]]
echo 'benchmark history regression tests passed'
