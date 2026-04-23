#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_cp_check.out"

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n --glob '!**/generated/**' --glob '!**/*test*' --glob '!**/node_modules/**' --glob '!**/dist/**' --glob '!**/target/**' --glob '!**/tooling/scripts/**' "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_absent_path() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    echo "[FAIL] $label"
    echo "$path"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_no_match "CP no goto in Go sources" "\\bgoto\\b" "$target"
check_no_match "CP no unsafe in Go sources" "\\bunsafe\\b" "$target" --glob '*.go'
check_no_match "CP no unwrap/todo/dbg in Rust runtime code" "\\bunwrap\\s*\\(|todo!\\s*\\(|dbg!\\s*\\(" "$target" --glob '*.rs'

check_absent_path "No checked-in dist artifacts" "$target/dist"
check_absent_path "No checked-in frontend dist artifacts" "$target/frontend/dist"
check_absent_path "No checked-in node_modules" "$target/node_modules"
check_absent_path "No checked-in macOS residue" "$target/.DS_Store"

if [[ "$failed" -ne 0 ]]; then
  echo "coding practices check failed"
  exit 1
fi

echo "coding practices check passed"
