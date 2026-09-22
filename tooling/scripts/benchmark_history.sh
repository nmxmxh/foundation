#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
out_dir="${BENCHMARK_HISTORY_DIR:-$target/benchmark-results}"
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="$out_dir/foundation_bench_${timestamp}.log"
summary_file="$out_dir/foundation_bench_${timestamp}.tsv"
metadata_file="$out_dir/foundation_bench_${timestamp}.meta.txt"
export PROFILE_DIR="${PROFILE_DIR:-$out_dir/foundation_bench_${timestamp}_profiles}"

echo "benchmark log: ${log_file#$target/}"
echo "benchmark summary: ${summary_file#$target/}"
{
  printf 'schema=foundation.benchmark_history.v1\ncaptured_at=%s\n' "$timestamp"
  printf 'revision=%s\n' "$(git -C "$target" rev-parse HEAD 2>/dev/null || echo unknown)"
  if [[ -n "$(git -C "$target" status --porcelain 2>/dev/null)" ]]; then
    echo 'working_tree=dirty'
  else
    echo 'working_tree=clean-or-unavailable'
  fi
  printf 'machine=%s\n' "$(uname -srm)"
  printf 'go=%s\n' "$(go version 2>/dev/null || echo unavailable)"
  printf 'node=%s\n' "$(node --version 2>/dev/null || echo unavailable)"
  printf 'rustc=%s\n' "$(rustc --version 2>/dev/null || echo unavailable)"
  printf 'scale_benchtime=%s\nlatency_benchtime=%s\n' "${SCALE_BENCHTIME:-100x}" "${LATENCY_BENCHTIME:-1s}"
  printf 'profile=%s\ntrace=%s\nprofile_dir=%s\n' "${PROFILE:-0}" "${TRACE:-0}" "$PROFILE_DIR"
  echo 'summary_scope=Go, native buffer, Vitest tables, frontend profile; other results remain in the raw log'
  echo 'ts_precision=displayed mean; zero means use reciprocal throughput and source ts-throughput-estimate'
} > "$metadata_file"

set +e
"$target/tooling/scripts/performance_check.sh" 2>&1 | tee "$log_file"
run_status=("${pipestatus[@]}")
set -e
bench_status=${run_status[1]}
[[ "$bench_status" -ne 0 ]] || bench_status=${run_status[2]}

{
  echo "# benchmark	ns_per_op	bytes_per_op	allocs_per_op	source"
  awk '
    /^Benchmark/ {
      bench=$1
      ns=""
      bytes=""
      allocs=""
      for (i=2; i<=NF; i++) {
        if ($(i+1) == "ns/op") ns=$i
        if ($(i+1) == "B/op") bytes=$i
        if ($(i+1) == "allocs/op") allocs=$i
      }
      if (ns != "") {
        printf "%s\t%s\t%s\t%s\tgo\n", bench, ns, bytes, allocs
      }
    }
    /^native / && $(NF) == "ns/op" {
      bench=""
      for (i=1; i<=NF-2; i++) {
        bench = bench (bench == "" ? "" : " ") $i
      }
      printf "%s\t%s\t\t\trust\n", bench, $(NF-1)
    }
    /^PROFILE\t/ {
      printf "%s\t\t%s\t\tfrontend-profile\n", $2, $3
    }
    {
      # Vitest 4 prefixes rows with a bullet; Vitest 5 omits it.
      # Either version can append a fastest/slowest label.
      end=NF
      if ($end == "fastest" || $end == "slowest") end--
      if (end < 11 || $(end-1) !~ /%$/ || $end !~ /^[0-9]+$/ || $(end-9) !~ /^[0-9][0-9,.]*$/) next
      start=1
      if ($1 == "·" || $1 == "✓" || $1 == "✔") start=2
      bench=""
      for (i=start; i<=end-10; i++) {
        bench = bench (bench == "" ? "" : " ") $i
      }
      if (bench != "") {
        hz = $(end-9)
        gsub(",", "", hz)
        ns = $(end-6) * 1000000
        source = "ts"
        # Reciprocal mean throughput is an estimate, not arithmetic mean latency.
        if (ns == 0 && hz > 0) {
          ns = 1000000000 / hz
          source = "ts-throughput-estimate"
        }
        if (ns > 0) printf "%s\t%.3f\t\t\t%s\n", bench, ns, source
      }
    }
  ' "$log_file"
} >"$summary_file"
printf 'exit_status=%s\n' "$bench_status" >> "$metadata_file"

if [[ "$bench_status" -ne 0 ]]; then
  echo "benchmark run failed; partial log retained"
  exit "$bench_status"
fi

echo "benchmark history captured"
