#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

first_existing() {
  local path
  for path in "$@"; do
    if [[ -e "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

check_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

docs_dir="$(first_existing "$target/docs/foundation" "$target/docs" || true)"
if [[ -z "${docs_dir:-}" ]]; then
  fail "formal methods docs directory exists" "expected docs/ or docs/foundation/"
else
  tla_doc="$docs_dir/tla_architecture_practices.md"
  check_exists "TLA architecture practices doc" "$tla_doc"
  check_contains "formal doc names PlusCal" "$tla_doc" "PlusCal"
  check_contains "formal doc names Alloy" "$tla_doc" "Alloy"
  check_contains "formal doc names P state machines" "$tla_doc" "P state machines"
  check_contains "formal doc names deterministic simulation" "$tla_doc" "deterministic simulation"
  check_contains "formal doc requires queue spec template" "$tla_doc" "WorkerRetryQueue"
  check_contains "formal doc requires cache/projection spec template" "$tla_doc" "CacheProjectionFreshness"
  check_contains "formal doc requires WebSocket spec template" "$tla_doc" "WebSocketBackpressure"

  spec_dir="$docs_dir/specs/tla"
  check_exists "TLA spec template directory" "$spec_dir"
  check_exists "TLA spec template index" "$spec_dir/README.md"

  for spec in WorkerRetryQueue CacheProjectionFreshness WebSocketBackpressure FrontendLiveProjection; do
    file="$spec_dir/${spec}.tla"
    check_exists "TLA template $spec" "$file"
    check_contains "$spec defines Init" "$file" "Init =="
    check_contains "$spec defines Next" "$file" "Next =="
    check_contains "$spec defines TypeOK" "$file" "TypeOK =="
    check_contains "$spec defines Spec" "$file" "Spec =="
    check_contains "$spec declares theorem/invariant" "$file" "THEOREM"
  done
fi

if [[ "${FORMAL_RUN_TLC:-0}" == "1" ]]; then
  if [[ -z "${TLA_TOOLS_JAR:-}" || ! -f "${TLA_TOOLS_JAR:-}" ]]; then
    fail "FORMAL_RUN_TLC requested but TLA_TOOLS_JAR is missing" "set TLA_TOOLS_JAR=/path/to/tla2tools.jar"
  elif ! command -v java >/dev/null 2>&1; then
    fail "FORMAL_RUN_TLC requested but java is unavailable"
  else
    for cfg in "$spec_dir"/*.cfg; do
      [[ -f "$cfg" ]] || continue
      module="${cfg%.cfg}.tla"
      if java -cp "$TLA_TOOLS_JAR" tlc2.TLC -config "$cfg" "$module"; then
        ok "TLC model check ${module#$target/}"
      else
        fail "TLC model check ${module#$target/}"
      fi
    done
  fi
else
  ok "TLC execution is opt-in with FORMAL_RUN_TLC=1"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "formal methods check failed"
  exit 1
fi

echo "formal methods check passed"
