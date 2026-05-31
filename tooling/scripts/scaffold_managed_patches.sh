#!/usr/bin/env bash

set -euo pipefail

target="${1:-}"
if [[ -z "$target" ]]; then
  echo "usage: scaffold_managed_patches.sh <project-path>" >&2
  exit 2
fi

if [[ ! -d "$target" ]]; then
  echo "project path not found: $target" >&2
  exit 2
fi

patched=0

log_patch() {
  printf '[PATCH] %s\n' "$1"
  patched=$((patched + 1))
}

replace_in_file() {
  local file="$1"
  local search="$2"
  local replace="$3"
  local label="$4"

  [[ -f "$file" ]] || return 0
  if ! grep -Fq -- "$search" "$file"; then
    return 0
  fi

  perl -0pi -e 's/\Q$ENV{PATCH_SEARCH}\E/$ENV{PATCH_REPLACE}/g' "$file"
  log_patch "$label: ${file#$target/}"
}

replace_go_version_defaults() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  PATCH_SEARCH='ARG GO_VERSION=1.25' PATCH_REPLACE='ARG GO_VERSION=1.26' replace_in_file "$file" 'ARG GO_VERSION=1.25' 'ARG GO_VERSION=1.26' "Go 1.26 scaffold default"
  PATCH_SEARCH='${GO_VERSION:-1.25}' PATCH_REPLACE='${GO_VERSION:-1.26}' replace_in_file "$file" '${GO_VERSION:-1.25}' '${GO_VERSION:-1.26}' "Go 1.26 scaffold default"
  PATCH_SEARCH='GO_VERSION=1.25' PATCH_REPLACE='GO_VERSION=1.26' replace_in_file "$file" 'GO_VERSION=1.25' 'GO_VERSION=1.26' "Go 1.26 scaffold default"
}

