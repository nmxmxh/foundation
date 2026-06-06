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
  if rg -n \
    --glob '!**/generated/**' \
    --glob '!**/*test*' \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/docs/**' \
    --glob '!**/tooling/scripts/**' \
    --glob '!**/scripts/checks/**' \
    "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

product_paths=()
for path in \
  "$target/internal" \
  "$target/cmd" \
  "$target/api" \
  "$target/frontend/src" \
  "$target/wasm" \
  "$target/runtime-transport" \
  "$target/runtime-sdk" \
  "$target/runtime-native" \
  "$target/templates/backend/internal" \
  "$target/templates/backend/cmd" \
  "$target/templates/frontend/src" \
  "$target/templates/wasm"; do
  [[ -e "$path" ]] && product_paths+=("$path")
done

if (( ${#product_paths[@]} > 0 )); then
  check_no_match \
    "dynamic payload maps are not allowed in scaffolded product hot paths" \
    "map\\[string\\](any|interface\\{\\})|Record<\\s*string\\s*,\\s*(unknown|any)\\s*>|\\{\\s*\\[key:\\s*string\\]\\s*:\\s*(unknown|any)\\s*\\}" \
    "${product_paths[@]}"

  check_no_match \
    "product hot paths avoid ad hoc JSON encode/decode" \
    "json\\.(Marshal|Unmarshal|NewDecoder|NewEncoder)|JSON\\.(parse|stringify)" \
    "${product_paths[@]}"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "dynamic payload practices check failed"
  exit 1
fi

echo "dynamic payload practices check passed"
