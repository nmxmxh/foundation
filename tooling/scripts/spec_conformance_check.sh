#!/bin/zsh
set -euo pipefail

# spec_conformance_check.sh -- enforce that every TLA invariant is tied to real
# code that confirms it. Reads docs/specs/tla/conformance.tsv and verifies every
# anchor (a test, a source symbol, a make target, a Rust loom module, a TS test)
# actually exists. A rename or deletion of a referenced test/symbol breaks this
# check, so the spec<->code mapping is an enforced reference, not a comment.
#
# It also asserts each spec is backed by BOTH a model anchor (tlc) AND at least
# one real-code anchor -- so no spec is "verified" only in the abstract.

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

# Resolve grep once, up front, and call it by absolute path. Bare `grep` can be
# unresolvable inside a redirected `while read` loop in some sandboxed shells.
GREP="$(command -v grep 2>/dev/null || true)"
[[ -z "$GREP" ]] && GREP=/usr/bin/grep

ok()   { echo "[OK] $1"; }
gap()  { echo "[GAP] $1"; }
fail() { echo "[FAIL] $1"; shift; local l; for l in "$@"; do [[ -n "$l" ]] && echo "  $l"; done; failed=1; }

# Resolve a manifest path in either Foundation Core (server-kit/...) or a
# generated app where Core is vendored (foundation/server-kit/...).
resolve_path() {
  local p="$1"
  if [[ -e "$target/$p" ]]; then printf '%s\n' "$target/$p"; return 0; fi
  if [[ -e "$target/foundation/$p" ]]; then printf '%s\n' "$target/foundation/$p"; return 0; fi
  return 1
}

manifest="$(resolve_path docs/specs/tla/conformance.tsv || true)"
if [[ -z "${manifest:-}" ]]; then
  # Some generated projects place docs under docs/foundation/.
  if [[ -e "$target/docs/foundation/specs/tla/conformance.tsv" ]]; then
    manifest="$target/docs/foundation/specs/tla/conformance.tsv"
  else
    fail "conformance manifest exists" "expected docs/specs/tla/conformance.tsv"
    echo "spec conformance check failed"; exit 1
  fi
fi
ok "conformance manifest: ${manifest#$target/}"

typeset -A has_model has_code
specs_order=()

while IFS=$'\t' read -r spec invariant kind anchor path; do
  [[ -z "$spec" || "$spec" == \#* ]] && continue
  [[ -z "${has_model[$spec]:-}${has_code[$spec]:-}" ]] && specs_order+=("$spec")

  file="$(resolve_path "$path" || true)"
  label="$spec/$invariant [$kind] $anchor"

  if [[ -z "${file:-}" ]]; then
    fail "$label" "missing file: $path"
    continue
  fi

  # Verify the anchor actually appears in the referenced file (fixed-string).
  if "$GREP" -Fq -- "$anchor" "$file"; then
    ok "$label -> ${file#$target/}"
  else
    fail "$label" "anchor not found in ${file#$target/}: $anchor"
    continue
  fi

  if [[ "$kind" == "tlc" ]]; then
    has_model[$spec]=1
  else
    has_code[$spec]=1
  fi
done < "$manifest"

# Every spec must be tied to BOTH the model and real code.
for spec in "${specs_order[@]}"; do
  if [[ -z "${has_model[$spec]:-}" ]]; then
    fail "$spec has a model (tlc) anchor"
  fi
  if [[ -z "${has_code[$spec]:-}" ]]; then
    fail "$spec has a real-code anchor" "a spec verified only in the abstract does not confirm the codebase"
  fi
  if [[ -n "${has_model[$spec]:-}" && -n "${has_code[$spec]:-}" ]]; then
    ok "$spec: model + code anchors present"
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "spec conformance check failed"
  exit 1
fi

echo "spec conformance check passed"
