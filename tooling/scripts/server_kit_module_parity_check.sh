#!/bin/bash
# Server-kit module parity check.
#
# The scaffold copies server-kit/go to projects wholesale, so the module
# surface is an architecture contract: every module that exists in Foundation
# core must be declared in tooling/server_kit_module_manifest.tsv, and every
# vendored module must arrive in (and every foundation-only module must be
# pruned from) each generated project. This replaces hand-enumerated
# per-module existence checks: a new server-kit module gets fleet verification
# by adding one manifest row instead of editing every check script.
#
# Core layout ($target/server-kit/go + templates/): manifest <-> directory
# parity, both directions.
# Project layout ($target/foundation/server-kit/go): vendored modules present,
# foundation-only modules pruned, judged against the vendored manifest copy.
set -euo pipefail

target="${1:-.}"
target="${target%/}"
failed=0

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

kit=""
manifest=""
mode=""
if [[ -d "$target/server-kit/go" && -d "$target/templates" ]]; then
  mode="core"
  kit="$target/server-kit/go"
  manifest="$target/tooling/server_kit_module_manifest.tsv"
elif [[ -d "$target/foundation/server-kit/go" ]]; then
  mode="project"
  kit="$target/foundation/server-kit/go"
  manifest="$target/foundation/tooling/server_kit_module_manifest.tsv"
else
  echo "[OK] server-kit module parity (no server-kit tree at $target; skipped)"
  exit 0
fi

if [[ ! -f "$manifest" ]]; then
  fail "server-kit module manifest exists" \
    "missing: ${manifest#$target/}" \
    "core: regenerate the manifest row set from server-kit/go module directories" \
    "project: re-run foundation update so foundation/tooling is refreshed"
  echo "server-kit module parity check failed"
  exit 1
fi
ok "server-kit module manifest exists"

# Parse the manifest into module/propagation pairs.
manifest_modules=()
manifest_propagation=()
line_no=0
while IFS=$'\t' read -r module propagation _notes; do
  line_no=$((line_no + 1))
  [[ -z "${module:-}" || "$module" == \#* ]] && continue
  case "${propagation:-}" in
    vendored|foundation-only) ;;
    *)
      fail "manifest propagation class" \
        "line $line_no: module=${module} propagation=${propagation:-<empty>} (expected vendored|foundation-only)"
      continue
      ;;
  esac
  manifest_modules+=("$module")
  manifest_propagation+=("$propagation")
done <"$manifest"

if [[ "${#manifest_modules[@]}" -eq 0 ]]; then
  fail "manifest declares modules" "no module rows parsed from ${manifest#$target/}"
  echo "server-kit module parity check failed"
  exit 1
fi

manifest_has() {
  local needle="$1"
  local m
  for m in "${manifest_modules[@]}"; do
    [[ "$m" == "$needle" ]] && return 0
  done
  return 1
}

# Actual Go package modules: direct child directories holding Go files.
actual_modules=()
while IFS= read -r dir; do
  name="$(basename "$dir")"
  if compgen -G "$dir/*.go" >/dev/null; then
    actual_modules+=("$name")
  fi
done < <(find "$kit" -mindepth 1 -maxdepth 1 -type d | sort)

if [[ "$mode" == "core" ]]; then
  missing_rows=""
  for name in "${actual_modules[@]}"; do
    manifest_has "$name" || missing_rows+="$name"$'\n'
  done
  if [[ -n "$missing_rows" ]]; then
    fail "every server-kit module is declared in the manifest" \
      "add rows to ${manifest#$target/} (module<TAB>vendored|foundation-only<TAB>notes):" \
      "$(printf '%s' "$missing_rows" | sed 's/^/    /')"
  else
    ok "every server-kit module is declared in the manifest"
  fi

  stale_rows=""
  for name in "${manifest_modules[@]}"; do
    found=0
    for actual in "${actual_modules[@]}"; do
      [[ "$actual" == "$name" ]] && found=1 && break
    done
    [[ "$found" -eq 1 ]] || stale_rows+="$name"$'\n'
  done
  if [[ -n "$stale_rows" ]]; then
    fail "manifest declares only real server-kit modules" \
      "remove retired rows from ${manifest#$target/}:" \
      "$(printf '%s' "$stale_rows" | sed 's/^/    /')"
  else
    ok "manifest declares only real server-kit modules"
  fi
else
  missing_vendored=""
  present_core_only=""
  for i in "${!manifest_modules[@]}"; do
    name="${manifest_modules[$i]}"
    propagation="${manifest_propagation[$i]}"
    if [[ "$propagation" == "vendored" ]]; then
      [[ -d "$kit/$name" ]] || missing_vendored+="$name"$'\n'
    else
      [[ -e "$kit/$name" ]] && present_core_only+="$name"$'\n'
    fi
  done
  if [[ -n "$missing_vendored" ]]; then
    fail "vendored server-kit modules are present" \
      "missing under ${kit#$target/} (stale or partial foundation sync; re-run foundation update):" \
      "$(printf '%s' "$missing_vendored" | sed 's/^/    /')"
  else
    ok "vendored server-kit modules are present"
  fi
  if [[ -n "$present_core_only" ]]; then
    fail "foundation-only server-kit modules are pruned" \
      "remove from ${kit#$target/} (core-only assets must not ship in projects):" \
      "$(printf '%s' "$present_core_only" | sed 's/^/    /')"
  else
    ok "foundation-only server-kit modules are pruned"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "server-kit module parity check failed"
  exit 1
fi

echo "server-kit module parity check passed"
