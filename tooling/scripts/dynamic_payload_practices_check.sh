#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_dynamic_payload_check.out"

if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  local rc=0
  rg -n \
    --glob '!**/generated/**' \
    --glob '!**/*test*' \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/docs/**' \
    --glob '!**/tooling/scripts/**' \
    --glob '!**/scripts/checks/**' \
    "$pattern" "$@" >"$tmp_output" 2>"$tmp_output.err" || rc=$?
  # rg exits 2 on a search error. Counting that as "no match" made gates pass
  # without searching, so it fails loudly.
  case "$rc" in
    0)
      echo "[FAIL] $label"
      cat "$tmp_output"
      failed=1
      ;;
    1)
      echo "[OK] $label"
      ;;
    *)
      echo "[FAIL] $label (search error, rg exit $rc)"
      cat "$tmp_output.err"
      failed=1
      ;;
  esac
}

product_paths=()
for product_path in \
  "$target/internal" \
  "$target/cmd" \
  "$target/api" \
  "$target/frontend/src" \
  "$target/runtime-transport" \
  "$target/runtime-sdk" \
  "$target/runtime-native" \
  "$target/templates/backend/internal" \
  "$target/templates/backend/cmd" \
  "$target/templates/frontend/src"; do
  [[ -e "$product_path" ]] && product_paths+=("$product_path")
done

# These two gates were dead until 2026-11-01: a `for path in` loop shadowed
# zsh's PATH, rg exited 127, and the helper counted that as a pass. Existing
# debt is ratcheted: a baseline file holds the current count per gate, and the
# count may only fall. Without a baseline file the counts are reported only.
baseline_file="$target/tooling/dynamic_payload_baseline.psv"

count_matches() {
  local pattern="$1"
  shift
  local rc=0 total=0 line
  rg -c \
    --glob '!**/generated/**' \
    --glob '!**/*test*' \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/docs/**' \
    --glob '!**/tooling/scripts/**' \
    --glob '!**/scripts/checks/**' \
    "$pattern" "$@" >"$tmp_output" 2>"$tmp_output.err" || rc=$?
  if (( rc > 1 )); then
    cat "$tmp_output.err" >&2
    echo "-1"
    return
  fi
  while IFS= read -r line; do
    total=$(( total + ${line##*:} ))
  done <"$tmp_output"
  echo "$total"
}

ratchet_gate() {
  local key="$1" label="$2" pattern="$3"
  shift 3
  local count ceiling=""
  count="$(count_matches "$pattern" "$@")"
  if [[ "$count" == "-1" ]]; then
    echo "[FAIL] $label (search error)"
    failed=1
    return
  fi
  if [[ -f "$baseline_file" ]]; then
    ceiling="$(awk -F'|' -v k="$key" '$1 == k { print $2 }' "$baseline_file")"
  fi
  if [[ -z "$ceiling" ]]; then
    echo "[WARN] $label: $count matches (no ratchet baseline; report only)"
  elif (( count > ceiling )); then
    echo "[FAIL] $label: $count matches, ceiling $ceiling. New dynamic payloads were added; use typed contracts."
    failed=1
  elif (( count < ceiling )); then
    echo "[OK] $label: $count matches, below ceiling $ceiling. Lower the ceiling in $baseline_file."
  else
    echo "[OK] $label: $count matches (ceiling $ceiling)"
  fi
}

if (( ${#product_paths[@]} > 0 )); then
  ratchet_gate dynamic_maps \
    "dynamic payload maps in scaffolded product hot paths" \
    "map\\[string\\](any|interface\\{\\})|Record<\\s*string\\s*,\\s*(unknown|any)\\s*>|\\{\\s*\\[key:\\s*string\\]\\s*:\\s*(unknown|any)\\s*\\}" \
    "${product_paths[@]}"

  ratchet_gate adhoc_json \
    "ad hoc JSON encode/decode in product hot paths" \
    "json\\.(Marshal|Unmarshal|NewDecoder|NewEncoder)|JSON\\.(parse|stringify)" \
    "${product_paths[@]}"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "dynamic payload practices check failed"
  exit 1
fi

echo "dynamic payload practices check passed"
