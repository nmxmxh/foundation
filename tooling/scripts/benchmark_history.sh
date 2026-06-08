#!/bin/zsh
set -euo pipefail

target="${1:-.}"
out_dir="${BENCHMARK_HISTORY_DIR:-$target/benchmark-results}"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="$out_dir/foundation_bench_${timestamp}.log"
summary_file="$out_dir/foundation_bench_${timestamp}.tsv"

mkdir -p "$out_dir"

echo "benchmark log: ${log_file#$target/}"
echo "benchmark summary: ${summary_file#$target/}"

set +e
"$target/tooling/scripts/performance_check.sh" 2>&1 | tee "$log_file"
bench_status=${pipestatus[1]}
set -e

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
    NF >= 11 && $(NF-1) ~ /%$/ && $(NF) ~ /^[0-9]+$/ && $(NF-9) ~ /^[0-9][0-9,.]*$/ && $(NF-6) ~ /^[0-9.]+$/ {
      bench=""
      for (i=2; i<=NF-10; i++) {
        bench = bench (bench == "" ? "" : " ") $i
      }
      if (bench != "") {
        hz = $(NF-9)
        gsub(",", "", hz)
        ns = $(NF-6) * 1000000
        if (ns == 0 && hz > 0) {
          ns = 1000000000 / hz
        }
        printf "%s\t%.3f\t\t\tts\n", bench, ns
      }
    }
  ' "$log_file"
} >"$summary_file"

if [[ "$bench_status" -ne 0 ]]; then
  echo "benchmark run failed; partial log retained"
  exit "$bench_status"
fi

echo "benchmark history captured"
