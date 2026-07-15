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

check_regex() {
  local label="$1"
  local actual="$2"
  local pattern="$3"
  if printf '%s\n' "$actual" | grep -Eq "$pattern"; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  expected pattern: $pattern"
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

check_file_contains_any() {
  local label="$1"
  local file="$2"
  shift 2
  local pattern
  for pattern in "$@"; do
    if [[ -e "$file" ]] && grep -RFq -- "$pattern" "$file"; then
      echo "[OK] $label"
      return
    fi
  done
  echo "[FAIL] $label"
  echo "  missing supported pattern: $*"
  echo "  file: ${file#$target/}"
  failed=1
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

check_any_server_contains() {
  local label="$1"
  local pattern="$2"
  local found="false"
  for file in "$target/foundation/server-kit/go/httpserver/server.go" "$target/cmd/server/main.go" "$target/backend/foundation/server-kit/go/httpserver/server.go" "$target/backend/cmd/server/main.go"; do
    if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
      found="true"
      break
    fi
  done
  if [[ "$found" == "true" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing server pattern: $pattern"
    failed=1
  fi
}

first_schema_migration() {
  find "$target/migrations" -maxdepth 1 -type f \
    \( -name '000001*up.sql' -o -name '0001*up.sql' \) 2>/dev/null | sort | head -n 1
}

check_typed_proto_plane() {
  local proto_root="$target/api/protos"
  [[ -d "$proto_root" ]] || return 0

  local has_domain_proto="false"
  local proto_file
  while IFS= read -r proto_file; do
    case "$proto_file" in
      */_template/*|*/common/*|*/foundation/*|*/transport/*|*/hermes/*) continue ;;
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
    if rg -n 'ProtoHandler|typedHandler|GetTypedHandlers|BuildTypedServiceHandlers|func \(s \*Service\) [A-Za-z0-9_]+V1\(ctx context\.Context, req \*.*v1\.[A-Za-z0-9_]+Request\)' "$search_root" \
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

# Architecture-wide vendored-surface verification: the server-kit module list
# is data-driven from tooling/server_kit_module_manifest.tsv rather than
# hand-enumerated here, so new foundation modules get fleet verification by
# adding one manifest row.
check_server_kit_module_parity() {
  [[ -d "$target/foundation/server-kit/go" ]] || return 0
  local script=""
  local candidate
  for candidate in "$target/scripts/checks/server_kit_module_parity_check.sh" "$target/foundation/tooling/scripts/server_kit_module_parity_check.sh"; do
    if [[ -f "$candidate" ]]; then
      script="$candidate"
      break
    fi
  done
  if [[ -z "$script" ]]; then
    echo "[FAIL] server-kit module parity check available"
    echo "  missing scripts/checks/server_kit_module_parity_check.sh; re-run foundation update"
    failed=1
    return
  fi
  local output
  if output="$(bash "$script" "$target" 2>&1)"; then
    echo "[OK] server-kit module surface matches foundation manifest"
  else
    echo "[FAIL] server-kit module surface matches foundation manifest"
    printf '%s\n' "$output" | grep -v '^\[OK\]' | sed 's/^/  /' | head -20
    failed=1
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

check_service_domain_contracts() {
  local service_root=""
  if [[ -d "$target/internal/service" ]]; then
    service_root="$target/internal/service"
  elif [[ -d "$target/backend/internal/service" ]]; then
    service_root="$target/backend/internal/service"
  fi
  [[ -n "$service_root" ]] || return 0

  local proto_root="$target/api/protos"
  local violations=""
  local service_dir service_name proto_dir
  while IFS= read -r service_dir; do
    service_name="$(basename "$service_dir")"
    case "$service_name" in
      .*|_*|common|transport) continue ;;
    esac
    proto_dir="$proto_root/$service_name"
    if [[ ! -d "$proto_dir" ]] || ! find "$proto_dir" -maxdepth 3 -type f -name '*.proto' 2>/dev/null | grep -q .; then
      violations+="${service_dir#$target/} -> missing api/protos/$service_name contract"$'\n'
      continue
    fi
    if ! rg -n '^[[:space:]]*(service|message)[[:space:]]+' "$proto_dir" --glob '*.proto' >/dev/null 2>&1; then
      violations+="${service_dir#$target/} -> api/protos/$service_name has no message/service contract"$'\n'
    fi
  done < <(find "$service_root" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort)

  if [[ -n "$violations" ]]; then
    echo "[FAIL] service folders map to domain proto contracts"
    echo "  internal/service/<name> is reserved for actual app domains; shared primitives belong in Foundation/server-kit or non-service internal packages"
    echo "$violations" | sed '/^$/d; s/^/  /' | head -40
    failed=1
  else
    echo "[OK] service folders map to domain proto contracts"
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
check_value "manifest baseline generation" "${BASELINE_GENERATION:-}" "manifest-v4"
check_regex "native project identifier format" "${PROJECT_IDENTIFIER:-com.ovasabi.foundation-app}" '^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*){2,}$'
check_not_symlink "foundation directory is local" "$target/foundation"
check_exists "cursor rules" "$target/.cursorrules"
check_exists "agents guide" "$target/.agents/DOMAIN_GUIDE.md"
check_exists "post-init checklist" "$target/.agents/POST_INIT.md"
check_exists "foundation guide" "$target/docs/foundation/foundation_guide.md"
check_exists "foundation architecture contract" "$target/docs/foundation/foundation_architecture_contract.md"
check_exists "foundation tour" "$target/docs/foundation/foundation_tour.md"
check_exists "agent operating contract" "$target/docs/foundation/agent_operating_contract.md"
check_exists "practice controls doc" "$target/docs/foundation/practice_controls.md"
check_exists "AI threat model doc" "$target/docs/foundation/ai_threat_model.md"
check_exists "performance lab doc" "$target/docs/foundation/performance_lab.md"
check_exists "projection freshness contract doc" "$target/docs/foundation/projection_freshness_contract.md"
check_exists "future practices research ledger" "$target/docs/foundation/future_practices_research.md"
check_exists "TLA worker retry queue template" "$target/docs/foundation/specs/tla/WorkerRetryQueue.tla"
check_exists "TLA cache projection freshness template" "$target/docs/foundation/specs/tla/CacheProjectionFreshness.tla"
check_exists "TLA WebSocket backpressure template" "$target/docs/foundation/specs/tla/WebSocketBackpressure.tla"
check_exists "foundation scaffold manifest guide" "$target/docs/foundation/scaffold_manifest.md"
check_exists "coding practices" "$target/docs/foundation/coding_practices.md"
check_exists "testing practices" "$target/docs/foundation/testing_practices.md"
check_exists "post quantum security posture" "$target/docs/foundation/post_quantum_security.md"
check_file_contains "agents guide references agent contract" "$target/AGENTS.md" "agent_operating_contract.md"
check_file_contains "project README references agent-native workflow" "$target/README.md" "Agent-Native Workflow"
check_file_contains "project Makefile exposes docs reference check" "$target/Makefile" "check-doc-references"
check_file_contains "project Makefile exposes lifecycle manifest check" "$target/Makefile" "check-lifecycle-manifest"
check_file_contains "project Makefile exposes app security profile check" "$target/Makefile" "check-app-security-profile"
check_exists "golangci baseline" "$target/.golangci.yml"
check_file_contains "golangci resource lint baseline" "$target/.golangci.yml" "gocognit"
check_exists "agent contract check" "$target/scripts/checks/agent_contract_check.sh"
check_exists "docs reference check" "$target/scripts/checks/docs_reference_check.mjs"
check_exists "practice controls check" "$target/scripts/checks/practice_controls_check.sh"
check_exists "lifecycle manifest check" "$target/scripts/checks/check_lifecycle_manifest.sh"
check_exists "lifecycle manifest generator" "$target/scripts/checks/generate_lifecycle_manifest.mjs"
check_exists "app security profile check" "$target/scripts/checks/app_security_profile_check.sh"
check_exists "app-owned security profile" "$target/docs/security/profile.md"
check_exists "runtime performance contract check" "$target/scripts/checks/runtime_performance_contract_check.sh"
check_exists "formal methods check" "$target/scripts/checks/formal_methods_check.sh"
check_exists "operational excellence check" "$target/scripts/checks/operational_excellence_check.sh"
check_exists "coding practices check" "$target/scripts/checks/coding_practices_check.sh"
check_exists "testing practices check" "$target/scripts/checks/testing_practices_check.sh"
check_exists "go fix modernization check" "$target/scripts/checks/go_fix_check.sh"
check_exists "go static analysis check" "$target/scripts/checks/go_static_analysis_check.sh"
check_exists "local toolchain environment check helper" "$target/scripts/checks/local_toolchain_env.sh"
check_exists "go concurrency practices check" "$target/scripts/checks/go_concurrency_practices_check.sh"
check_exists "frontend script runner" "$target/scripts/checks/frontend_script_runner.sh"
check_exists "logging practices check" "$target/scripts/checks/logging_practices_check.sh"
check_exists "metadata practices check" "$target/scripts/checks/metadata_practices_check.sh"
check_exists "dynamic payload practices check" "$target/scripts/checks/dynamic_payload_practices_check.sh"
check_exists "river practices check" "$target/scripts/checks/river_practices_check.sh"
check_exists "server-kit module contract check" "$target/scripts/checks/server_kit_module_contract_check.sh"
check_exists "server-kit usage check" "$target/scripts/checks/server_kit_usage_check.sh"
check_exists "project scaffold check" "$target/scripts/checks/project_scaffold_check.sh"
check_exists "ci workflow" "$target/.github/workflows/ci.yml"
check_exists "security workflow" "$target/.github/workflows/security.yml"
check_exists "operations bootstrap" "$target/docs/operations/README.md"
check_exists "incident record template" "$target/docs/operations/incident_record_template.md"
check_exists "migration log template" "$target/docs/operations/migration_log.md"
check_file_contains "ci uses Go 1.26 baseline" "$target/.github/workflows/ci.yml" 'GO_VERSION: "1.26"'
check_file_contains "security workflow uses Go 1.26 baseline" "$target/.github/workflows/security.yml" 'go-version: "1.26"'
check_file_contains "ci runs Go coverage gate" "$target/.github/workflows/ci.yml" "make test-coverage"
check_file_contains "ci captures delivery metrics" "$target/.github/workflows/ci.yml" "ci_delivery_metrics.mjs"
check_file_contains "ci uploads delivery metrics artifact" "$target/.github/workflows/ci.yml" "delivery-metrics/ci-event.json"

check_absent "stale vendored foundation initializer" "$target/foundation/init.sh"
check_absent "stale vendored foundation updater" "$target/foundation/scripts/update-project.sh"
check_absent "unowned root pkg directory" "$target/pkg"
check_absent "legacy internal domain directory" "$target/internal/domain"
check_absent "stale root server-kit module" "$target/server-kit"
check_absent "stale root runtime-sdk module" "$target/runtime-sdk"
check_absent "stale root runtime-transport module" "$target/runtime-transport"
check_absent "stale root config-contracts module" "$target/config-contracts"
check_typed_proto_plane
check_repository_boundaries
check_service_domain_contracts
check_server_kit_module_parity

if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "backend" ]]; then
  check_exists "server command" "$target/cmd/server/main.go"
  # Standardization drift guard: the HTTP server is foundation-owned
  # (server-kit/go/httpserver, module-synced). A project must consume it and must
  # not carry its own divergent server. See docs/foundation_project_standardization.md.
  check_file_contains "server command uses foundation httpserver" "$target/cmd/server/main.go" "server-kit/go/httpserver"
  check_absent "project carries no divergent internal/server" "$target/internal/server/server.go"
  check_absent "project carries no divergent backend/internal/server" "$target/backend/internal/server/server.go"
  check_exists "Go workspace" "$target/go.work"
  check_file_contains "Go workspace includes project module" "$target/go.work" "."
  check_file_contains "Go workspace includes foundation server-kit" "$target/go.work" "./foundation/server-kit/go"
  check_file_contains "Go workspace includes foundation runtime transport" "$target/go.work" "./foundation/runtime-transport/go"
  check_file_contains "Go workspace includes foundation config contracts" "$target/go.work" "./foundation/config-contracts/go"
  check_exists "worker command" "$target/cmd/worker/main.go"
  check_exists "docgen command" "$target/cmd/docgen/main.go"
  check_exists "docgen helper tests" "$target/cmd/docgen/helpers_test.go"
  check_file_contains "Makefile exposes OpenAPI generation" "$target/Makefile" "docgen:"
  check_file_contains "server serves API docs through Foundation" "$target/foundation/server-kit/go/httpserver/server.go" "server-kit/go/apidocs"
  check_file_contains "server stores API docs handler" "$target/foundation/server-kit/go/httpserver/server.go" "apiDocs"
  check_file_contains "server registers API docs endpoints" "$target/foundation/server-kit/go/httpserver/server.go" "s.apiDocs.Register"
  check_file_contains "server makes OpenAPI spec public" "$target/foundation/server-kit/go/httpserver/server.go" '"/openapi.json"'
  check_file_contains "server makes API docs UI public" "$target/foundation/server-kit/go/httpserver/server.go" '"/docs"'
  if [[ -f "$target/Dockerfile" ]]; then
    check_file_contains "Docker server image generates OpenAPI spec" "$target/Dockerfile" "go run ./cmd/docgen > /tmp/openapi.json"
    check_file_contains "Docker server image embeds OpenAPI spec" "$target/Dockerfile" "COPY --from=builder /tmp/openapi.json ./openapi.json"
  fi
  # The project's HTTP surface is declared once, in bootstrap.Services: a single
  # HTTPRoutes() catalogue consumed by both the server (SetHTTPRoutes) and docgen
  # (OpenAPI). The default derives routes from handlers; domains override
  # HTTPRoutes to aggregate explicit per-service routes. See
  # docs/foundation_project_standardization.md.
  check_file_contains_any "bootstrap declares the HTTP route catalogue" "$target/internal/bootstrap" "func (s *Services) HTTPRoutes()" "func RouteCatalog()" "func RouteCatalogHandlers()"
  check_file_contains "server command installs the route catalogue" "$target/cmd/server/main.go" "SetHTTPRoutes"
  check_file_contains_any "server command sources routes from bootstrap catalogue" "$target/cmd/server/main.go" ".HTTPRoutes()" "RouteCatalog()"
  check_file_contains_any "docgen consumes the route catalogue" "$target/cmd/docgen/main.go" "RouteCatalog()" "RouteCatalogHandlers()"
  check_file_not_contains "docgen avoids empty route catalogue" "$target/cmd/docgen/main.go" "Routes: []registry.HTTPRoute{}"
  check_file_not_contains "docgen avoids route TODO scaffold" "$target/cmd/docgen/main.go" "TODO: Import your domain handlers"
  check_file_not_contains "docgen avoids hand-maintained route collector" "$target/cmd/docgen/main.go" "func collectRoutes"
  check_file_not_contains "server avoids hand-registering service routes" "$target/foundation/server-kit/go/httpserver/server.go" ".RegisterRoutes(api)"
  check_exists "worker helper tests" "$target/cmd/worker/helpers_test.go"
  check_exists "startup package" "$target/internal/startup"
  check_exists "startup smoke tests" "$target/internal/startup/startup_test.go"
  check_exists "bootstrap services container" "$target/internal/bootstrap/services.go"
  check_exists "config scaffold tests" "$target/internal/config/config_test.go"
  check_exists "server scaffold tests" "$target/foundation/server-kit/go/httpserver/server_test.go"
  check_exists "server middleware scaffold tests" "$target/foundation/server-kit/go/httpserver/middleware/middleware_test.go"
  check_exists "river worker registry" "$target/internal/worker/registry.go"
  check_exists "river worker helper tests" "$target/internal/worker/registry_helpers_test.go"
  check_exists "river worker registry tests" "$target/internal/worker/registry_test.go"
  check_exists "river periodic jobs" "$target/internal/worker/periodic_jobs.go"
  check_exists "integration infra test" "$target/tests/integration/infra_test.go"
  check_exists "integration Hermes hotplane test" "$target/tests/integration/hermes_test.go"
  check_exists "integration test DB helper" "$target/tests/integration/setup_helpers_test.go"
  check_file_contains "Hermes integration covers Postgres rebuild" "$target/tests/integration/hermes_test.go" "store.Rebuild"
  check_file_contains "Hermes integration covers Redis stream envelope" "$target/tests/integration/hermes_test.go" "NewRedisStreamEnvelopeSource"
  check_file_contains "Hermes integration uses typed record values" "$target/tests/integration/hermes_test.go" "database.RecordValueFromAny"
  check_file_contains "Hermes integration uses typed Redis stream values" "$target/tests/integration/hermes_test.go" "rediskit.Values{rediskit.Field(\"envelope\", raw)}"
  check_file_not_contains "Hermes integration avoids removed RecordDataFromMap helper" "$target/tests/integration/hermes_test.go" "RecordDataFromMap"
  check_file_not_contains "Hermes integration avoids removed RecordQueryFromMap helper" "$target/tests/integration/hermes_test.go" "RecordQueryFromMap"
  check_file_contains "managed test env defaults" "$target/tests/testutil/env.go" "ApplyTestEnvDefaults"
  check_file_contains "managed test infra required flag" "$target/tests/testutil/env.go" "TEST_INFRA_REQUIRED"
  check_exists "testutil scaffold tests" "$target/tests/testutil/storage_env_test.go"
  check_exists "managed recurring load test harness" "$target/tests/load/load_test.go"
  check_file_contains "load tests are opt-in" "$target/tests/load/load_test.go" "RUN_LOAD_TESTS"
  check_file_contains "load tests cover Redis path" "$target/tests/load/load_test.go" "opRedis"
  check_file_contains "load tests cover DB write path" "$target/tests/load/load_test.go" "opDBWrite"
  check_file_contains "load tests expose River queue state" "$target/tests/load/load_test.go" "fetchRiverStateCounts"
  check_file_contains "load tests report latency distribution" "$target/tests/load/load_test.go" "Latency Distribution: p50<="
  check_file_contains "load tests prewarm measured steps" "$target/tests/load/load_test.go" "prewarmLoadStep(ctx, t, env, concurrency, opTimeout)"
  check_file_contains "load tests prepare write table outside hot loop" "$target/tests/load/load_test.go" "prepareLoadArtifacts(ctx, t, env, opTimeout)"
  check_file_not_contains "load tests avoid nolint suppressions" "$target/tests/load/load_test.go" "nolint"
  check_file_not_contains "load tests avoid weak random traffic mix" "$target/tests/load/load_test.go" "math/rand"
  check_file_not_contains "load tests avoid hot-loop temp table DDL" "$target/tests/load/load_test.go" "CREATE TEMP TABLE"
  check_file_contains "make integration target" "$target/Makefile" "test-integration"
  check_file_contains "make local full target" "$target/Makefile" "test-local-full: communication-contracts test-unit test-wasm test-frontend test-integration test-e2e"
  check_file_contains "make local services target" "$target/Makefile" "test-local-services: test-local-full test-load test-bench"
  check_file_contains "make local full covers infrastructure" "$target/Makefile" "Local full test path complete"
  check_file_contains "make local services marks opt-in evidence" "$target/Makefile" "Opt-in local service evidence path complete"
  check_file_contains "make coverage target" "$target/Makefile" "test-coverage:"
  check_file_contains "make coverage threshold" "$target/Makefile" "GO_COVERAGE_THRESHOLD ?= 95.0"
  check_file_contains "make load target" "$target/Makefile" "test-load:"
  check_file_contains "make benchmark target" "$target/Makefile" "test-bench:"
  check_file_contains "make test database URL" "$target/Makefile" "TEST_DATABASE_URL"
  check_file_contains "make test Redis URL" "$target/Makefile" "TEST_REDIS_URL"
  check_file_contains "make auto-selects PostGIS test image when migrations need it" "$target/Makefile" "TEST_REQUIRES_POSTGIS ?="
  check_file_contains "make supports service-scoped PostGIS test image platform" "$target/Makefile" "TEST_POSTGRES_PLATFORM"
  check_file_contains "env test database URL" "$target/.env.example" "TEST_DATABASE_URL"
  check_file_contains "env test Redis URL" "$target/.env.example" "TEST_REDIS_URL"
  check_file_contains "env DB pool budget" "$target/.env.example" "DB_MAX_CONNS"
  check_file_contains "env Hermes scope record bound" "$target/.env.example" "HERMES_MAX_RECORDS_PER_SCOPE"
  check_file_contains "env Hermes scope byte bound" "$target/.env.example" "HERMES_MAX_BYTES_PER_SCOPE"
  check_file_contains "env DB query budget" "$target/.env.example" "DB_QUERY_TIMEOUT"
  schema_migration="$(first_schema_migration)"
  check_file_contains "migration includes durable event log" "$schema_migration" "foundation_event_log"
  check_file_contains "migration keeps event log typed" "$schema_migration" "envelope BYTEA NOT NULL"
  check_file_contains "migration includes event log publish claim lease" "$schema_migration" "publish_claim_expires_at"
  check_file_contains "migration separates logs from durable facts" "$schema_migration" "Operational logs must not feed Hermes"
  check_file_contains "env Postgres 18 baseline" "$target/.env.example" "POSTGRES_VERSION=18"
  check_file_contains "env Redis pool budget" "$target/.env.example" "REDIS_POOL_SIZE"
  check_file_contains "env Redis shard extension" "$target/.env.example" "REDIS_SHARD_URLS"
  check_file_contains "env Redis read timeout" "$target/.env.example" "REDIS_READ_TIMEOUT"
  check_file_contains "env runtime shared memory mode" "$target/.env.example" "RUNTIME_SHARED_MEMORY"
  check_file_contains "env post quantum TLS mode" "$target/.env.example" "POST_QUANTUM_TLS_HYBRID_KEM"
  check_exists "foundation server-kit" "$target/foundation/server-kit/go/go.mod"
  check_absent "foundation service-backed core harness" "$target/foundation/server-kit/go/servicebacked"
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
  check_file_contains "foundation pgx pool options helper" "$target/foundation/server-kit/go/database/postgres.go" "ApplyPoolOptions"
  check_file_contains "foundation trace handler" "$target/foundation/server-kit/go/observability/http.go" "TraceHandler"
  check_exists "foundation delivery metrics practice" "$target/docs/foundation/delivery_metrics_practices.md"
  check_exists "delivery metrics collector" "$target/scripts/checks/ci_delivery_metrics.mjs"
  check_file_contains "foundation River job metadata cascades with jobs" "$target/foundation/server-kit/sql/river_setup.up.sql" "job_id bigint primary key references river_job(id) on delete cascade"
  check_exists "foundation performance check script" "$target/foundation/tooling/scripts/performance_check.sh"
  check_exists "foundation practice controls matrix" "$target/foundation/tooling/practice_controls.psv"
  check_exists "foundation practice controls check script" "$target/foundation/tooling/scripts/practice_controls_check.sh"
  check_exists "foundation runtime performance contract check script" "$target/foundation/tooling/scripts/runtime_performance_contract_check.sh"
  check_exists "foundation formal methods check script" "$target/foundation/tooling/scripts/formal_methods_check.sh"
  check_exists "foundation operational excellence check script" "$target/foundation/tooling/scripts/operational_excellence_check.sh"
  check_exists "foundation Rust static analysis script" "$target/foundation/tooling/scripts/rust_static_analysis_check.sh"
  check_exists "foundation Rust runtime practices script" "$target/foundation/tooling/scripts/rust_runtime_practices_check.sh"
  check_exists "project combined Rust check script" "$target/scripts/checks/check-rust.sh"
  check_exists "project Rust static analysis script" "$target/scripts/checks/rust_static_analysis_check.sh"
  check_exists "project Rust runtime practices script" "$target/scripts/checks/rust_runtime_practices_check.sh"
  check_exists "foundation Vitest Node runtime runner" "$target/foundation/tooling/scripts/run_vitest.sh"
  check_exists "foundation lifecycle contract generator" "$target/foundation/tooling/scripts/generate_lifecycle_contract_tests.mjs"
  check_exists "foundation hermes snapshot tier" "$target/foundation/server-kit/go/hermes/snapshot_tier.go"
  check_exists "foundation hermes snapshot writer" "$target/foundation/server-kit/go/hermes/snapshot_writer.go"
  check_exists "foundation objectstore snapshot store" "$target/foundation/server-kit/go/hermessnapshot/store.go"
  check_exists "foundation parallel chain module" "$target/foundation/server-kit/go/chain/chain.go"
  check_exists "foundation chaos module" "$target/foundation/server-kit/go/chaos/chaos.go"
  check_exists "foundation contract testing module" "$target/foundation/server-kit/go/contracttest/event_contract.go"
  check_exists "foundation profiling module" "$target/foundation/server-kit/go/profiling/profiling.go"
  check_exists "foundation SLO module" "$target/foundation/server-kit/go/slo/slo.go"
  check_exists "foundation portable disk health check" "$target/foundation/server-kit/go/healthcheck/disk_space_unsupported.go"
  check_file_not_contains "foundation healthcheck avoids common syscall statfs" "$target/foundation/server-kit/go/healthcheck/healthcheck.go" "syscall.Statfs"
  check_file_contains "make agent contract check target" "$target/Makefile" "check-agent-contract:"
  check_file_contains "make practice controls check target" "$target/Makefile" "check-practice-controls:"
  check_file_contains "make runtime performance contract target" "$target/Makefile" "check-runtime-performance-contracts:"
  check_file_contains "make formal methods target" "$target/Makefile" "check-formal-methods:"
  check_file_contains "make operational excellence target" "$target/Makefile" "check-operational-excellence:"
  check_file_contains "make server-kit usage check target" "$target/Makefile" "check-server-kit-usage:"
  check_file_contains "make Go concurrency review target" "$target/Makefile" "check-go-concurrency-practices:"
  check_file_contains "make logging practices target" "$target/Makefile" "check-logging-practices:"
  check_file_contains "make testing practices target" "$target/Makefile" "check-testing-practices:"
  check_file_contains "coding check enforces internal frame/protobuf lane" "$target/scripts/checks/coding_practices_check.sh" "CP internal Go avoids JSON gRPC compatibility dispatch"
  check_file_contains "testing check blocks focused TypeScript tests" "$target/scripts/checks/testing_practices_check.sh" "TE no focused TypeScript tests"
  check_file_contains "agent check enforces evidence ledger" "$target/scripts/checks/agent_contract_check.sh" "Evidence Ledger"
  check_file_contains "practice controls check maps CP rules" "$target/scripts/checks/practice_controls_check.sh" "control mapped"
  check_file_contains "runtime performance check maps pprof and trace" "$target/scripts/checks/runtime_performance_contract_check.sh" "Go pprof/trace"
  check_file_contains "formal methods check maps TLA templates" "$target/scripts/checks/formal_methods_check.sh" "WorkerRetryQueue"
  check_file_contains "operational excellence check maps DORA SPACE SLSA" "$target/scripts/checks/operational_excellence_check.sh" "supply_chain"
  check_file_contains "logging check blocks raw slog drift" "$target/scripts/checks/logging_practices_check.sh" "application code avoids raw slog imports"
  check_file_contains "logging check requires Foundation declarative logger install" "$target/scripts/checks/logging_practices_check.sh" "logger.Install"
  check_file_contains "dynamic payload check blocks map any in product hot paths" "$target/scripts/checks/dynamic_payload_practices_check.sh" "dynamic payload maps are not allowed in scaffolded product hot paths"
  check_file_contains "lint-foundation includes go fix modernization" "$target/Makefile" "check-go-fix"
  check_file_contains "make exposes combined Rust check target" "$target/Makefile" "check-rust:"
  check_file_contains "lint-foundation includes Go static analysis" "$target/Makefile" "check-go-static-analysis"
  check_file_contains "lint-foundation includes Rust static analysis" "$target/Makefile" "check-rust-static-analysis"
  check_file_contains "lint-foundation includes Rust runtime practices" "$target/Makefile" "check-rust-runtime-practices"
  check_file_contains "lint-foundation includes logging practices" "$target/Makefile" "check-logging-practices"
  check_file_contains "lint-foundation includes metadata practices" "$target/Makefile" "check-metadata-practices"
  check_file_contains "lint-foundation includes dynamic payload practices" "$target/Makefile" "check-dynamic-payload-practices"
  check_file_contains "lint-foundation includes server-kit module contract" "$target/Makefile" "check-server-kit-module-contract"
  check_file_contains "make resolves Docker CLI centrally" "$target/Makefile" "DOCKER_BIN ?="
  check_file_contains "make isolates test Compose project" "$target/Makefile" "TEST_COMPOSE_PROJECT_NAME ?="
  check_file_contains "make uses isolated test Compose project" "$target/Makefile" 'compose -p $(TEST_COMPOSE_PROJECT_NAME)'
  check_file_contains "integration tests clean started Docker test env" "$target/Makefile" "started_test_env=1"
  check_file_contains "test env cleanup removes orphan containers" "$target/Makefile" "down -v --remove-orphans"
  check_file_contains "make delegates frontend scripts to Foundation runner" "$target/Makefile" "FRONTEND_SCRIPT_RUNNER ?="
  check_file_contains "make leaves Vitest worker count unforced by default" "$target/Makefile" "FOUNDATION_VITEST_WORKERS ?= 0"
  check_file_contains "make serializes frontend tests by default" "$target/Makefile" "FOUNDATION_VITEST_SERIAL ?= 1"
  check_file_contains "server-kit check rejects internal JSON grpc dispatch" "$target/scripts/checks/server_kit_usage_check.sh" "internal code avoids JSON gRPC compatibility dispatch"
  check_file_contains "server-kit check rejects legacy map handler bridges" "$target/scripts/checks/server_kit_usage_check.sh" "internal code avoids legacy map handler bridges"
  check_file_contains "server-kit check rejects map-return object handlers" "$target/scripts/checks/server_kit_usage_check.sh" "internal code avoids map-return object handlers"
  check_file_contains "server-kit check rejects ObjectResult bridges" "$target/scripts/checks/server_kit_usage_check.sh" "internal code avoids ObjectResult map bridges"
  check_file_contains "server-kit check rejects generated typed wrappers" "$target/scripts/checks/server_kit_usage_check.sh" "bootstrap avoids generated typed wrapper files"
  check_file_contains "server-kit check rejects app adapter shapes" "$target/scripts/checks/server_kit_usage_check.sh" "application internals avoid adapter package/file shapes"
  check_file_contains "startup installs Foundation logger" "$target/internal/startup/logger.go" "server-kit/go/logger"
  check_file_contains "startup installs Foundation runtime logger" "$target/internal/startup/logger.go" "logger.Install"
  check_file_contains "startup declares Foundation runtime logger scope" "$target/internal/startup/logger.go" "logger.RuntimeConfig"
  check_file_contains "server uses Foundation logger type" "$target/foundation/server-kit/go/httpserver/server.go" "kitlogger.Logger"
  check_file_contains "middleware injects log metadata" "$target/foundation/server-kit/go/httpserver/middleware/middleware.go" "metadata.IntoContext"
  check_file_contains "middleware propagates correlation header" "$target/foundation/server-kit/go/httpserver/middleware/middleware.go" "X-Correlation-ID"
  check_any_startup_contains "startup logs database and Hermes readiness" "\"hermes\", \"enabled\""
  check_any_startup_contains "startup logs Redis event bus readiness" "redis event bus connected"
  check_any_startup_contains "startup initializes server-kit resilience" "resilience.New"
  check_any_startup_contains "startup registers server-kit resilience dependencies" "RegisterDependency("
  check_any_startup_contains "startup initializes mandatory Hermes" "hermes.WrapRuntimeStore"
  check_any_startup_contains "startup exposes Hermes dependency" "Hermes"
  check_any_startup_contains "startup registers Hermes health" "HermesHealth"
  check_any_server_contains "server logs listening state" "server listening"
  check_file_contains "server logs route registration" "$target/foundation/server-kit/go/httpserver/server.go" "registered route"
  check_file_contains "server-kit usage check in foundation lint" "$target/Makefile" "check-server-kit-usage"
  check_file_contains "make lifecycle contract generation target" "$target/Makefile" "lifecycle-contracts:"
  check_file_contains "make delivery metrics target" "$target/Makefile" "delivery-metrics:"
  check_file_contains "worker uses foundation pool options" "$target/cmd/worker/main.go" "database.ApplyPoolOptions"
  check_file_contains "worker uses background DB lane" "$target/cmd/worker/main.go" "RuntimeLaneBackground"
  check_file_contains "worker propagates DB acquire timeout" "$target/cmd/worker/main.go" "DBAcquireTimeout"
  check_file_contains "worker default queue concurrency is configurable" "$target/internal/worker/registry.go" "QUEUE_WORKERS_DEFAULT"
  check_file_contains "worker processing queue concurrency is configurable" "$target/internal/worker/registry.go" "QUEUE_WORKERS_PROCESSING"
  check_file_contains "worker scheduled queue concurrency is configurable" "$target/internal/worker/registry.go" "QUEUE_WORKERS_SCHEDULED"
  check_file_contains "server exposes correlation trace endpoint" "$target/foundation/server-kit/go/httpserver/server.go" "/metricsz/trace"
  check_file_contains "server wires CORS middleware" "$target/foundation/server-kit/go/httpserver/server.go" "security.CORS("
  check_file_contains "server uses configured CORS origins" "$target/foundation/server-kit/go/httpserver/server.go" "allowedOrigins"
  check_file_not_contains "server avoids wildcard CORS default" "$target/foundation/server-kit/go/httpserver/server.go" 'security.CORS([]string{"*"})'
  check_file_contains "server protects operational endpoints" "$target/foundation/server-kit/go/httpserver/server.go" "operationalHandler"
  check_file_contains "websocket reserves capacity before upgrade" "$target/foundation/server-kit/go/httpserver/websocket.go" "reserveWSConnectionSlot"
  check_file_contains "websocket registration uses reserved capacity slots" "$target/foundation/server-kit/go/httpserver/websocket.go" "reserved:  true"
  check_file_contains "websocket queue full records failed message metric" "$target/foundation/server-kit/go/httpserver/websocket.go" "RecordMessageFailed()"
  check_file_contains "server tests cover websocket capacity rejection" "$target/foundation/server-kit/go/httpserver/server_test.go" "TestReserveWSConnectionSlotRejectsWhenCapacityExceeded"
  check_file_contains "server tests cover reserved websocket registration" "$target/foundation/server-kit/go/httpserver/server_test.go" "TestRegisterWSConnectionUsesReservedSlot"
  check_file_contains "server tests cover websocket backpressure metric" "$target/foundation/server-kit/go/httpserver/server_test.go" "TestEnqueueWSRecordsBackpressureFailure"
  check_file_contains "load tests bound pre/post infrastructure probes" "$target/tests/load/load_test.go" "probeCtx, probeCancel := context.WithTimeout(ctx, opTimeout)"
  check_file_contains "config loads allowed origins" "$target/internal/config/config.go" "ALLOWED_ORIGINS"
  check_file_contains "config loads explicit Redis URL" "$target/internal/config/config.go" "REDIS_URL"
  check_file_contains "config defaults auth on in production" "$target/internal/config/config.go" 'env == "production"'
  check_file_contains "env documents operational endpoint protection" "$target/.env.example" "PROTECT_OPERATIONAL_ENDPOINTS"
  check_exists "foundation runtime transport" "$target/foundation/runtime-transport/go/go.mod"
  check_exists "foundation proto envelope" "$target/foundation/runtime-transport/protos/foundation/v1/envelope.proto"
  check_exists "foundation proto projection" "$target/foundation/runtime-transport/protos/foundation/v1/projection.proto"
  check_exists "foundation proto metadata" "$target/foundation/runtime-transport/protos/foundation/v1/metadata.proto"
  check_exists "foundation shared types proto" "$target/foundation/runtime-transport/protos/foundation/v1/types.proto"
  check_file_contains "foundation projection proto supports patch" "$target/foundation/runtime-transport/protos/foundation/v1/projection.proto" "PROJECTION_OPERATION_PATCH"
  check_exists "foundation config contracts" "$target/foundation/config-contracts/go/go.mod"
  check_exists "foundation tooling" "$target/foundation/tooling/docs/enforcement.md"
  check_exists "api README" "$target/api/README.md"
  check_exists "proto README" "$target/api/protos/README.md"
  check_exists "scaffolded Foundation proto envelope" "$target/api/protos/foundation/v1/envelope.proto"
  check_exists "scaffolded Foundation proto metadata" "$target/api/protos/foundation/v1/metadata.proto"
  check_exists "scaffolded Foundation proto projection" "$target/api/protos/foundation/v1/projection.proto"
  check_exists "scaffolded Foundation shared types proto" "$target/api/protos/foundation/v1/types.proto"
  check_file_contains "Foundation metadata owns actor type" "$target/api/protos/foundation/v1/metadata.proto" "string actor_type"
  check_file_contains "Foundation metadata owns policy snapshot" "$target/api/protos/foundation/v1/metadata.proto" "string policy_snapshot_ref"
  check_file_contains "Foundation metadata owns legal basis" "$target/api/protos/foundation/v1/metadata.proto" "string legal_basis_code"
  check_file_contains "Foundation metadata owns requested timestamp" "$target/api/protos/foundation/v1/metadata.proto" "google.protobuf.Timestamp requested_at"
  check_file_contains "Foundation shared types own pagination" "$target/api/protos/foundation/v1/types.proto" "message PaginationRequest"
  check_file_contains "Foundation shared types own money" "$target/api/protos/foundation/v1/types.proto" "message Money"
  check_file_contains "scaffolded projection proto supports patch" "$target/api/protos/foundation/v1/projection.proto" "PROJECTION_OPERATION_PATCH"
  check_exists "scaffolded Foundation Cap'n Proto envelope schema" "$target/api/schemas/foundation/v1/envelope.capnp"
  check_absent "legacy scaffolded transport proto directory" "$target/api/protos/transport"
  check_absent "legacy scaffolded Hermes proto directory" "$target/api/protos/hermes"
  check_absent "app-local common proto namespace" "$target/api/protos/common"
  check_absent "app-local common schema namespace" "$target/api/schemas/common"
  check_file_contains "proto README documents lifecycle generator" "$target/api/protos/README.md" "make lifecycle-contracts"
fi

if [[ "${WITH_DOCKER:-}" == "true" ]]; then
  check_exists "Dockerfile" "$target/Dockerfile"
  check_exists "docker-compose.yml" "$target/docker-compose.yml"
  if [[ -n "$(find "$target" \
    \( -type d \( -name .git \
      -o -name foundation \
      -o -name node_modules \
      -o -name .next \
      -o -name dist \
      -o -name build \
      -o -name tmp \
      -o -name vendor \
      -o -name target \) \) -prune \
    -o -name Dockerfile -type f -exec grep -Fq "fholzer/nginx-brotli" {} \; -print -quit 2>/dev/null)" ]]; then
    echo "[FAIL] Dockerfiles avoid removed nginx-brotli image"
    failed=1
  else
    echo "[OK] Dockerfiles avoid removed nginx-brotli image"
  fi
  check_file_contains "Redis config image baseline is Redis 8" "$target/Dockerfile.redis" "ARG REDIS_VERSION=8-alpine"
  check_file_contains "compose Postgres service" "$target/docker-compose.yml" "  app-postgres:"
  # The bare "postgres" alias collides with the platform database on shared
  # proxy networks (Coolify): Docker DNS round-robins between the two.
  check_file_not_contains "compose avoids bare postgres service alias" "$target/docker-compose.yml" "  postgres:"
  check_file_contains "compose Postgres 18 mounts parent data directory" "$target/docker-compose.yml" "/var/lib/postgresql"
  check_file_not_contains "compose Postgres 18 avoids legacy data mount" "$target/docker-compose.yml" "/var/lib/postgresql/data"
  check_file_contains "compose server receives DB host" "$target/docker-compose.yml" 'DB_HOST: "${DB_HOST:-app-postgres}"'
  check_file_contains "compose migrate defaults to Compose Postgres host" "$target/docker-compose.yml" 'DB_HOST=${DB_HOST:-app-postgres}'
  check_file_contains "compose migrate waits for Postgres" "$target/docker-compose.yml" "condition: service_healthy"
  check_file_contains "compose migrate rejects container-local DATABASE_URL" "$target/docker-compose.yml" "DATABASE_URL points at localhost"
  if awk '
    /^  frontend:/ { in_frontend = 1; next }
    /^  [A-Za-z0-9_-]+:/ && in_frontend { in_frontend = 0 }
    in_frontend && /postgres:/ { found = 1 }
    END { exit found ? 0 : 1 }
  ' "$target/docker-compose.yml"; then
    echo "[FAIL] compose frontend avoids direct Postgres dependency"
    failed=1
  else
    echo "[OK] compose frontend avoids direct Postgres dependency"
  fi
  check_exists "docker-compose.dev.yml" "$target/docker-compose.dev.yml"
  check_file_contains "dev Postgres 18 mounts parent data directory" "$target/docker-compose.dev.yml" "/var/lib/postgresql"
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
    check_file_contains "test Postgres service-scoped platform override" "$target/docker-compose.test.yml" "TEST_POSTGRES_PLATFORM"
    check_file_contains "test Redis image override" "$target/docker-compose.test.yml" "TEST_REDIS_IMAGE"
    check_file_contains "test Postgres host port defaults to Docker ephemeral allocation" "$target/docker-compose.test.yml" '${TEST_DB_PORT:-0}:5432'
    check_file_contains "test Redis host port defaults to Docker ephemeral allocation" "$target/docker-compose.test.yml" '${TEST_REDIS_PORT:-0}:6379'
    check_file_not_contains "test compose avoids fixed container names" "$target/docker-compose.test.yml" "container_name:"
    check_file_contains "test Postgres 18 mounts parent data directory" "$target/docker-compose.test.yml" "/var/lib/postgresql"
    check_file_not_contains "test Postgres 18 avoids legacy data mount" "$target/docker-compose.test.yml" "/var/lib/postgresql/data"
    check_file_contains "Makefile test DB port defaults to ephemeral allocation" "$target/Makefile" "TEST_DB_PORT ?= 0"
    check_file_contains "Makefile test Redis port defaults to ephemeral allocation" "$target/Makefile" "TEST_REDIS_PORT ?= 0"
    check_file_contains "Makefile records resolved test Postgres port" "$target/Makefile" "test-postgres 5432"
    check_file_contains "Makefile records resolved test Redis port" "$target/Makefile" "test-redis 6379"
    check_file_contains "shared Docker Go module cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gomod'
    check_file_contains "shared Docker Go build cache" "$target/Dockerfile" 'id=${CACHE_NAMESPACE}-gobuild'
    check_file_contains "Docker dependency stage" "$target/Dockerfile" "AS go-deps"
    check_exists "Postgres config image Dockerfile" "$target/Dockerfile.postgres"
    check_exists "Redis config image Dockerfile" "$target/Dockerfile.redis"
    check_file_contains "Postgres Dockerfile bakes config" "$target/Dockerfile.postgres" "COPY config/postgresql.conf"
    check_file_contains "Postgres Dockerfile bakes hba" "$target/Dockerfile.postgres" "COPY config/pg_hba.conf"
    check_file_contains "Redis Dockerfile bakes config" "$target/Dockerfile.redis" "COPY config/redis.conf"
    check_file_contains_any "Compose supplies a configured Postgres image" "$target/docker-compose.yml" "Dockerfile.postgres" "postgis/postgis:18-"
    check_file_contains "Compose builds Redis config image" "$target/docker-compose.yml" "Dockerfile.redis"
    check_file_contains "Compose uses baked Postgres hba" "$target/docker-compose.yml" "hba_file=/etc/postgresql/pg_hba.conf"
    check_file_contains "Compose migration fails auth after grace window" "$target/docker-compose.yml" "database authentication still failing after"
    check_file_not_contains "Compose avoids Postgres config bind" "$target/docker-compose.yml" "./config/postgresql.conf"
    check_file_not_contains "Compose avoids Redis config bind" "$target/docker-compose.yml" "./config/redis.conf"
    check_file_not_contains "Compose avoids default CA bind" "$target/docker-compose.yml" "config/certs/ca.crt"
    if grep -Fq '=> ./foundation/runtime-sdk/go' "$target/go.mod"; then
      check_file_contains "Docker deps copy runtime-sdk go.mod" "$target/Dockerfile" "COPY foundation/runtime-sdk/go/go.mod"
    fi
    if [[ -f "$target/api/protos/go.mod" ]] && grep -Fq '=> ./api/protos' "$target/go.mod"; then
      check_file_contains "Docker deps copy api protos go.mod" "$target/Dockerfile" "COPY api/protos/go.mod ./api/protos/"
    fi
    if [[ -f "$target/frontend/package.json" ]] && grep -Fq '"@ovasabi/runtime-native"' "$target/frontend/package.json"; then
      check_file_contains "Docker copies runtime-native package manifest" "$target/Dockerfile" "COPY foundation/runtime-native/ts/package.json"
      check_file_contains "Docker copies runtime-native source" "$target/Dockerfile" "COPY foundation/runtime-native/ts ./foundation/runtime-native/ts"
      if [[ -f "$target/frontend/package-lock.json" ]]; then
        check_file_contains "frontend lock includes runtime-native" "$target/frontend/package-lock.json" '"@ovasabi/runtime-native"'
        check_file_contains "frontend lock links runtime-native workspace package" "$target/frontend/package-lock.json" '"node_modules/@ovasabi/runtime-native"'
      else
        echo "[OK] frontend lock absent before npm install"
      fi
    fi
    check_exists "postgresql config" "$target/config/postgresql.conf"
    check_exists "Postgres hba config" "$target/config/pg_hba.conf"
    check_exists "redis config" "$target/config/redis.conf"
    check_file_contains "postgres timeout guardrail" "$target/config/postgresql.conf" "statement_timeout"
    check_file_contains "postgres WAL headroom baseline" "$target/config/postgresql.conf" "max_wal_size = 4GB"
    check_file_contains "postgres WAL floor baseline" "$target/config/postgresql.conf" "min_wal_size = 512MB"
    check_file_contains "postgres checkpoint cadence baseline" "$target/config/postgresql.conf" "checkpoint_timeout = 15min"
    check_file_contains "postgres autovacuum guardrail" "$target/config/postgresql.conf" "autovacuum_vacuum_scale_factor"
    check_file_contains "postgres autovacuum work memory baseline" "$target/config/postgresql.conf" "autovacuum_work_mem = 128MB"
    check_file_contains "postgres async I/O baseline" "$target/config/postgresql.conf" "io_method"
    check_file_contains "postgres I/O observability baseline" "$target/config/postgresql.conf" "track_io_timing"
    check_file_contains "Postgres hba supports local operator recovery" "$target/config/pg_hba.conf" "local   all             all                                     trust"
    check_file_contains "Postgres hba permits Compose network SCRAM clients" "$target/config/pg_hba.conf" "0.0.0.0/0"
    check_file_contains "redis LFU eviction guardrail" "$target/config/redis.conf" "maxmemory-policy allkeys-lfu"
    check_file_contains "redis io thread baseline" "$target/config/redis.conf" "io-threads"
    check_file_contains "redis ephemeral persistence baseline" "$target/config/redis.conf" "appendonly no"
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
  check_exists "frontend prototype store bootstrap" "$frontend_root/src/stores/prototype.ts"
  check_file_contains "frontend prototype context factory" "$frontend_root/src/stores/prototype.ts" "createPrototypeRuntimeContext"
  check_file_contains "frontend prototype generated stores" "$frontend_root/src/stores/prototype.ts" "createPrototypeTenantStores"
  check_file_contains "frontend prototype persistence binding" "$frontend_root/src/stores/prototype.ts" "createTenantSnapshotPersistence"
  check_file_contains "frontend prototype persistence hydrate" "$frontend_root/src/stores/prototype.ts" "hydratePersistence"
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
  check_file_contains "make frontend prototype runtime target" "$target/Makefile" "frontend-prototype-runtime:"
  check_file_contains "build frontend depends on prototype runtime" "$target/Makefile" "build-frontend: frontend-prototype-runtime"
  check_file_contains "test frontend depends on prototype runtime" "$target/Makefile" "test-frontend: frontend-prototype-runtime"
  check_file_contains "lint frontend depends on prototype runtime" "$target/Makefile" "lint-frontend: frontend-prototype-runtime"
  check_file_contains "frontend benchmark target depends on prototype runtime" "$target/Makefile" "test-bench-frontend: frontend-prototype-runtime"
  check_file_contains "frontend benchmark target captures workbench profile" "$target/Makefile" "frontend_workbench_profile.sh"
  check_file_contains "make wasm test target rebuilds runtime" "$target/Makefile" "test-wasm: build-runtime"
  check_file_contains "wasm optimizer enables Go non-trapping float-to-int" "$target/Makefile" "--enable-nontrapping-float-to-int"
  check_file_contains "make e2e target regenerates contracts" "$target/Makefile" "test-e2e: communication-contracts"
  check_file_contains "make e2e target supports explicit frontend script" "$target/Makefile" 'zsh $(FRONTEND_SCRIPT_RUNNER) . e2e'
  check_file_contains "make e2e target supports test e2e script" "$target/Makefile" 'zsh $(FRONTEND_SCRIPT_RUNNER) . test:e2e'
  check_file_contains "make foundation transport proto target" "$target/Makefile" "foundation-transport-proto:"
  check_file_contains "make communication contract aggregate" "$target/Makefile" "communication-contracts:"
  check_file_contains "communication contracts include frontend prototype runtime" "$target/Makefile" "communication-contracts: proto proto-ts frontend-prototype-runtime"
  check_file_contains "proto-ts writes generated app contracts" "$target/Makefile" "--ts_proto_out=frontend/src/types/protos"
fi

if [[ "${WITH_WASM:-false}" == "true" ]]; then
  check_exists "rust clippy baseline" "$target/clippy.toml"
  check_exists "rustfmt baseline" "$target/rustfmt.toml"
  check_exists "runtime sdk" "$target/foundation/runtime-sdk/go/go.mod"
  check_exists "runtime shared arena schema" "$target/foundation/runtime-sdk/protocols/system/v1/runtime_shared_arena.capnp"
  check_exists "runtime shared arena host API" "$target/foundation/runtime-sdk/ts/browser-host/src/arena.ts"
  check_exists "runtime payload router API" "$target/foundation/runtime-sdk/ts/browser-host/src/payloadRouter.ts"
  check_exists "runtime ffi portable string helpers" "$target/foundation/runtime-sdk/go/runtimehost/ffi_strings.go"
  check_file_contains "runtime ffi macro exports ovrt-core" "$target/foundation/runtime-sdk/rust/crates/ovrt-ffi/src/lib.rs" "pub use ovrt_core;"
  check_file_contains "runtime ffi pool reuses fixed buffer" "$target/foundation/runtime-sdk/go/runtimehost/ffi_unix.go" "bufferPool"
  check_file_contains "runtime ffi pool uses backend seam" "$target/foundation/runtime-sdk/go/runtimehost/ffi_unix.go" "type ffiBackend interface"
  check_exists "runtime transport compression API" "$target/foundation/runtime-transport/ts/src/compression.ts"
  check_exists "runtime offline queue API" "$target/foundation/runtime-transport/ts/src/offlineQueue.ts"
  check_exists "wasm entry" "$target/wasm/main.go"
  check_file_contains "wasm runtime-transport shim" "$target/wasm/main.go" "__OVASABI_RUNTIME_TRANSPORT"
fi

if [[ "${WITH_NATIVE:-false}" == "true" ]]; then
  check_exists "foundation runtime-native Rust crate" "$target/foundation/runtime-native/rust/Cargo.toml"
  check_exists "foundation runtime-native TS package" "$target/foundation/runtime-native/ts/package.json"
  check_file_contains "runtime transport supports native lane" "$target/foundation/runtime-transport/ts/src/index.ts" '"native"'
  check_exists "native package" "$target/native/package.json"
  check_exists "native Tauri config" "$target/native/src-tauri/tauri.conf.json"
  check_exists "native Tauri dev config" "$target/native/src-tauri/tauri.dev.conf.json"
  check_exists "native Tauri prod config" "$target/native/src-tauri/tauri.prod.conf.json"
  check_exists "native Tauri icon asset" "$target/native/src-tauri/icons/icon.png"
  check_exists "native Tauri Rust manifest" "$target/native/src-tauri/Cargo.toml"
  check_exists "native Tauri capability" "$target/native/src-tauri/capabilities/main.json"
  check_exists "native capability examples" "$target/native/src-tauri/capabilities/examples.md"
  check_file_contains "native active capability list is explicit" "$target/native/src-tauri/tauri.conf.json" '"capabilities": ["main"]'
  check_file_contains "native dev uses config overlay" "$target/native/package.json" 'tauri dev --config src-tauri/tauri.dev.conf.json'
  check_file_contains "native build uses prod config overlay" "$target/native/package.json" 'tauri build --config src-tauri/tauri.prod.conf.json'
  check_file_contains "native dev CSP allows Vite websocket" "$target/native/src-tauri/tauri.dev.conf.json" 'ws://127.0.0.1:5173'
  check_file_contains "native dev CSP allows inline styles" "$target/native/src-tauri/tauri.dev.conf.json" "'unsafe-inline'"
  check_file_not_contains "native prod CSP forbids Vite websocket" "$target/native/src-tauri/tauri.prod.conf.json" 'ws://127.0.0.1:5173'
  check_file_not_contains "native prod CSP forbids inline styles" "$target/native/src-tauri/tauri.prod.conf.json" "'unsafe-inline'"
  check_file_contains "native README documents frontend layout" "$target/native/README.md" "../../frontend"
  check_file_contains "native command dispatch" "$target/native/src-tauri/src/lib.rs" "foundation_runtime_dispatch"
  check_file_contains "native command ACL manifest" "$target/native/src-tauri/build.rs" "AppManifest::new().commands"
  check_file_contains "native capability allows runtime dispatch explicitly" "$target/native/src-tauri/capabilities/main.json" "allow-foundation-runtime-dispatch"
  check_file_not_contains "native capability avoids broad core defaults" "$target/native/src-tauri/capabilities/main.json" "core:default"
  check_file_not_contains "native scaffold does not expose ephemeral storage as a vault" "$target/native/src-tauri/src/lib.rs" "foundation_secure_store_get"
  check_file_not_contains "native scaffold avoids startup expect" "$target/native/src-tauri/src/lib.rs" ".expect("
  check_file_contains "native uses runtime-native crate" "$target/native/src-tauri/Cargo.toml" "ovasabi-runtime-native"
  check_file_contains "native scaffold pins Tauri" "$target/native/src-tauri/Cargo.toml" '=2.11.1'
  check_file_contains "make native dev target" "$target/Makefile" "native-dev:"
  check_file_contains "make native benchmark target" "$target/Makefile" "native-bench:"
  check_exists "native benchmark script" "$target/scripts/checks/native_benchmark.sh"
  if [[ "${PROFILE:-}" == "full" || "${PROFILE:-}" == "frontend" ]]; then
    frontend_root="$target/frontend"
    if [[ "${PROFILE:-}" == "frontend" ]]; then
      frontend_root="$target"
      if [[ -f "$target/frontend/package.json" ]]; then
        frontend_root="$target/frontend"
      fi
    fi
    check_file_contains "frontend vitest config honors worker cap" "$frontend_root/vitest.config.ts" "FOUNDATION_VITEST_WORKERS"
    check_file_contains "frontend vitest config honors serial mode" "$frontend_root/vitest.config.ts" "FOUNDATION_VITEST_SERIAL"
    check_frontend_package_contains "frontend runtime native package" "$frontend_root/package.json" '"@ovasabi/runtime-native"'
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "project scaffold check failed"
  exit 1
fi

echo "project scaffold check passed"
