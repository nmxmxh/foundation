#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_cp_check.out"
git_root=""

if git -C "$target" rev-parse --show-toplevel >/dev/null 2>&1; then
  git_root="$(git -C "$target" rev-parse --show-toplevel 2>/dev/null)"
fi

is_tracked_path() {
  local candidate="$1"
  if [[ -z "$git_root" ]]; then
    return 1
  fi

  local relative="$candidate"
  if [[ "$relative" = "$git_root/"* ]]; then
    relative="${relative#$git_root/}"
  fi
  relative="${relative#$target/}"
  relative="${relative#./}"
  if [[ -d "$candidate" ]]; then
    local tracked
    tracked="$(git -C "$git_root" ls-files -- "$relative" 2>/dev/null)"
    [[ -n "$tracked" ]]
    return $?
  else
    git -C "$git_root" ls-files --error-unmatch "$relative" >/dev/null 2>&1
    return $?
  fi
}

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
    --glob '!**/tooling/scripts/**' \
    --glob '!**/docs/**' \
    --glob '!**/data/**' \
    "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
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
    if is_tracked_path "$path"; then
      echo "[FAIL] $label"
      echo "$path"
      failed=1
    else
      echo "[OK] $label (local ignored artifact)"
    fi
  else
    echo "[OK] $label"
  fi
}

check_no_match "CP no goto in handwritten sources" "\\bgoto\\b" "$target" --glob '*.go' --glob '*.rs' --glob '*.ts' --glob '*.tsx'
check_no_match "CP no unsafe in handwritten Go sources" "(\"unsafe\"|\\bunsafe\\.)" "$target" --glob '*.go' --glob '!**/*.pb.go' --glob '!**/foundation/runtime-sdk/**' --glob '!**/runtime-sdk/**'
check_no_match "CP JSON marshal/unmarshal errors checked" "(_\\s*=\\s*json\\.Unmarshal|[A-Za-z0-9_]+\\s*,\\s*_\\s*:=\\s*json\\.Marshal|_\\s*,\\s*[A-Za-z0-9_]+\\s*:=\\s*json\\.Marshal)" "$target" --glob '*.go'
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
