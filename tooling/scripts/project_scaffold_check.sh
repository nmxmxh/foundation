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
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing pattern: $pattern"
    echo "  file: ${file#$target/}"
    failed=1
  fi
}

check_frontend_package_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing frontend manifest contract: $pattern"
    echo "  file: ${file#$target/}"
    failed=1
  fi
}

check_file_not_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    echo "[FAIL] $label"
    echo "  forbidden pattern: $pattern"
    echo "  file: ${file#$target/}"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_any_startup_contains() {
  local label="$1"
  local pattern="$2"
  local found="false"
  for file in "$target/internal/startup/dependencies.go" "$target/internal/startup/deps.go" "$target/internal/startup/init.go"; do
    if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
      found="true"
      break
    fi
  done
  if [[ "$found" == "true" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing startup pattern: $pattern"
    failed=1
  fi
}

check_typed_proto_plane() {
  local proto_root="$target/api/protos"
  [[ -d "$proto_root" ]] || return 0

  local has_domain_proto="false"
  local proto_file
  while IFS= read -r proto_file; do
    case "$proto_file" in
      */_template/*|*/common/*|*/transport/*) continue ;;
    esac
    if grep -Eq '^[[:space:]]*(service|message)[[:space:]]+' "$proto_file"; then
      has_domain_proto="true"
      break
    fi
  done < <(find "$proto_root" -type f -name '*.proto' 2>/dev/null)

  [[ "$has_domain_proto" == "true" ]] || return 0

  local typed_found="false"
  local search_root
  for search_root in "$target/internal" "$target/backend/internal"; do
    [[ -d "$search_root" ]] || continue
    if rg -n 'typedHandler|GetTypedHandlers|BuildTypedServiceHandlers|func \(s \*Service\) [A-Za-z0-9_]+V1\(ctx context\.Context, req \*.*v1\.[A-Za-z0-9_]+Request\)' "$search_root" \
      --glob '*.go' \
      --glob '!**/*test.go' >/dev/null 2>&1; then
      typed_found="true"
      break
    fi
  done

  if [[ "$typed_found" == "true" ]]; then
    echo "[OK] protobuf contracts project onto typed frame plane"
  else
    echo "[FAIL] protobuf contracts project onto typed frame plane"
    echo "  add real generated-protobuf typed handlers; empty AllTypedHandlers maps are not acceptable"
    failed=1
  fi

  local typed_adapter_violation="false"
  for search_root in "$target/internal/bootstrap" "$target/backend/internal/bootstrap"; do
    [[ -d "$search_root" ]] || continue
    if rg -n 'Execute\(ctx|map\[string\]any|DecodeRequestMap|ResponseFromMap' "$search_root" \
      --glob 'typed*.go' \
      --glob '!**/*test.go' >/dev/null 2>&1; then
      typed_adapter_violation="true"
      break
    fi
  done

  if [[ "$typed_adapter_violation" == "true" ]]; then
    echo "[FAIL] typed frame handlers use protobuf-native service methods"
    echo "  typed bootstrap must call generated-protobuf service methods directly; do not bridge through map executors"
    failed=1
  else
    echo "[OK] typed frame handlers use protobuf-native service methods"
  fi
}

check_repository_boundaries() {
  local service_root=""
  if [[ -d "$target/internal/service" ]]; then
    service_root="$target/internal/service"
  elif [[ -d "$target/backend/internal/service" ]]; then
    service_root="$target/backend/internal/service"
  fi
  [[ -n "$service_root" ]] || return 0

  local legacy_sql
  legacy_sql="$(rg -n '(^|[[:space:]])"database/sql"|\*sql\.|sql\.(DB|Tx|Rows|Row|ErrNoRows|Null)' "$service_root" \
    --glob '*.go' \
    --glob '!**/*test.go' \
    --glob '!**/testutil/**' 2>/dev/null || true)"
  if [[ -n "$legacy_sql" ]]; then
    echo "[FAIL] service repositories avoid database/sql"
    echo "  use Foundation/pgx executor interfaces for app repositories; database/sql stays out of service code"
    echo "$legacy_sql" | sed 's/^/  /' | head -20
    failed=1
  else
    echo "[OK] service repositories avoid database/sql"
  fi

  local service_sql
  service_sql="$(rg -n '(^|[^A-Za-z0-9_])(s|svc)\.db\.(BeginTx|QueryRow|Query|Exec)\(' "$service_root" \
    --glob 'service.go' \
    --glob '!**/*test.go' 2>/dev/null || true)"
  if [[ -n "$service_sql" ]]; then
    echo "[FAIL] service handlers avoid embedded SQL"
    echo "  move SQL and transaction bodies into repository methods; service.go should orchestrate domain behavior"
    echo "$service_sql" | sed 's/^/  /' | head -20
    failed=1
  else
    echo "[OK] service handlers avoid embedded SQL"
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
check_file_contains "golangci resource lint baseline" "$target/.golangci.yml" "gocognit"
check_exists "coding practices check" "$target/scripts/checks/coding_practices_check.sh"
check_exists "river practices check" "$target/scripts/checks/river_practices_check.sh"
check_exists "server-kit usage check" "$target/scripts/checks/server_kit_usage_check.sh"
check_exists "project scaffold check" "$target/scripts/checks/project_scaffold_check.sh"
check_exists "ci workflow" "$target/.github/workflows/ci.yml"
check_exists "security workflow" "$target/.github/workflows/security.yml"

check_absent "stale vendored foundation initializer" "$target/foundation/init.sh"
check_absent "stale vendored foundation updater" "$target/foundation/scripts/update-project.sh"
check_absent "unowned root pkg directory" "$target/pkg"
check_absent "legacy internal domain directory" "$target/internal/domain"
check_typed_proto_plane
check_repository_boundaries

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
  check_exists "managed recurring load test harness" "$target/tests/load/load_test.go"
  check_file_contains "load tests are opt-in" "$target/tests/load/load_test.go" "RUN_LOAD_TESTS"
  check_file_contains "load tests cover Redis path" "$target/tests/load/load_test.go" "opRedis"
  check_file_contains "load tests cover DB write path" "$target/tests/load/load_test.go" "opDBWrite"
  check_file_contains "load tests expose River queue state" "$target/tests/load/load_test.go" "fetchRiverStateCounts"
  check_file_contains "make integration target" "$target/Makefile" "test-integration"
  check_file_contains "make load target" "$target/Makefile" "test-load:"
  check_file_contains "make benchmark target" "$target/Makefile" "test-bench:"
  check_file_contains "make test database URL" "$target/Makefile" "TEST_DATABASE_URL"
  check_file_contains "make test Redis URL" "$target/Makefile" "TEST_REDIS_URL"
  check_file_contains "env test database URL" "$target/.env.example" "TEST_DATABASE_URL"
  check_file_contains "env test Redis URL" "$target/.env.example" "TEST_REDIS_URL"
  check_file_contains "env DB pool budget" "$target/.env.example" "DB_MAX_CONNS"
  check_file_contains "env DB query budget" "$target/.env.example" "DB_QUERY_TIMEOUT"
  check_file_contains "env Postgres 18 baseline" "$target/.env.example" "POSTGRES_VERSION=18"
  check_file_contains "env Redis pool budget" "$target/.env.example" "REDIS_POOL_SIZE"
  check_file_contains "env Redis shard extension" "$target/.env.example" "REDIS_SHARD_URLS"
  check_file_contains "env runtime shared memory mode" "$target/.env.example" "RUNTIME_SHARED_MEMORY"
  check_file_contains "env post quantum TLS mode" "$target/.env.example" "POST_QUANTUM_TLS_HYBRID_KEM"
  check_exists "foundation server-kit" "$target/foundation/server-kit/go/go.mod"
  check_exists "foundation metrics module" "$target/foundation/server-kit/go/metrics/metrics.go"
  check_exists "foundation grpc service module" "$target/foundation/server-kit/go/grpcsvc/grpcsvc.go"
  check_file_contains "foundation direct frame dispatch client" "$target/foundation/server-kit/go/grpcsvc/grpcsvc.go" "NewDirectFrameClient"
  check_file_contains "foundation binary frame registration" "$target/foundation/server-kit/go/grpcsvc/grpcsvc.go" "RegisterFrame"
  check_file_contains "foundation borrowed frame view" "$target/foundation/server-kit/go/grpcsvc/grpcsvc.go" "UnmarshalFrameView"
  check_file_contains "foundation typed frame handler projection" "$target/foundation/server-kit/go/bootstrap/frame_handlers.go" "RegisterTypedFrameHandlers"
  check_file_contains "foundation runtime initializes frame router" "$target/foundation/server-kit/go/startup/runtime.go" "FrameRouter"
  check_file_contains "foundation lane-aware DB pool defaults" "$target/foundation/server-kit/go/database/database.go" "DefaultPoolOptionsFor"
  check_file_contains "foundation Redis sharded client options" "$target/foundation/server-kit/go/redis/client.go" "ConnectWithOptions"
  check_file_contains "foundation worker cascades use bounded detached context" "$target/foundation/server-kit/go/worker/engine.go" "DetachedContextWithTimeout"
  check_file_contains "foundation worker bridge injects correlation" "$target/foundation/server-kit/go/worker/engine.go" "contextWithJobCorrelation"
  check_file_contains "foundation River job metadata cascades with jobs" "$target/foundation/server-kit/sql/river_setup.up.sql" "job_id bigint primary key references river_job(id) on delete cascade"
  check_exists "foundation performance check script" "$target/foundation/tooling/scripts/performance_check.sh"
  check_exists "foundation parallel chain module" "$target/foundation/server-kit/go/chain/chain.go"
  check_exists "foundation chaos module" "$target/foundation/server-kit/go/chaos/chaos.go"
  check_exists "foundation contract testing module" "$target/foundation/server-kit/go/contracttest/event_contract.go"
  check_exists "foundation profiling module" "$target/foundation/server-kit/go/profiling/profiling.go"
  check_exists "foundation SLO module" "$target/foundation/server-kit/go/slo/slo.go"
  check_file_contains "make server-kit usage check target" "$target/Makefile" "check-server-kit-usage:"
  check_file_contains "coding check enforces internal frame/protobuf lane" "$target/scripts/checks/coding_practices_check.sh" "CP internal Go avoids JSON gRPC compatibility dispatch"
  check_file_contains "server-kit check rejects internal JSON grpc dispatch" "$target/scripts/checks/server_kit_usage_check.sh" "internal code avoids JSON gRPC compatibility dispatch"
  check_any_startup_contains "startup initializes server-kit resilience" "resilience.New"
  check_any_startup_contains "startup registers server-kit resilience dependencies" "RegisterDependency("
  check_file_contains "server-kit usage check in foundation lint" "$target/Makefile" "check-server-kit-usage"
  check_exists "foundation runtime transport" "$target/foundation/runtime-transport/go/go.mod"
  check_exists "foundation config contracts" "$target/foundation/config-contracts/go/go.mod"
  check_exists "foundation tooling" "$target/foundation/tooling/docs/enforcement.md"
  check_exists "api README" "$target/api/README.md"
  check_exists "proto README" "$target/api/protos/README.md"
fi

if [[ "${WITH_DOCKER:-}" == "true" ]]; then
  check_exists "Dockerfile" "$target/Dockerfile"
  check_exists "docker-compose.yml" "$target/docker-compose.yml"
  check_exists "docker-compose.dev.yml" "$target/docker-compose.dev.yml"
  check_file_not_contains "dev Postgres 18 avoids legacy data mount" "$target/docker-compose.dev.yml" "/var/lib/postgresql/data"
  check_exists "docker ignore" "$target/.dockerignore"
  check_file_contains "compose Docker cache namespace" "$target/docker-compose.yml" "DOCKER_CACHE_NAMESPACE"
  check_exists "nginx template" "$target/config/default.conf.template"
  check_exists "nginx config" "$target/config/nginx.conf"
  check_file_contains "nginx COOP header" "$target/config/default.conf.template" "Cross-Origin-Opener-Policy"
  check_file_contains "nginx COEP header" "$target/config/default.conf.template" "Cross-Origin-Embedder-Policy"
  check_file_contains "nginx CORP header" "$target/config/default.conf.template" "Cross-Origin-Resource-Policy"
  check_file_contains "nginx wasm compression types" "$target/config/nginx.conf" "application/wasm"
  if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "backend" ]]; then
    check_exists "Dockerfile.migrate" "$target/Dockerfile.migrate"
    check_exists "docker-compose.test.yml" "$target/docker-compose.test.yml"
    check_file_contains "test Postgres image override" "$target/docker-compose.test.yml" "TEST_POSTGRES_IMAGE"
    check_file_not_contains "test Postgres 18 avoids legacy data mount" "$target/docker-compose.test.yml" "/var/lib/postgresql/data"
    check_file_contains "shared Docker Go module cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gomod'
    check_file_contains "shared Docker Go build cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gobuild'
    check_file_contains "Docker dependency stage" "$target/Dockerfile" "AS go-deps"
    check_exists "postgresql config" "$target/config/postgresql.conf"
    check_exists "redis config" "$target/config/redis.conf"
    check_file_contains "postgres timeout guardrail" "$target/config/postgresql.conf" "statement_timeout"
    check_file_contains "postgres autovacuum guardrail" "$target/config/postgresql.conf" "autovacuum_vacuum_scale_factor"
    check_file_contains "postgres async I/O baseline" "$target/config/postgresql.conf" "io_method"
    check_file_contains "postgres I/O observability baseline" "$target/config/postgresql.conf" "track_io_timing"
    check_file_contains "redis LFU eviction guardrail" "$target/config/redis.conf" "maxmemory-policy allkeys-lfu"
    check_file_contains "redis io thread baseline" "$target/config/redis.conf" "io-threads"
  fi
fi

if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "frontend" ]]; then
  frontend_root="$target/frontend"
  if [[ "${PROFILE:-}" == "frontend" ]]; then
    frontend_root="$target"
    if [[ -f "$target/frontend/package.json" ]]; then
      frontend_root="$target/frontend"
    fi
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
  check_file_contains "vite preserves workspace package symlinks" "$frontend_root/vite.config.ts" "preserveSymlinks: true"
  check_file_contains "vitest preserves workspace package symlinks" "$frontend_root/vitest.config.ts" "preserveSymlinks: true"
  check_file_contains "tsconfig preserves workspace package symlinks" "$frontend_root/tsconfig.app.json" "\"preserveSymlinks\": true"
  check_file_not_contains "vite does not alias ui-minimal source" "$frontend_root/vite.config.ts" "foundation/ui-minimal/ts/src"
  check_file_not_contains "vite does not alias runtime-transport source" "$frontend_root/vite.config.ts" "foundation/runtime-transport/ts/src"
  check_file_not_contains "vitest does not alias ui-minimal source" "$frontend_root/vitest.config.ts" "foundation/ui-minimal/ts/src"
  check_file_not_contains "vitest does not alias runtime-transport source" "$frontend_root/vitest.config.ts" "foundation/runtime-transport/ts/src"
  check_exists "frontend vite env" "$frontend_root/src/vite-env.d.ts"
  check_exists "frontend test setup" "$frontend_root/src/test/setup.ts"
  check_frontend_package_contains "frontend preview script" "$frontend_root/package.json" '"preview": "vite preview"'
  check_frontend_package_contains "frontend test script" "$frontend_root/package.json" '"test": "vitest run"'
  check_frontend_package_contains "frontend test watch script" "$frontend_root/package.json" '"test:watch": "vitest"'
  check_frontend_package_contains "frontend router dependency" "$frontend_root/package.json" '"react-router-dom"'
  check_frontend_package_contains "frontend styled-components dependency" "$frontend_root/package.json" '"styled-components"'
  check_frontend_package_contains "frontend zustand dependency" "$frontend_root/package.json" '"zustand"'
  check_frontend_package_contains "frontend jsdom dependency" "$frontend_root/package.json" '"jsdom"'
  check_frontend_package_contains "frontend testing library react" "$frontend_root/package.json" '"@testing-library/react"'
  check_frontend_package_contains "frontend testing library jest dom" "$frontend_root/package.json" '"@testing-library/jest-dom"'
  check_frontend_package_contains "frontend testing library user event" "$frontend_root/package.json" '"@testing-library/user-event"'
  check_frontend_package_contains "frontend runtime transport package" "$frontend_root/package.json" '"@ovasabi/runtime-transport"'
  check_frontend_package_contains "frontend kit package" "$frontend_root/package.json" '"@ovasabi/frontend-kit"'
  check_frontend_package_contains "frontend ui minimal package" "$frontend_root/package.json" '"@ovasabi/ui-minimal"'
  check_file_contains "make proto-ts target" "$target/Makefile" "proto-ts:"
  check_file_contains "make foundation transport proto target" "$target/Makefile" "foundation-transport-proto:"
  check_file_contains "make communication contract aggregate" "$target/Makefile" "communication-contracts:"
  check_file_contains "proto-ts writes generated app contracts" "$target/Makefile" "--ts_proto_out=frontend/src/types/protos"
fi

if [[ "${WITH_WASM:-false}" == "true" ]]; then
  check_exists "rust clippy baseline" "$target/clippy.toml"
  check_exists "rustfmt baseline" "$target/rustfmt.toml"
  check_exists "runtime sdk" "$target/foundation/runtime-sdk/go/go.mod"
  check_exists "runtime shared arena schema" "$target/foundation/runtime-sdk/protocols/system/v1/runtime_shared_arena.capnp"
  check_exists "runtime shared arena host API" "$target/foundation/runtime-sdk/ts/browser-host/src/arena.ts"
  check_exists "runtime payload router API" "$target/foundation/runtime-sdk/ts/browser-host/src/payloadRouter.ts"
  check_file_contains "runtime ffi macro exports ovrt-core" "$target/foundation/runtime-sdk/rust/crates/ovrt-ffi/src/lib.rs" "pub use ovrt_core;"
  check_file_contains "runtime ffi pool reuses fixed buffer" "$target/foundation/runtime-sdk/go/runtimehost/ffi_unix.go" "bufferPool"
  check_file_contains "runtime ffi pool reuses error buffer" "$target/foundation/runtime-sdk/go/runtimehost/ffi_unix.go" "errBufPool"
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
