#!/bin/zsh
# benchmark_ratchet_check.sh — enforce allocation and byte budgets as a ratchet.
#
# Policy (docs/performance_practices.md "Allocation discipline",
# docs/performance_lab.md "Measurement Lanes"):
#   * Hot paths declare an allocation budget; the budget is a contract.
#   * A benchmark may not allocate more than its recorded ceiling.
#   * Ceilings only ever FALL. Improving a path lowers its ceiling permanently.
#
# What is gated, and why:
#   allocs/op is an exact per-iteration count of allocation events. It is a
#   property of the code, not the machine: it does not move with CPU model,
#   thermal state, or CI neighbour noise. It is gated exactly, zero tolerance.
#
#   B/op is an average — total bytes divided by iterations — so it absorbs
#   amortized capacity growth and rounds. Measured jitter across consecutive
#   runs on identical hardware: 0 -> 2 B/op on a zero-alloc path, and
#   5675 -> 5690 B/op (0.26%) on an encode path. It is therefore gated with a
#   bounded tolerance, wide enough to swallow that jitter and far narrower than
#   any real regression, which moves bytes by tens to thousands.
#
#   ns/op is machine-dependent and is NOT gated. See NS_DRIFT_PCT below.
#   Timing claims belong in the benchmark ledger (docs/foundation_benchmarks.md)
#   under the variance rules in docs/performance_lab.md.
#
# This turns a stated practice into an enforced invariant. Before this check,
# allocation budgets existed in exactly two files as hardcoded magic numbers
# behind `//go:build perf`, and nothing ran them on the merge path.
#
# Baseline: tooling/benchmark_baseline.psv, "package|benchmark|max_allocs|max_bytes|ns_ref".
# Discovery: tooling/benchmark_ratchet_packages.tsv, "module_dir<TAB>package<TAB>bench_regex".
#
# Usage:
#   tooling/scripts/benchmark_ratchet_check.sh .
#   HUMAN_SUPERVISED_CHECK_UPDATE=1 UPDATE=1 tooling/scripts/benchmark_ratchet_check.sh . --write
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
mode="${2:-check}"

baseline="$target/tooling/benchmark_baseline.psv"
packages="$target/tooling/benchmark_ratchet_packages.tsv"

# 200x is enough for allocs/op and B/op to be exact (they are per-iteration
# counters, not timings) while keeping the whole gate well under a minute.
BENCHTIME="${BENCH_RATCHET_BENCHTIME:-200x}"
# B/op tolerance: the larger of a percentage and an absolute byte floor, so
# small ceilings (including 0) are not tripped by rounding.
BYTES_TOLERANCE_PCT="${BENCH_RATCHET_BYTES_TOLERANCE_PCT:-2}"
BYTES_TOLERANCE_ABS="${BENCH_RATCHET_BYTES_TOLERANCE_ABS:-8}"
# ns/op drift reporting is OFF by default (0 disables it), because at the
# benchtime this gate uses, ns/op is noise: consecutive runs on identical
# hardware were measured swinging by more than 2x on the same benchmark.
# Reporting that would train reviewers to ignore the check. Enable it only on
# quiesced hardware together with a longer BENCH_RATCHET_BENCHTIME, e.g.
#   BENCH_RATCHET_BENCHTIME=2s BENCH_RATCHET_NS_DRIFT_PCT=25 ...
# Authoritative timing evidence belongs to tooling/scripts/performance_check.sh
# and docs/foundation_benchmarks.md, under the variance rules in
# docs/performance_lab.md.
NS_DRIFT_PCT="${BENCH_RATCHET_NS_DRIFT_PCT:-0}"
GOCACHE="${FOUNDATION_GO_CACHE_DIR:-/tmp/ovasabi-foundation-go-build}"
export GOCACHE
mkdir -p "$GOCACHE"

failed=0
ok()   { echo "[OK] $1"; }
warn() { echo "[WARN] $1"; }
fail() {
  echo "[FAIL] $1"
  shift
  local d
  for d in "$@"; do [[ -n "$d" ]] && echo "  $d"; done
  failed=1
}

