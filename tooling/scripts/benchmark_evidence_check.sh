#!/bin/bash
# Benchmark evidence check.
#
# docs/foundation_benchmarks.md is the evidence ledger the architecture ladder
# (CP-07/CP-07b/CP-36) leans on: it names benchmark functions, source paths,
# and benchmark-results artifacts across every lane (transport frames, gRPC,
# workers, runtime buffer, envelopes, Hermes, frontend workbench, native Rust).
# Prose drifts silently when code moves; this check fails when the ledger
# references a benchmark, file, or artifact that no longer exists.
#
# Foundation-core only: generated projects receive the doc as reference under
# docs/foundation/ but do not carry benchmark-results/ or the full bench trees.
set -euo pipefail

target="${1:-.}"
target="${target%/}"
doc="$target/docs/foundation_benchmarks.md"
failed=0

if [[ ! -f "$doc" || ! -d "$target/benchmark-results" ]]; then
  echo "[OK] benchmark evidence (foundation-core layout not detected; skipped)"
  exit 0
fi

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local detail
  for detail in "$@"; do
    [[ -n "$detail" ]] && echo "  $detail"
  done
  failed=1
}

tmp_defined="$(mktemp "${TMPDIR:-/tmp}/foundation-bench-defined.XXXXXX")"
trap 'rm -f "$tmp_defined"' EXIT

# All benchmark functions defined in the repo's test trees. rg respects
# .gitignore, so generated/vendored trees stay out.
rg -o --no-filename 'func (Benchmark[A-Za-z0-9_]+)' -r '$1' \
  --glob '*_test.go' "$target" 2>/dev/null | sort -u >"$tmp_defined" || true

if [[ ! -s "$tmp_defined" ]]; then
  fail "benchmark definitions discoverable" "no Go benchmark functions found under $target"
fi

# --- 1. Benchmark names referenced in the doc must exist -------------------
unresolved_benchmarks=""
while IFS= read -r name; do
  case "$name" in
    Benchmark|Benchmarks|Benchmarking) continue ;;
  esac
  # Exact match, then family/prefix mention (e.g. BenchmarkEnvelope_,
  # BenchmarkHermes), then non-Go bench sources (Vitest/Rust bench names).
  if grep -qx "$name" "$tmp_defined"; then
    continue
  fi
  if grep -q "^$name" "$tmp_defined"; then
    continue
  fi
  if rg -q -F "$name" "$target" \
    --glob '!docs/**' --glob '!benchmark-results/**' --glob '!*.md' 2>/dev/null; then
    continue
  fi
  unresolved_benchmarks+="$name"$'\n'
done < <(grep -oE 'Benchmark[A-Za-z0-9_]+' "$doc" | sort -u)

if [[ -n "$unresolved_benchmarks" ]]; then
  fail "benchmarks referenced in the ledger exist" \
    "no benchmark function or bench source matches (rename in code, or fix the doc):" \
    "$(printf '%s' "$unresolved_benchmarks" | sed 's/^/    /')"
else
  ok "benchmarks referenced in the ledger exist"
fi

# --- 2. benchmark-results artifacts referenced in the doc must exist -------
missing_artifacts=""
while IFS= read -r artifact; do
  if [[ "$artifact" == *_ ]]; then
    # Trailing underscore is a family/prefix mention (e.g. load_research_).
    if ! compgen -G "$target/${artifact}*" >/dev/null; then
      missing_artifacts+="$artifact* (no file with this prefix)"$'\n'
    fi
  elif [[ ! -f "$target/$artifact" ]]; then
    missing_artifacts+="$artifact"$'\n'
  fi
done < <(grep -oE 'benchmark-results/[A-Za-z0-9_.-]+' "$doc" | sort -u)

if [[ -n "$missing_artifacts" ]]; then
  fail "benchmark artifacts referenced in the ledger exist" \
    "restore the artifact or mark the doc row as pruned:" \
    "$(printf '%s' "$missing_artifacts" | sed 's/^/    /')"
else
  ok "benchmark artifacts referenced in the ledger exist"
fi

# --- 3. Repo paths referenced in the doc must exist ------------------------
resolve_path() {
  local ref="$1"
  ref="${ref#foundation/}"
  [[ -e "$target/$ref" ]] && return 0
  # Symbol suffix mentions like server-kit/go/bulk.Pipeline: strip from the
  # first dot after the last slash and retry.
  local dir="${ref%/*}"
  local base="${ref##*/}"
  if [[ "$base" == *.* && -e "$target/$dir/${base%%.*}" ]]; then
    return 0
  fi
  # Generator output paths (e.g. tests/contract/generated_lifecycle_test.go)
  # exist only in generated projects; the evidence is the generator or template
  # that declares them.
  if rg -q -F "$ref" "$target/tooling/scripts" "$target/templates" 2>/dev/null; then
    return 0
  fi
  return 1
}

missing_paths=""
while IFS= read -r ref; do
  case "$ref" in
    server-kit/*|runtime-transport/*|runtime-sdk/*|runtime-native/*|frontend-kit/*|ui-minimal/*|config-contracts/*|tooling/*|docs/*|tests/*|templates/*|cmd/*|scripts/*|api/*|foundation/*) ;;
    *) continue ;;
  esac
  resolve_path "$ref" || missing_paths+="$ref"$'\n'
done < <(grep -oE '`[A-Za-z0-9_./-]+`' "$doc" | tr -d '`' | grep '/' | grep -vE '^Benchmark|^benchmark-results/' | sort -u)

if [[ -n "$missing_paths" ]]; then
  fail "source paths referenced in the ledger exist" \
    "path moved or was retired; update the doc entry:" \
    "$(printf '%s' "$missing_paths" | sed 's/^/    /')"
else
  ok "source paths referenced in the ledger exist"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "benchmark evidence check failed"
  exit 1
fi

echo "benchmark evidence check passed"
