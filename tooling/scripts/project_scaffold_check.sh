#!/bin/zsh
set -euo pipefail

target="${1:-.}"
foundation_file="$target/.foundation"
failed=0

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing: ${path#$target/}"
    failed=1
  fi
}

check_absent() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    echo "[FAIL] $label"
    echo "  remove: ${path#$target/}"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_not_symlink() {
  local label="$1"
  local path="$2"
  if [[ -L "$path" ]]; then
    echo "[FAIL] $label"
    echo "  replace symlink: ${path#$target/}"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_value() {
  local label="$1"
  local actual="$2"
  local expected="$3"
  if [[ "$actual" == "$expected" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  expected: $expected"
    echo "  actual: ${actual:-<unset>}"
    failed=1
  fi
}

check_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq "$pattern" "$file"; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing pattern: $pattern"
    echo "  file: ${file#$target/}"
    failed=1
  fi
}

if [[ ! -f "$foundation_file" ]]; then
  echo "[FAIL] foundation metadata missing"
  exit 1
fi

set -a
source "$foundation_file"
set +a

check_exists "foundation metadata" "$foundation_file"
check_value "manifest baseline generation" "${BASELINE_GENERATION:-}" "manifest-v3"
check_not_symlink "foundation directory is local" "$target/foundation"
check_exists "cursor rules" "$target/.cursorrules"
check_exists "agents guide" "$target/.agents/DOMAIN_GUIDE.md"
check_exists "post-init checklist" "$target/.agents/POST_INIT.md"
check_exists "foundation guide" "$target/docs/foundation/foundation_guide.md"
check_exists "foundation architecture contract" "$target/docs/foundation/foundation_architecture_contract.md"
check_exists "coding practices" "$target/docs/foundation/coding_practices.md"
check_exists "post quantum security posture" "$target/docs/foundation/post_quantum_security.md"
check_exists "golangci baseline" "$target/.golangci.yml"
check_exists "coding practices check" "$target/scripts/checks/coding_practices_check.sh"
check_exists "river practices check" "$target/scripts/checks/river_practices_check.sh"
check_exists "project scaffold check" "$target/scripts/checks/project_scaffold_check.sh"
check_exists "ci workflow" "$target/.github/workflows/ci.yml"
check_exists "security workflow" "$target/.github/workflows/security.yml"

check_absent "stale vendored foundation initializer" "$target/foundation/init.sh"
check_absent "stale vendored foundation updater" "$target/foundation/scripts/update-project.sh"
check_absent "unowned root pkg directory" "$target/pkg"

if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "backend" ]]; then
  check_exists "server command" "$target/cmd/server/main.go"
  check_exists "worker command" "$target/cmd/worker/main.go"
  check_exists "docgen command" "$target/cmd/docgen/main.go"
  check_exists "startup package" "$target/internal/startup"
  check_exists "bootstrap services container" "$target/internal/bootstrap/services.go"
  check_exists "river worker registry" "$target/internal/worker/registry.go"
  check_exists "river periodic jobs" "$target/internal/worker/periodic_jobs.go"
  check_exists "integration infra test" "$target/tests/integration/infra_test.go"
  check_file_contains "managed test env defaults" "$target/tests/testutil/env.go" "ApplyTestEnvDefaults"
  check_file_contains "managed test infra required flag" "$target/tests/testutil/env.go" "TEST_INFRA_REQUIRED"
  check_file_contains "make integration target" "$target/Makefile" "test-integration"
  check_file_contains "make test database URL" "$target/Makefile" "TEST_DATABASE_URL"
  check_file_contains "make test Redis URL" "$target/Makefile" "TEST_REDIS_URL"
  check_file_contains "env test database URL" "$target/.env.example" "TEST_DATABASE_URL"
  check_file_contains "env test Redis URL" "$target/.env.example" "TEST_REDIS_URL"
  check_file_contains "env runtime shared memory mode" "$target/.env.example" "RUNTIME_SHARED_MEMORY"
  check_file_contains "env post quantum TLS mode" "$target/.env.example" "POST_QUANTUM_TLS_HYBRID_KEM"
  check_exists "foundation server-kit" "$target/foundation/server-kit/go/go.mod"
  check_exists "foundation metrics module" "$target/foundation/server-kit/go/metrics/metrics.go"
  check_exists "foundation grpc service module" "$target/foundation/server-kit/go/grpcsvc/grpcsvc.go"
  check_exists "foundation performance check script" "$target/foundation/tooling/scripts/performance_check.sh"
  check_exists "foundation parallel chain module" "$target/foundation/server-kit/go/chain/chain.go"
  check_exists "foundation chaos module" "$target/foundation/server-kit/go/chaos/chaos.go"
  check_exists "foundation contract testing module" "$target/foundation/server-kit/go/contracttest/event_contract.go"
  check_exists "foundation profiling module" "$target/foundation/server-kit/go/profiling/profiling.go"
  check_exists "foundation SLO module" "$target/foundation/server-kit/go/slo/slo.go"
  check_exists "foundation runtime transport" "$target/foundation/runtime-transport/go/go.mod"
  check_exists "foundation config contracts" "$target/foundation/config-contracts/go/go.mod"
  check_exists "foundation tooling" "$target/foundation/tooling/docs/enforcement.md"
  check_exists "api README" "$target/api/README.md"
  check_exists "proto README" "$target/api/protos/README.md"
