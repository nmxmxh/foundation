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

check_file_not_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    fail "$label" "unexpected pattern: $pattern" "file: ${file#$target/}"
  else
    ok "$label"
  fi
}

check_no_paths "foundation tree avoids checked-in local build residue" \
  "$target/templates/.DS_Store" \
  "$target/server-kit/go/appbench.test" \
  "$target/server-kit/go/chain.test" \
  "$target/server-kit/go/grpcsvc.test"

check_no_tracked_path_pattern "foundation git index avoids cache/build directories" '(^|/)(\.gocache|node_modules|dist|build|target)(/|$)'
check_file_not_contains "foundation gitignore does not blanket-ignore generated contracts" "$target/.gitignore" "**/generated/"

for module in server-kit/go runtime-transport/go runtime-sdk/go config-contracts/go; do
  check_exists "$module module manifest" "$target/$module/go.mod"
done

check_exists "runtime transport foundation proto namespace" "$target/runtime-transport/protos/foundation"
check_no_paths "legacy transport proto namespace is removed" \
  "$target/runtime-transport/protos/transport/v1/envelope.proto" \
  "$target/runtime-transport/protos/transport/v1/metadata.proto"

check_file_contains "scaffold sync copies tooling scripts" "$target/scripts/lib/scaffold.sh" "cp -R \"\$FOUNDATION_DIR/tooling/scripts/.\" \"\$PROJECT_PATH/scripts/checks/\""
check_exists "agent contract check exists for scaffold sync" "$target/tooling/scripts/agent_contract_check.sh"
check_exists "docs reference check exists for scaffold sync" "$target/tooling/scripts/docs_reference_check.mjs"
check_exists "practice controls matrix exists for scaffold sync" "$target/tooling/practice_controls.psv"
check_exists "practice controls check exists for scaffold sync" "$target/tooling/scripts/practice_controls_check.sh"
check_exists "lifecycle manifest check exists for scaffold sync" "$target/tooling/scripts/check_lifecycle_manifest.sh"
check_exists "lifecycle manifest generator exists for scaffold sync" "$target/tooling/scripts/generate_lifecycle_manifest.mjs"
check_exists "app security profile check exists for scaffold sync" "$target/tooling/scripts/app_security_profile_check.sh"
check_exists "runtime performance contract check exists for scaffold sync" "$target/tooling/scripts/runtime_performance_contract_check.sh"
check_exists "formal methods check exists for scaffold sync" "$target/tooling/scripts/formal_methods_check.sh"
check_exists "operational excellence check exists for scaffold sync" "$target/tooling/scripts/operational_excellence_check.sh"
check_exists "TLA worker retry queue template exists for docs sync" "$target/docs/specs/tla/WorkerRetryQueue.tla"
check_exists "TLA cache projection template exists for docs sync" "$target/docs/specs/tla/CacheProjectionFreshness.tla"
check_exists "TLA WebSocket template exists for docs sync" "$target/docs/specs/tla/WebSocketBackpressure.tla"
check_exists "operation migration log template exists for scaffold sync" "$target/templates/ops/migration_log.md"
check_exists "logging practice check exists for scaffold sync" "$target/tooling/scripts/logging_practices_check.sh"
check_exists "server-kit usage check exists for scaffold sync" "$target/tooling/scripts/server_kit_usage_check.sh"
check_exists "metadata practice check exists for scaffold sync" "$target/tooling/scripts/metadata_practices_check.sh"
check_exists "Vitest Node runtime runner exists for scaffold sync" "$target/tooling/scripts/run_vitest.sh"
check_file_contains "scaffold Makefile exposes lint-foundation" "$target/templates/Makefile" "lint-foundation"
check_file_contains "scaffold Makefile exposes docs reference check" "$target/templates/Makefile" "check-doc-references"
check_file_contains "scaffold Makefile exposes agent contract check" "$target/templates/Makefile" "check-agent-contract"
check_file_contains "scaffold Makefile exposes practice controls check" "$target/templates/Makefile" "check-practice-controls"
check_file_contains "scaffold Makefile exposes lifecycle manifest check" "$target/templates/Makefile" "check-lifecycle-manifest"
check_file_contains "app security profile template exists for scaffold sync" "$target/templates/ops/security_profile.md" "Application Security Profile"
check_file_contains "scaffold manifest creates app security profile" "$target/templates/scaffold.manifest.tsv" "docs/security/profile.md"
check_file_contains "scaffold Makefile exposes app security profile check" "$target/templates/Makefile" "check-app-security-profile"
check_file_contains "scaffold Makefile exposes runtime performance contract check" "$target/templates/Makefile" "check-runtime-performance-contracts"
check_file_contains "scaffold Makefile exposes formal methods check" "$target/templates/Makefile" "check-formal-methods"
check_file_contains "scaffold Makefile exposes operational excellence check" "$target/templates/Makefile" "check-operational-excellence"

check_no_rg "templates avoid deprecated nginx brotli base image" "fholzer/nginx-brotli" "$target/templates" --glob 'Dockerfile*' --glob '*.yml'
check_no_rg "templates avoid PostgreSQL 18 data subdirectory mount" "/var/lib/postgresql/data" "$target/templates" --glob '*.yml'
check_no_rg "templates avoid Redis 7 baseline" "redis:7" "$target/templates" --glob '*.yml'
check_no_rg "production Compose avoids Postgres config bind" "./config/postgresql.conf" "$target/templates/docker/docker-compose.yml"
check_no_rg "production Compose avoids Redis config bind" "./config/redis.conf" "$target/templates/docker/docker-compose.yml"
check_no_rg "production Compose avoids default CA bind" "config/certs/ca.crt" "$target/templates/docker/docker-compose.yml"
check_file_contains "template Redis baseline is Redis 8" "$target/templates/docker/Dockerfile.redis" "ARG REDIS_VERSION=8-alpine"
check_file_contains "template production Compose includes Postgres service" "$target/templates/docker/docker-compose.yml" "  postgres:"
check_file_contains "template Postgres mount uses PostgreSQL root" "$target/templates/docker/docker-compose.yml" "/var/lib/postgresql"
check_file_contains "template Postgres config is baked" "$target/templates/docker/Dockerfile.postgres" "COPY config/postgresql.conf"
check_file_contains "template Postgres hba is baked" "$target/templates/docker/Dockerfile.postgres" "COPY config/pg_hba.conf"
check_file_contains "template Redis config is baked" "$target/templates/docker/Dockerfile.redis" "COPY config/redis.conf"
check_file_contains "template Postgres uses baked hba" "$target/templates/docker/docker-compose.yml" "hba_file=/etc/postgresql/pg_hba.conf"
check_file_contains "template Postgres Compose auth allows SCRAM clients" "$target/templates/config/pg_hba.conf" "0.0.0.0/0"
check_file_contains "template Postgres local socket supports operator recovery" "$target/templates/config/pg_hba.conf" "local   all             all                                     trust"
check_file_contains "template migration fails fast on DB auth errors" "$target/templates/docker/docker-compose.yml" "database authentication is not retryable"
check_file_contains "template server receives DB host" "$target/templates/docker/docker-compose.yml" 'DB_HOST: "${DB_HOST:-postgres}"'
check_file_contains "template migrate defaults to Compose Postgres host" "$target/templates/docker/docker-compose.yml" 'DB_HOST=${DB_HOST:-postgres}'

if [[ "$failed" -ne 0 ]]; then
  echo "foundation assets check failed"
  exit 1
fi

echo "foundation assets check passed"
