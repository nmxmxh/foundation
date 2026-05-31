#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="${TMPDIR:-/tmp}/ovasabi_foundation_assets_check.out"

fail() {
  echo "[FAIL] $1"
  shift
  if [[ "$#" -gt 0 ]]; then
    local line
    for line in "$@"; do
      while IFS= read -r nested_line; do
        [[ -n "$nested_line" ]] && echo "  $nested_line"
      done <<<"$line"
    done
  fi
  failed=1
}

ok() {
  echo "[OK] $1"
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

check_no_paths() {
  local label="$1"
  shift
  local found=""
  local path
  for path in "$@"; do
    if [[ -e "$path" ]]; then
      found+="${path#$target/}"$'\n'
    fi
  done
  if [[ -n "$found" ]]; then
    fail "$label" "$found"
  else
    ok "$label"
  fi
}

check_no_find() {
  local label="$1"
  shift
  : >"$tmp_output"
  find "$@" >"$tmp_output" 2>/dev/null || true
  if [[ -s "$tmp_output" ]]; then
    fail "$label" "$(sed "s#^$target/##" "$tmp_output")"
  else
    ok "$label"
  fi
}

check_no_tracked_path_pattern() {
  local label="$1"
  local pattern="$2"
  if git -C "$target" rev-parse --show-toplevel >/dev/null 2>&1; then
    if git -C "$target" ls-files | grep -E "$pattern" >"$tmp_output" 2>/dev/null; then
      fail "$label" "$(cat "$tmp_output")"
    else
      ok "$label"
    fi
  else
    ok "$label (no git metadata)"
  fi
}

check_no_rg() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    fail "$label" "$(cat "$tmp_output")"
  else
    ok "$label"
  fi
}

check_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

check_no_paths "foundation tree avoids checked-in local build residue" \
  "$target/templates/.DS_Store" \
  "$target/server-kit/go/appbench.test" \
  "$target/server-kit/go/chain.test" \
  "$target/server-kit/go/grpcsvc.test"

check_no_tracked_path_pattern "foundation git index avoids cache/build directories" '(^|/)(\.gocache|node_modules|dist|build|target)(/|$)'

for module in server-kit/go runtime-transport/go runtime-sdk/go config-contracts/go; do
  check_exists "$module module manifest" "$target/$module/go.mod"
done

check_exists "runtime transport foundation proto namespace" "$target/runtime-transport/protos/foundation"
check_no_paths "legacy transport proto namespace is removed" \
  "$target/runtime-transport/protos/transport/v1/envelope.proto" \
  "$target/runtime-transport/protos/transport/v1/metadata.proto"

check_file_contains "scaffold sync copies tooling scripts" "$target/scripts/lib/scaffold.sh" "cp -R \"\$FOUNDATION_DIR/tooling/scripts/.\" \"\$PROJECT_PATH/scripts/checks/\""
check_exists "logging practice check exists for scaffold sync" "$target/tooling/scripts/logging_practices_check.sh"
check_exists "server-kit usage check exists for scaffold sync" "$target/tooling/scripts/server_kit_usage_check.sh"
check_exists "metadata practice check exists for scaffold sync" "$target/tooling/scripts/metadata_practices_check.sh"
check_exists "Vitest Node runtime runner exists for scaffold sync" "$target/tooling/scripts/run_vitest.sh"
check_file_contains "scaffold Makefile exposes lint-foundation" "$target/templates/Makefile" "lint-foundation"

check_no_rg "templates avoid deprecated nginx brotli base image" "fholzer/nginx-brotli" "$target/templates" --glob 'Dockerfile*' --glob '*.yml'
check_no_rg "templates avoid PostgreSQL 18 data subdirectory mount" "/var/lib/postgresql/data" "$target/templates" --glob '*.yml'
check_no_rg "templates avoid Redis 7 baseline" "redis:7" "$target/templates" --glob '*.yml'
check_file_contains "template Redis baseline is Redis 8" "$target/templates/docker/docker-compose.yml" "redis:8-alpine"
check_file_contains "template Postgres mount uses PostgreSQL root" "$target/templates/docker/docker-compose.yml" "/var/lib/postgresql"

if [[ "$failed" -ne 0 ]]; then
  echo "foundation assets check failed"
  exit 1
fi

echo "foundation assets check passed"
