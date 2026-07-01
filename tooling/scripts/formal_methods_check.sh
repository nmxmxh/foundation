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

  for spec in WorkerRetryQueue CacheProjectionFreshness WebSocketBackpressure FrontendLiveProjection HermesProjectionPublish MetadataMerge; do
    file="$spec_dir/${spec}.tla"
    check_exists "TLA spec $spec" "$file"
    check_contains "$spec defines Init" "$file" "Init =="
    check_contains "$spec defines Next" "$file" "Next =="
    check_contains "$spec defines TypeOK" "$file" "TypeOK =="
    check_contains "$spec defines Spec" "$file" "Spec =="
    check_contains "$spec declares theorem/invariant" "$file" "THEOREM"
  done

  # Every spec must be model-checkable (ship a runnable TLC config) AND carry a
  # negative control: a *Broken model that injects a bad transition so a named
  # invariant is violated, proving the invariant has teeth. The config may be a
  # bare <Name>.cfg (monolithic spec) or an MC<Name>.cfg (base + finite model
  # instance); either satisfies the check.
  for spec in WorkerRetryQueue CacheProjectionFreshness WebSocketBackpressure FrontendLiveProjection HermesProjectionPublish MetadataMerge; do
    if [[ -f "$spec_dir/${spec}.cfg" || -f "$spec_dir/MC${spec}.cfg" ]]; then
      ok "$spec has a runnable TLC config"
    else
      fail "$spec has a runnable TLC config" "expected ${spec}.cfg or MC${spec}.cfg"
    fi
    if [[ -f "$spec_dir/${spec}Broken.cfg" || -f "$spec_dir/MC${spec}Broken.cfg" ]]; then
      ok "$spec has a negative control"
    else
      fail "$spec has a negative control" "expected ${spec}Broken.cfg or MC${spec}Broken.cfg"
    fi
  done

  # HermesProjectionPublish is the implementation-mapped spec: it must assert the
  # tear-free read invariant that mirrors the Go concurrency test, and name the
  # atomic.Pointer it models.
  hermes_spec="$spec_dir/HermesProjectionPublish.tla"
  check_contains "Hermes spec asserts tear-free read invariant" "$hermes_spec" "TearFreeRead"
  check_contains "Hermes spec maps to atomic.Pointer publish" "$hermes_spec" "atomic.Pointer"

  # MetadataMerge is the CRDT convergence spec: it must assert Strong Eventual
  # Consistency, the join-semilattice result from mathematical_practices.md.
  check_contains "MetadataMerge asserts Strong Eventual Consistency" "$spec_dir/MetadataMerge.tla" "StrongEventualConsistency"
fi

if [[ "${FORMAL_RUN_TLC:-0}" == "1" ]]; then
  if [[ -z "${TLA_TOOLS_JAR:-}" || ! -f "${TLA_TOOLS_JAR:-}" ]]; then
    fail "FORMAL_RUN_TLC requested but TLA_TOOLS_JAR is missing" "set TLA_TOOLS_JAR=/path/to/tla2tools.jar"
  elif ! command -v java >/dev/null 2>&1; then
    fail "FORMAL_RUN_TLC requested but java is unavailable"
  else
    tlc_jar="$(cd "$(dirname "$TLA_TOOLS_JAR")" && pwd)/$(basename "$TLA_TOOLS_JAR")"
    # Run TLC from inside the spec directory so an INSTANCE resolves its sibling
    # base module, and send scratch output to a throwaway metadir so TLC's
    # states/ directory never lands in docs/ (which propagates to apps).
    scratch="$(mktemp -d "${TMPDIR:-/tmp}/tla-scratch.XXXXXX")"
    trap 'rm -rf "$scratch"' EXIT
    for cfg in "$spec_dir"/*.cfg; do
      [[ -f "$cfg" ]] || continue
      cfg_base="$(basename "$cfg")"
      module_base="$(basename "${cfg%.cfg}.tla")"
      label="${cfg%.cfg}.tla"; label="${label#$target/}"
      case "$cfg_base" in
        *Broken*)
          # Negative control: TLC MUST report an invariant violation. -deadlock
          # disables deadlock checking (terminal states are expected in these
          # safety models and are not errors).
          out="$( (cd "$spec_dir" && java -cp "$tlc_jar" tlc2.TLC -deadlock -metadir "$scratch" -config "$cfg_base" "$module_base") 2>&1 || true )"
          if printf '%s\n' "$out" | grep -q "is violated"; then
            violated="$(printf '%s\n' "$out" | grep -m1 "is violated")"
            ok "TLC negative control caught: $label ($violated)"
          else
            fail "TLC negative control did NOT fail (invariant has no teeth): $label"
          fi
          ;;
        *)
          if (cd "$spec_dir" && java -cp "$tlc_jar" tlc2.TLC -deadlock -metadir "$scratch" -config "$cfg_base" "$module_base") >/dev/null 2>&1; then
            ok "TLC model check $label"
          else
            fail "TLC model check $label"
          fi
          ;;
      esac
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