fi

if [[ "${WITH_DOCKER:-}" == "true" ]]; then
  check_exists "Dockerfile" "$target/Dockerfile"
  check_exists "Dockerfile.migrate" "$target/Dockerfile.migrate"
  check_exists "docker-compose.yml" "$target/docker-compose.yml"
  check_exists "docker-compose.dev.yml" "$target/docker-compose.dev.yml"
  check_exists "docker-compose.test.yml" "$target/docker-compose.test.yml"
  check_exists "docker ignore" "$target/.dockerignore"
  check_file_contains "shared Docker Go module cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gomod'
  check_file_contains "shared Docker Go build cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gobuild'
  check_file_contains "Docker dependency stage" "$target/Dockerfile" "AS go-deps"
  check_file_contains "compose Docker cache namespace" "$target/docker-compose.yml" "DOCKER_CACHE_NAMESPACE"
  check_exists "nginx template" "$target/config/default.conf.template"
  check_exists "nginx config" "$target/config/nginx.conf"
  check_file_contains "nginx COOP header" "$target/config/default.conf.template" "Cross-Origin-Opener-Policy"
  check_file_contains "nginx COEP header" "$target/config/default.conf.template" "Cross-Origin-Embedder-Policy"
  check_file_contains "nginx CORP header" "$target/config/default.conf.template" "Cross-Origin-Resource-Policy"
  check_file_contains "nginx wasm compression types" "$target/config/nginx.conf" "application/wasm"
  check_exists "postgresql config" "$target/config/postgresql.conf"
  check_exists "redis config" "$target/config/redis.conf"
fi

if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "frontend" ]]; then
  frontend_root="$target/frontend"
  if [[ "${PROFILE:-}" == "frontend" ]]; then
    frontend_root="$target"
  fi
  check_exists "frontend package" "$frontend_root/package.json"
  check_exists "frontend tsconfig" "$frontend_root/tsconfig.json"
  check_exists "frontend app tsconfig" "$frontend_root/tsconfig.app.json"
  check_exists "frontend node tsconfig" "$frontend_root/tsconfig.node.json"
  check_exists "frontend eslint" "$frontend_root/eslint.config.js"
  check_exists "frontend vite config" "$frontend_root/vite.config.ts"
  check_file_contains "vite COOP header" "$frontend_root/vite.config.ts" "Cross-Origin-Opener-Policy"
  check_file_contains "vite COEP header" "$frontend_root/vite.config.ts" "Cross-Origin-Embedder-Policy"
  check_file_contains "vite websocket proxy" "$frontend_root/vite.config.ts" "'/ws'"
  check_exists "frontend vitest config" "$frontend_root/vitest.config.ts"
  check_exists "frontend vite env" "$frontend_root/src/vite-env.d.ts"
  check_exists "frontend test setup" "$frontend_root/src/test/setup.ts"
fi

if [[ "${WITH_WASM:-false}" == "true" ]]; then
  check_exists "runtime sdk" "$target/foundation/runtime-sdk/go/go.mod"
  check_exists "runtime shared arena schema" "$target/foundation/runtime-sdk/protocols/system/v1/runtime_shared_arena.capnp"
  check_exists "runtime shared arena host API" "$target/foundation/runtime-sdk/ts/browser-host/src/arena.ts"
  check_exists "runtime payload router API" "$target/foundation/runtime-sdk/ts/browser-host/src/payloadRouter.ts"
  check_exists "runtime transport compression API" "$target/foundation/runtime-transport/ts/src/compression.ts"
  check_exists "runtime offline queue API" "$target/foundation/runtime-transport/ts/src/offlineQueue.ts"
  check_exists "wasm entry" "$target/wasm/main.go"
  check_file_contains "wasm runtime-transport shim" "$target/wasm/main.go" "__OVASABI_RUNTIME_TRANSPORT"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "project scaffold check failed"
  exit 1
fi

echo "project scaffold check passed"
