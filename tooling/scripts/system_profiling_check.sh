#!/bin/zsh
# system_profiling_check.sh — enforce the system-profiling lane.
#
# docs/system_profiling_practices.md turns the mechanics of perf into four
# rules that a repository can actually hold:
#
#   PERFLAB-05  A stripped binary profiles as a column of hex addresses. Every
#               site that drops the symbol table must be registered with the
#               path back to symbols for the same build SHA.
#   PERFLAB-06  Sampling cost is set by -F and capture size by duration. An
#               unpinned or unbounded capture in a checked-in script or runbook
#               is an unbounded cost someone will pay in production.
#   PERFLAB-07  strace costs about 10x; perf trace is the production-safe
#               syscall lane. Reaching for strace requires saying so.
#   PERFLAB-08  perf.data, folded stacks, and flamegraphs are captures, not
#               source. They must be ignored, never committed.
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() { echo "[OK] $1"; }
fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

# Foundation scans itself; a scaffolded project vendors this under
# scripts/checks/ and scans its own tree from the same relative roots.
scan_roots=()
for root in templates tooling/scripts .github Makefile scripts docs; do
  [[ -e "$target/$root" ]] && scan_roots+=("$target/$root")
done
if (( ${#scan_roots[@]} == 0 )); then
  ok "system profiling check: no scannable roots"
  exit 0
fi

# ---------------------------------------------------------------- PERFLAB-05
sites_file="$target/tooling/profiling_symbol_sites.tsv"
[[ -f "$sites_file" ]] || sites_file="$target/tooling/foundation/profiling_symbol_sites.tsv"

# A generated project inherits this check but not Foundation's registry. There
# a missing registry is a gap to report, not a broken build: the finding is
# reversible (add the file), so it warns rather than failing. Inside Foundation
# the registry exists and the rule has teeth.
registry_present=1
if [[ ! -f "$sites_file" ]]; then
  registry_present=0
fi

if (( registry_present == 0 )); then
  advisory=()
  while IFS= read -r hit; do
    [[ -n "$hit" ]] || continue
    advisory+=("${hit#$target/}")
  done < <(grep -rIl -- '-ldflags="-s -w' "${scan_roots[@]}" 2>/dev/null \
    | grep -v '/node_modules/' | grep -v 'system_profiling' | sort -u)
  if (( ${#advisory[@]} > 0 )); then
    echo "[NOTE] PERFLAB-05: symbol-stripping build sites with no registry"
    for site in "${advisory[@]}"; do echo "  $site"; done
    echo "  add tooling/profiling_symbol_sites.tsv (see docs/foundation/system_profiling_practices.md)"
  else
    ok "PERFLAB-05 no symbol-stripping build sites"
  fi
else
  typeset -A registered
  # NOTE: `path` is tied to PATH in zsh. Never name a loop variable `path`
  # here — assigning it silently empties the command search path.
  while IFS=$'\t' read -r site_path artifact symbolization; do
    [[ -z "${site_path:-}" || "$site_path" == \#* ]] && continue
    # zsh takes a quoted associative subscript literally in an assignment,
    # storing the quotes as part of the key. Leave it unquoted.
    registered[$site_path]=1
    if [[ ! -e "$target/$site_path" ]]; then
      fail "PERFLAB-05 registered site exists" "stale row: $site_path"
    fi
    if [[ -z "${symbolization:-}" ]]; then
      fail "PERFLAB-05 symbolization path" "$site_path has no symbolization column"
    fi
  done <"$sites_file"

  stripping_sites=()
  while IFS= read -r hit; do
    [[ -n "$hit" ]] || continue
    rel="${hit#$target/}"
    stripping_sites+=("$rel")
  done < <(grep -rIl -- '-ldflags="-s -w' "${scan_roots[@]}" 2>/dev/null \
    | grep -v '/node_modules/' | grep -v 'system_profiling' | sort -u)

  unregistered=()
  for site in "${stripping_sites[@]}"; do
    [[ -n "${registered[$site]:-}" ]] || unregistered+=("$site")
  done
  if (( ${#unregistered[@]} > 0 )); then
    fail "PERFLAB-05 stripping sites registered" \
      "these builds drop the symbol table with no registered symbolization path:" \
      "${unregistered[@]}" \
      "add a row to tooling/profiling_symbol_sites.tsv naming how a perf capture against this artifact gets symbolized"
  else
    ok "PERFLAB-05 stripping sites registered (${#stripping_sites[@]} site(s))"
  fi
fi

# ---------------------------------------------------------------- PERFLAB-06
# Only real command lines: a command starts the line (optionally under sudo or
# a shell prompt). Prose and table cells that merely name `perf record` do not
# run anything and are not the check's business.
unpinned=()
unbounded=()
while IFS= read -r hit; do
  [[ -n "$hit" ]] || continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  lineno="${rest%%:*}"
  text="${rest#*:}"
  case "$text" in
    *" -F "*|*" -F"[0-9]*) ;;
    *) unpinned+=("${file#$target/}:$lineno: $text") ;;
  esac
done < <(grep -rInE '^[[:space:]]*(\$ )?(sudo )?perf (record|top)\b' "${scan_roots[@]}" 2>/dev/null \
  | grep -v '/node_modules/' | sort)

while IFS= read -r hit; do
  [[ -n "$hit" ]] || continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  lineno="${rest%%:*}"
  text="${rest#*:}"
  case "$text" in
    *sleep*|*timeout*) ;;
    *) unbounded+=("${file#$target/}:$lineno: $text") ;;
  esac
done < <(grep -rInE '^[[:space:]]*(\$ )?(sudo )?perf (record|stat|trace)\b.*[[:space:]]-a([[:space:]]|$)' "${scan_roots[@]}" 2>/dev/null \
  | grep -v '/node_modules/' | sort)

if (( ${#unpinned[@]} > 0 )); then
  fail "PERFLAB-06 sample frequency pinned" \
    "perf record/top without -F samples at the default rate, which is a cost nobody chose:" \
    "${unpinned[@]}"
else
  ok "PERFLAB-06 sample frequency pinned"
fi

if (( ${#unbounded[@]} > 0 )); then
  fail "PERFLAB-06 system-wide capture bounded" \
    "a system-wide (-a) capture with no duration runs until someone notices:" \
    "${unbounded[@]}" \
    "bound it with a trailing 'sleep N' or 'timeout N'"
else
  ok "PERFLAB-06 system-wide capture bounded"
fi

# ---------------------------------------------------------------- PERFLAB-07
strace_files=()
while IFS= read -r hit; do
  [[ -n "$hit" ]] || continue
  rel="${hit#$target/}"
  grep -q 'profiling-overhead-ack' "$hit" && continue
  strace_files+=("$rel")
done < <(grep -rIlE '(^|[^[:alnum:]_])strace([^[:alnum:]_]|$)' "${scan_roots[@]}" 2>/dev/null \
  | grep -v '/node_modules/' | grep -v 'system_profiling' | sort -u)

if (( ${#strace_files[@]} > 0 )); then
  fail "PERFLAB-07 observer overhead acknowledged" \
    "strace costs roughly 10x; perf trace answers the same question in production:" \
    "${strace_files[@]}" \
    "use perf trace, or add a 'profiling-overhead-ack:' comment stating the measured overhead and why strace is required"
else
  ok "PERFLAB-07 observer overhead acknowledged"
fi

# ---------------------------------------------------------------- PERFLAB-08
gitignore="$target/.gitignore"
missing_patterns=()
for pattern in 'perf.data' '*.folded' '*.pprof'; do
  if [[ -f "$gitignore" ]] && grep -Fq -- "$pattern" "$gitignore"; then
    continue
  fi
  missing_patterns+=("$pattern")
done
if (( ${#missing_patterns[@]} > 0 )); then
  fail "PERFLAB-08 profile artifacts ignored" \
    "missing .gitignore patterns:" "${missing_patterns[@]}"
else
  ok "PERFLAB-08 profile artifacts ignored"
fi

tracked_captures=()
while IFS= read -r tracked; do
  [[ -n "$tracked" ]] && tracked_captures+=("$tracked")
done < <(cd "$target" && git ls-files -- 'perf.data*' '*.folded' '*.pprof' 2>/dev/null || true)
if (( ${#tracked_captures[@]} > 0 )); then
  fail "PERFLAB-08 no committed captures" "${tracked_captures[@]}"
else
  ok "PERFLAB-08 no committed captures"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "system profiling check failed"
  exit 1
fi

echo "system profiling check passed"
