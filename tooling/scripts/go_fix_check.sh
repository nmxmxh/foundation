#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_go_fix_check.out"

if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi

target="${target:A}"
cache_dir="${GOCACHE:-$target/.cache/go-build}"
mkdir -p "$cache_dir"

typeset -a module_dirs

add_module() {
  local module_dir="$1"
  if [[ -f "$module_dir/go.mod" ]]; then
    module_dirs+=("${module_dir:A}")
  fi
}

case "${target:t}" in
  foundation)
    add_module "$target/server-kit/go"
    add_module "$target/runtime-transport/go"
    add_module "$target/runtime-sdk/go"
    add_module "$target/config-contracts/go"
    ;;
  *)
    add_module "$target"
    add_module "$target/backend"
    ;;
esac

if [[ "${#module_dirs[@]}" -eq 0 ]]; then
  echo "[OK] go fix modernization check skipped; no Go modules found"
  exit 0
fi

for module_dir in "${module_dirs[@]}"; do
  ignored_mods=()
  for ignored_dir in "$module_dir"/frontend/node_modules "$module_dir"/node_modules "$module_dir"/dist "$module_dir"/build; do
    if [[ -d "$ignored_dir" && ! -f "$ignored_dir/go.mod" ]]; then
      printf 'module ignored.local/artifact\n\ngo 1.25\n' >"$ignored_dir/go.mod"
      ignored_mods+=("$ignored_dir/go.mod")
    fi
  done
  cleanup_ignored_mods() {
    for ignored_mod in "${ignored_mods[@]}"; do
      rm -f "$ignored_mod"
    done
  }
  trap cleanup_ignored_mods EXIT
  if GOCACHE="$cache_dir" go fix -C "$module_dir" -diff ./... >"$tmp_output" 2>&1; then
    cleanup_ignored_mods
    echo "[OK] go fix modernization check: ${module_dir#$target/}"
  else
    cleanup_ignored_mods
    echo "[FAIL] go fix modernization check: ${module_dir#$target/}"
    cat "$tmp_output"
    failed=1
  fi
  trap - EXIT
done

if [[ "$failed" -ne 0 ]]; then
  echo "go fix modernization check failed"
  echo "Run: GOCACHE=\"$cache_dir\" go fix -C <module> ./..."
  exit 1
fi

echo "go fix modernization check passed"