patch_compose_targets() {
  local compose="$1"
  local dockerfile="$target/Dockerfile"
  [[ -f "$compose" && -f "$dockerfile" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$compose" "$before"

  if grep -q ' AS final$' "$dockerfile"; then
    perl -0pi -e 's/target:\s+final-app/target: final/g' "$compose"
  fi

  if grep -q ' AS frontend$' "$dockerfile"; then
    perl -0pi -e 's/target:\s+final-nginx/target: frontend/g' "$compose"
  fi

  if ! cmp -s "$before" "$compose"; then
    log_patch "Docker Compose build target drift: ${compose#$target/}"
  fi
  rm -f "$before"
}

patch_reframe_frontend_dockerfile() {
  local file="$target/frontend/Dockerfile"
  [[ -f "$file" ]] || return 0

  local search='FROM fholzer/nginx-brotli:v1.24.0
COPY --from=build /app/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["-g", "daemon off;"]'

  local replace='FROM alpine:3.23
RUN apk add --no-cache nginx nginx-mod-http-brotli ca-certificates && \
    mkdir -p /var/cache/nginx /var/run /var/log/nginx /var/lib/nginx
COPY --from=build /app/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]'

  PATCH_SEARCH="$search" PATCH_REPLACE="$replace" replace_in_file "$file" "$search" "$replace" "removed nginx brotli image"
}

patch_runtime_native_dockerfile() {
  local file="$target/Dockerfile"
  local package_json="$target/frontend/package.json"
  [[ -f "$file" && -f "$package_json" ]] || return 0
  grep -Fq '"@ovasabi/runtime-native"' "$package_json" || return 0

  if ! grep -Fq 'COPY foundation/runtime-native/ts/package.json ./foundation/runtime-native/ts/' "$file"; then
    PATCH_SEARCH='COPY foundation/ui-minimal/ts/package.json ./foundation/ui-minimal/ts/'
    PATCH_REPLACE='COPY foundation/ui-minimal/ts/package.json ./foundation/ui-minimal/ts/
COPY foundation/runtime-native/ts/package.json ./foundation/runtime-native/ts/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "runtime-native Docker package manifest"
  fi

  if ! grep -Fq 'COPY foundation/runtime-native/ts ./foundation/runtime-native/ts' "$file"; then
    PATCH_SEARCH='COPY foundation/ui-minimal/ts ./foundation/ui-minimal/ts'
    PATCH_REPLACE='COPY foundation/ui-minimal/ts ./foundation/ui-minimal/ts
COPY foundation/runtime-native/ts ./foundation/runtime-native/ts'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "runtime-native Docker source"
  fi
}

patch_go_dependency_manifests() {
  local file="$target/Dockerfile"
  [[ -f "$file" ]] || return 0

  if grep -Fq 'go mod download && touch /tmp/deps-ready' "$file"; then
    PATCH_SEARCH='go mod download && touch /tmp/deps-ready'
    PATCH_REPLACE='set -eux; \
    go env GOMOD GOWORK GOPROXY; \
    find . -path '\''*/go.mod'\'' -maxdepth 5 -print | sort; \
    touch /tmp/deps-ready'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency diagnostics"
  fi

  if grep -Fq '    go mod download; \' "$file"; then
    PATCH_SEARCH='    go mod download; \
'
    PATCH_REPLACE=''
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency predownload gate"
  fi

  if grep -Fq '=> ./foundation/runtime-sdk/go' "$target/go.mod" && ! grep -Fq 'COPY foundation/runtime-sdk/go/go.mod ./foundation/runtime-sdk/go/' "$file"; then
    PATCH_SEARCH='COPY foundation/config-contracts/go/go.mod ./foundation/config-contracts/go/'
    PATCH_REPLACE='COPY foundation/config-contracts/go/go.mod ./foundation/config-contracts/go/
COPY foundation/runtime-sdk/go/go.mod ./foundation/runtime-sdk/go/
COPY foundation/runtime-sdk/go/go.sum ./foundation/runtime-sdk/go/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency manifest: runtime-sdk"
  fi

  if [[ -f "$target/api/protos/go.mod" ]] && grep -Fq '=> ./api/protos' "$target/go.mod" && ! grep -Fq 'COPY api/protos/go.mod ./api/protos/' "$file"; then
    PATCH_SEARCH='COPY go.sum ./'
    PATCH_REPLACE='COPY go.sum ./
COPY api/protos/go.mod ./api/protos/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency manifest: api/protos"
  fi
}

patch_server_binary_path() {
  local file="$target/Dockerfile"
  [[ -f "$file" ]] || return 0

  PATCH_SEARCH='ARG PROJECT_NAME=server
' PATCH_REPLACE='' replace_in_file "$file" 'ARG PROJECT_NAME=server
' '' "Docker server binary fixed output"
  PATCH_SEARCH='CGO_ENABLED=${CGO_ENABLED} go build' PATCH_REPLACE='CGO_ENABLED="${CGO_ENABLED:-0}" go build' replace_in_file "$file" 'CGO_ENABLED=${CGO_ENABLED} go build' 'CGO_ENABLED="${CGO_ENABLED:-0}" go build' "Docker server CGO default"
  PATCH_SEARCH='-ldflags="-s -w -X main.Version=${VERSION}"' PATCH_REPLACE='-ldflags="-s -w -X main.Version=${VERSION:-dev}"' replace_in_file "$file" '-ldflags="-s -w -X main.Version=${VERSION}"' '-ldflags="-s -w -X main.Version=${VERSION:-dev}"' "Docker server version default"
  PATCH_SEARCH='-o /bin/${PROJECT_NAME} ./cmd/server' PATCH_REPLACE='-o /bin/server ./cmd/server' replace_in_file "$file" '-o /bin/${PROJECT_NAME} ./cmd/server' '-o /bin/server ./cmd/server' "Docker server binary fixed output"
  PATCH_SEARCH='COPY --from=builder /bin/${PROJECT_NAME} ./server' PATCH_REPLACE='COPY --from=builder /bin/server ./server' replace_in_file "$file" 'COPY --from=builder /bin/${PROJECT_NAME} ./server' 'COPY --from=builder /bin/server ./server' "Docker server binary fixed copy"
}

patch_foundation_event_log_trigger_function() {
  local migration
  while IFS= read -r migration; do
    if grep -Fq "CREATE OR REPLACE FUNCTION update_updated_at_column" "$migration"; then
      continue
    fi
    if ! grep -Fq "update_foundation_event_log_updated_at" "$migration"; then
      continue
    fi
    if ! grep -Fq "EXECUTE FUNCTION update_updated_at_column()" "$migration"; then
      continue
    fi

    perl -0pi -e 's/-- Foundation durable event log/CREATE OR REPLACE FUNCTION update_updated_at_column()\nRETURNS TRIGGER AS \$\$\nBEGIN\n    NEW.updated_at = NOW();\n    RETURN NEW;\nEND;\n\$\$ LANGUAGE plpgsql;\n\n-- Foundation durable event log/' "$migration"
    log_patch "Foundation event log trigger function: ${migration#$target/}"
  done < <(find "$target/migrations" -maxdepth 1 -type f -name '*.up.sql' 2>/dev/null | sort)
}

patch_test_postgres_platform() {
  local file="$target/docker-compose.test.yml"
  [[ -f "$file" ]] || return 0
  grep -Fq "TEST_POSTGRES_PLATFORM" "$file" && return 0

  if grep -Fq 'image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}' "$file"; then
    PATCH_SEARCH='    image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}'
    PATCH_REPLACE='    image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}
    platform: ${TEST_POSTGRES_PLATFORM:-linux/amd64}'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "test Postgres service platform"
  fi
}

patch_test_compose_ephemeral_ports() {
  local file="$target/docker-compose.test.yml"
  [[ -f "$file" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$file" "$before"

  perl -0pi -e 's/#   TEST_DB_PORT: Port to expose PostgreSQL \(default: 5433\)/#   TEST_DB_PORT: Host port to expose PostgreSQL (default: 0, Docker assigns an ephemeral port)/g' "$file"
  perl -0pi -e 's/#   TEST_REDIS_PORT: Port to expose Redis \(default: 6380\)/#   TEST_REDIS_PORT: Host port to expose Redis (default: 0, Docker assigns an ephemeral port)/g' "$file"
  perl -0pi -e 's/"\$\{TEST_DB_PORT:-5433\}:5432"/"\${TEST_DB_PORT:-0}:5432"/g' "$file"
  perl -0pi -e 's/"\$\{TEST_REDIS_PORT:-6380\}:6379"/"\${TEST_REDIS_PORT:-0}:6379"/g' "$file"
  perl -0pi -e 's/^[[:blank:]]*container_name:[^\n]*\n//mg' "$file"

  if ! cmp -s "$before" "$file"; then
    log_patch "test Compose ephemeral ports and scoped container names: ${file#$target/}"
  fi
  rm -f "$before"
}

sync_go_work() {
  [[ -f "$target/go.mod" ]] || return 0

  local modules=(".")
  local optional_modules=(
    "./api/protos"
    "./foundation/config-contracts/go"
    "./foundation/runtime-sdk/go"
    "./foundation/runtime-transport/go"
    "./foundation/server-kit/go"
    "./wasm"
  )

  local module
  for module in "${optional_modules[@]}"; do
    [[ -f "$target/${module#./}/go.mod" ]] && modules+=("$module")
  done

  local tmp
  tmp="$(mktemp)"
  {
    printf 'go 1.26.0\n\n'
    printf 'use (\n'
    for module in "${modules[@]}"; do
      printf '\t%s\n' "$module"
    done
    printf ')\n'
  } >"$tmp"

  if [[ ! -f "$target/go.work" ]] || ! cmp -s "$tmp" "$target/go.work"; then
    cp "$tmp" "$target/go.work"
    log_patch "Go workspace scaffold: go.work"
  fi
  rm -f "$tmp"
}

export PATCH_SEARCH PATCH_REPLACE

replace_go_version_defaults "$target/.env.example"
replace_go_version_defaults "$target/Dockerfile"
replace_go_version_defaults "$target/docker-compose.yml"
replace_go_version_defaults "$target/docker-compose.dev.yml"
replace_go_version_defaults "$target/docker-compose.test.yml"

patch_compose_targets "$target/docker-compose.yml"
patch_compose_targets "$target/docker-compose.dev.yml"
patch_compose_targets "$target/docker-compose.test.yml"
patch_reframe_frontend_dockerfile
patch_runtime_native_dockerfile
patch_go_dependency_manifests
patch_server_binary_path
patch_foundation_event_log_trigger_function
patch_test_postgres_platform
patch_test_compose_ephemeral_ports
sync_go_work

if [[ "$patched" -eq 0 ]]; then
  printf '[PATCH] no managed scaffold drift found\n'
fi
