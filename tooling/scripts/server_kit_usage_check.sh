#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

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

check_no_project_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  local output
  if output="$(rg -n "$pattern" "$@" \
    --glob '*.go' \
    --glob '!foundation/**' \
    --glob '!docs/**' \
    --glob '!scripts/checks/**' \
    --glob '!**/generated/**' \
    --glob '!**/*test*' \
    --glob '!**/testdata/**' \
    --glob '!**/.cache/**' \
    --glob '!**/dist/**' \
    --glob '!**/build/**' \
    --glob '!**/node_modules/**' \
    --glob '!**/vendor/**' 2>/dev/null)"; then
    echo "[FAIL] $label"
    echo "$output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_no_service_dir() {
  local label="$1"
  local name="$2"
  local found=""
  local service_path
  for service_root in "$target/internal/service" "$target/backend/internal/service"; do
    [[ -d "$service_root" ]] || continue
    while IFS= read -r service_path; do
      found+="${service_path#$target/}"$'\n'
    done < <(find "$service_root" -mindepth 1 -maxdepth 1 -type d -name "$name" 2>/dev/null)
  done
  if [[ -n "$found" ]]; then
    echo "[FAIL] $label"
    echo "$found" | sed '/^$/d; s/^/  /'
    failed=1
  else
    echo "[OK] $label"
  fi
}

if [[ -d "$target/foundation/server-kit/go" ]]; then
  kit="$target/foundation/server-kit/go"
else
  kit="$target/server-kit/go"
fi

check_exists "server-kit module" "$kit/go.mod"
check_exists "server-kit registry" "$kit/registry/registry.go"
check_exists "server-kit graceful handler" "$kit/graceful/graceful.go"
check_exists "server-kit HTTP API bridge" "$kit/httpapi/dispatch_route.go"
check_exists "server-kit events" "$kit/events/envelope.go"
check_exists "server-kit metadata" "$kit/metadata/metadata.go"
check_exists "server-kit security" "$kit/security/middleware.go"
check_exists "server-kit compression" "$kit/compress/middleware.go"
check_exists "server-kit observability" "$kit/observability/http.go"
check_exists "server-kit resilience" "$kit/resilience/resilience.go"
check_exists "server-kit worker engine" "$kit/worker/engine.go"
check_file_contains "server-kit exposes direct frame client" "$kit/grpcsvc/grpcsvc.go" "NewDirectFrameClient"
check_file_contains "server-kit exposes binary frame registration" "$kit/grpcsvc/grpcsvc.go" "RegisterFrame"
check_file_contains "server-kit exposes borrowed frame views" "$kit/grpcsvc/grpcsvc.go" "UnmarshalFrameView"
check_file_contains "server-kit exposes typed frame projection" "$kit/bootstrap/frame_handlers.go" "RegisterTypedFrameHandlers"
check_file_contains "server-kit runtime initializes frame router" "$kit/startup/runtime.go" "FrameRouter"

if [[ -f "$target/.foundation" && -d "$target/internal" ]]; then
  startup_file=""
  for candidate in "$target/internal/startup/dependencies.go" "$target/internal/startup/deps.go" "$target/internal/startup/init.go"; do
    if [[ -f "$candidate" ]]; then
      startup_file="$candidate"
      break
    fi
  done
  check_exists "startup dependency wiring" "$startup_file"
  check_file_contains "startup initializes resilience runtime" "$startup_file" "resilience.New"
  check_file_contains "startup registers dependencies with resilience" "$startup_file" 'RegisterDependency('
  check_file_contains "server uses registry" "$target/internal/server/server.go" "server-kit/go/registry"
  check_file_contains "server uses graceful responses" "$target/internal/server/server.go" "server-kit/go/graceful"
  check_file_contains "server uses metadata normalization" "$target/internal/server/server.go" "server-kit/go/metadata"
  check_file_contains "server uses HTTP API bridge" "$target/internal/server/server.go" "server-kit/go/httpapi"
  check_file_contains "server uses security middleware" "$target/internal/server/server.go" "server-kit/go/security"
  check_file_contains "server uses compression middleware" "$target/internal/server/server.go" "server-kit/go/compress"
  check_file_contains "server uses observability middleware" "$target/internal/server/server.go" "server-kit/go/observability"
  check_file_contains "websocket uses routing metrics" "$target/internal/server/websocket.go" "server-kit/go/wsrouting"
  check_file_contains "websocket uses websocket metrics" "$target/internal/server/websocket.go" "server-kit/go/wsmetrics"
  check_file_contains "worker queues are bounded by config" "$target/internal/worker/registry.go" "DefaultQueueConfig"
  check_no_project_match "internal code avoids JSON gRPC compatibility dispatch" "grpcsvc\\.Dispatch\\s*\\(|\\.Dispatch\\s*\\([^\\n]*grpcsvc\\.Envelope|grpcsvc\\.Envelope\\b" "$target/internal" "$target/cmd"
  check_no_project_match "services use Foundation domain errors" "type .*Error struct|type .*Error interface|func New.*Error\\(" "$target/internal/service"
  check_no_project_match "services use Foundation event bus" "type .*Bus struct|type .*EventBus" "$target/internal/service"
  check_no_project_match "services use Foundation cache/Redis clients" "type .*Cache struct|type .*Redis|redis\\.NewClient\\(|redis\\.NewClusterClient\\(" "$target/internal/service"
  check_no_project_match "services use Foundation database executors" "\"database/sql\"|sql\\.(DB|Tx|Rows|Row)|pgxpool\\.New|pgxpool\\.ParseConfig|pgx\\.Connect" "$target/internal/service"
  check_no_project_match "services use Foundation worker/runtime primitives" "type .*Worker|type .*Queue|go func\\(|time\\.NewTicker\\(" "$target/internal/service"
  check_no_service_dir "service tree avoids local persistence primitive packages" "persistence"
  check_no_service_dir "service tree avoids local shared primitive packages" "shared"
  check_no_service_dir "service tree avoids local adapter primitive packages" "adapters"
  check_no_service_dir "service tree avoids local service-kit primitive packages" "servicekit"
  check_no_service_dir "service tree avoids local test utility service packages" "testutil"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "server-kit usage check failed"
  exit 1
fi

echo "server-kit usage check passed"