if [[ ! -f "$packages" ]]; then
  fail "benchmark ratchet package list missing" "${packages#$target/}"
  echo "benchmark ratchet check failed"
  exit 1
fi

# Measure every configured (module, package, regex) triple.
# Emits "package<TAB>benchmark<TAB>ns<TAB>bytes<TAB>allocs" lines.
measure() {
  local module_dir pkg regex
  while IFS=$'\t' read -r module_dir pkg regex; do
    [[ -z "$module_dir" || "$module_dir" == \#* ]] && continue
    [[ -f "$target/$module_dir/go.mod" ]] || continue
    ( cd "$target/$module_dir" && \
      go test "$pkg" -run='^$' -bench="$regex" -benchmem -benchtime="$BENCHTIME" -count=1 2>/dev/null ) | \
    awk -v pkg="$pkg" '
      /^Benchmark/ {
        name=$1
        sub(/-[0-9]+$/, "", name)     # strip the -GOMAXPROCS suffix
        ns=""; bytes=""; allocs=""
        for (i=2; i<=NF; i++) {
          if ($i == "ns/op")     ns=$(i-1)
          if ($i == "B/op")      bytes=$(i-1)
          if ($i == "allocs/op") allocs=$(i-1)
        }
        # Only rows carrying allocation data are gateable.
        if (name != "" && allocs != "" && bytes != "")
          printf "%s\t%s\t%s\t%s\t%s\n", pkg, name, ns, bytes, allocs
      }'
  done < "$packages"
}

typeset -A cur_allocs cur_bytes cur_ns
while IFS=$'\t' read -r pkg name ns bytes allocs; do
  [[ -n "$name" ]] || continue
  key="$pkg|$name"
  cur_allocs[$key]="$allocs"
  cur_bytes[$key]="$bytes"
  cur_ns[$key]="$ns"
done < <(measure)

if [[ "${#cur_allocs[@]}" -eq 0 ]]; then
  fail "benchmark measurement produced no rows" \
       "is the Go toolchain available, and do the configured regexes still match?"
  echo "benchmark ratchet check failed"
  exit 1
fi

# --write / UPDATE: rewrite the baseline, ratcheting each ceiling DOWN only.
if [[ "$mode" == "--write" || "${UPDATE:-0}" == "1" ]]; then
  if [[ "${HUMAN_SUPERVISED_CHECK_UPDATE:-0}" != "1" ]]; then
    echo "[FAIL] refusing to rewrite benchmark baseline without HUMAN_SUPERVISED_CHECK_UPDATE=1"
    echo "  Allocation budgets are part of the performance contract; ceilings only fall under supervision."
    exit 1
  fi
  typeset -A old_allocs old_bytes
  if [[ -f "$baseline" ]]; then
    while IFS='|' read -r pkg name a b n; do
      [[ -z "$pkg" || "$pkg" == \#* ]] && continue
      old_allocs["$pkg|$name"]="$a"
      old_bytes["$pkg|$name"]="$b"
    done < "$baseline"
  fi
  tmp="$baseline.tmp"
  {
    echo "# package|benchmark|max_allocs|max_bytes|ns_ref"
    echo "# Ceilings only fall. max_allocs and max_bytes are gated; ns_ref is a"
    echo "# machine-dependent reference value reported as drift, never gated."
    for key in ${(ok)cur_allocs}; do
      pkg="${key%%|*}"; name="${key#*|}"
      ca="${cur_allocs[$key]}"; cb="${cur_bytes[$key]}"
      pa="${old_allocs[$key]:-}"; pb="${old_bytes[$key]:-}"
      na="$ca"; nb="$cb"
      [[ -n "$pa" ]] && na="$(awk -v a="$pa" -v b="$ca" 'BEGIN{print (a<b?a:b)}')"
      [[ -n "$pb" ]] && nb="$(awk -v a="$pb" -v b="$cb" 'BEGIN{print (a<b?a:b)}')"
      echo "$pkg|$name|$na|$nb|${cur_ns[$key]}"
    done
  } > "$tmp"
  mv "$tmp" "$baseline"
  echo "benchmark baseline updated: ${baseline#$target/} (${#cur_allocs[@]} benchmarks)"
  exit 0
fi

if [[ ! -f "$baseline" ]]; then
  fail "benchmark baseline missing" "${baseline#$target/}" \
       "Generate under supervision: HUMAN_SUPERVISED_CHECK_UPDATE=1 UPDATE=1 tooling/scripts/benchmark_ratchet_check.sh . --write"
  echo "benchmark ratchet check failed"
  exit 1
fi

typeset -A seen
gated=0
drifted=0
while IFS='|' read -r pkg name max_allocs max_bytes ns_ref; do
  [[ -z "$pkg" || "$pkg" == \#* ]] && continue
  key="$pkg|$name"
  seen[$key]=1
  ca="${cur_allocs[$key]:-}"
  if [[ -z "$ca" ]]; then
    warn "in baseline but not measured: $name (renamed, deleted, or regex no longer matches?)"
    continue
  fi
  cb="${cur_bytes[$key]}"
  gated=$((gated + 1))

  # Allocation ceiling — deterministic, hard gate.
  if awk -v c="$ca" -v m="$max_allocs" 'BEGIN{exit !(c > m)}'; then
    fail "allocation ceiling exceeded: $name" \
         "ceiling ${max_allocs} allocs/op  measured ${ca} allocs/op" \
         "Either restore the allocation shape or justify a new ceiling under docs/performance_lab.md."
    continue
  fi
  # Byte ceiling — gated with a bounded tolerance (see header).
  if awk -v c="$cb" -v m="$max_bytes" -v p="$BYTES_TOLERANCE_PCT" -v a="$BYTES_TOLERANCE_ABS" \
       'BEGIN{pct=m*(1+p/100); abs=m+a; lim=(pct>abs?pct:abs); exit !(c > lim)}'; then
    fail "byte ceiling exceeded: $name" \
         "ceiling ${max_bytes} B/op  measured ${cb} B/op  (tolerance ${BYTES_TOLERANCE_PCT}% or ${BYTES_TOLERANCE_ABS} B, whichever is larger)" \
         "Either restore the allocation shape or justify a new ceiling under docs/performance_lab.md."
    continue
  fi

  # Improvement pressure: report when a path now beats its recorded ceiling, so
  # the ratchet gets tightened deliberately rather than drifting back up later.
  if awk -v c="$ca" -v m="$max_allocs" 'BEGIN{exit !(c < m)}'; then
    ok "$name ${ca} allocs/op ${cb} B/op ✓ under ceiling ${max_allocs} — ratchet with UPDATE=1"
  else
    ok "$name ${ca} allocs/op ${cb} B/op ✓ at ceiling"
  fi

  # ns/op drift: informational only, opt-in. Machine-dependent by nature.
  if awk -v p="$NS_DRIFT_PCT" 'BEGIN{exit !(p > 0)}' && \
     [[ -n "$ns_ref" ]] && awk -v r="$ns_ref" 'BEGIN{exit !(r > 0)}'; then
    if awk -v c="${cur_ns[$key]}" -v r="$ns_ref" -v p="$NS_DRIFT_PCT" \
         'BEGIN{exit !(c > r * (1 + p/100))}'; then
      drifted=$((drifted + 1))
      warn "  ns/op drift: $name ${cur_ns[$key]} vs reference ${ns_ref} (>${NS_DRIFT_PCT}%, not gated — confirm on comparable hardware)"
    fi
  fi
done < "$baseline"

# Benchmarks that exist but carry no recorded ceiling are ungated.
ungated=0
for key in ${(ok)cur_allocs}; do
  [[ -n "${seen[$key]:-}" ]] && continue
  ungated=$((ungated + 1))
  warn "ungated benchmark: ${key#*|} (${cur_allocs[$key]} allocs/op) — record it with UPDATE=1 to gate it"
done

echo "benchmark ratchet: ${gated} gated, ${ungated} ungated, ${drifted} with ns/op drift"

if [[ "$failed" -ne 0 ]]; then
  echo "benchmark ratchet check failed"
  exit 1
fi
echo "benchmark ratchet check passed"
