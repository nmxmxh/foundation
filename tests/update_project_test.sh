#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_DIR="$(mktemp -d /tmp/ovasabi-foundation-update.XXXXXX)"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$PROJECT_DIR"
}
trap cleanup EXIT

mkdir -p "$PROJECT_DIR/frontend" "$PROJECT_DIR/pkg"
mkdir -p "$PROJECT_DIR/api/protos/transport/v1" "$PROJECT_DIR/api/protos/hermes/v1"
printf 'legacy transport proto\n' > "$PROJECT_DIR/api/protos/transport/v1/envelope.proto"
printf 'legacy hermes proto\n' > "$PROJECT_DIR/api/protos/hermes/v1/projection.proto"
cat > "$PROJECT_DIR/.gitignore" <<'EOF'
# Generated files
**/generated/
EOF
sed "s|{{MODULE_PATH}}|github.com/ovasabi/foundation_update_fixture|g" \
    "$FOUNDATION_DIR/templates/backend/go.mod.template" > "$PROJECT_DIR/go.mod"
printf '{"scripts":{"build":"vite build","lint":"eslint .","test":"vitest run"}}\n' > "$PROJECT_DIR/frontend/package.json"

cat > "$PROJECT_DIR/.foundation" <<EOF
FOUNDATION_VERSION=0.0.0
FOUNDATION_PATH=$(printf '%q' "$FOUNDATION_DIR")
CREATED_AT=2026-01-01T00:00:00Z
PROFILE=full
PROJECT_NAME=foundation_update_fixture
GO_MODULE=github.com/ovasabi/foundation_update_fixture
WITH_DOCKER=true
WITH_WASM=false
BASELINE_GENERATION=legacy
EOF

test_step "update legacy fixture from current foundation"
"$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR" >/dev/null

assert_contains ".foundation" "^WITH_WASM=true$"
assert_contains ".foundation" "^BASELINE_GENERATION=manifest-v4$"
assert_file ".cursorrules"
assert_file ".clauderules"
assert_file "cmd/worker/main.go"
assert_file "cmd/docgen/main.go"
assert_file "internal/bootstrap/services.go"
assert_contains "internal/startup/dependencies.go" "hermes.WrapRuntimeStore"
assert_not_contains ".gitignore" "\\*\\*/generated/"
assert_not_contains "foundation/.gitignore" "\\*\\*/generated/"
assert_contains ".env.example" "HERMES_MAX_RECORDS_PER_SCOPE"
assert_file "internal/worker/registry.go"
assert_file "internal/worker/periodic_jobs.go"
assert_file "api/protos/foundation/v1/envelope.proto"
assert_file "api/protos/foundation/v1/metadata.proto"
assert_file "api/protos/foundation/v1/projection.proto"
assert_file "api/protos/foundation/v1/types.proto"
assert_file "api/schemas/foundation/v1/envelope.capnp"
assert_absent "api/protos/common"
assert_absent "api/schemas/common"
assert_file "tests/integration/hermes_test.go"
assert_file "tests/integration/setup_helpers_test.go"
assert_file "wasm/main.go"
assert_file "foundation/runtime-sdk/go/go.mod"
assert_file "foundation/runtime-transport/go/generated/foundation/v1/envelope.pb.go"
assert_file "foundation/runtime-transport/go/generated/foundation/v1/metadata.pb.go"
assert_file "foundation/runtime-transport/go/generated/foundation/v1/projection.pb.go"
assert_file "foundation/runtime-transport/go/generated/foundation/v1/types.pb.go"
assert_file "docs/foundation/foundation_architecture_contract.md"
assert_file "docs/foundation/foundation_tour.md"
assert_file "docs/foundation/agent_operating_contract.md"
assert_file "docs/foundation/practice_controls.md"
assert_file "docs/foundation/ai_threat_model.md"
assert_file "docs/foundation/performance_lab.md"
assert_file "docs/foundation/projection_freshness_contract.md"
assert_file "docs/foundation/future_practices_research.md"
assert_file "docs/foundation/specs/tla/WorkerRetryQueue.tla"
assert_file "docs/foundation/specs/tla/CacheProjectionFreshness.tla"
assert_file "docs/foundation/specs/tla/WebSocketBackpressure.tla"
assert_file "docs/foundation/scaffold_manifest.md"
assert_contains "AGENTS.md" "agent_operating_contract.md"
assert_contains ".cursorrules" "scaffold.manifest.tsv"
assert_contains ".clauderules" "Ovasabi Foundation workspace"
assert_contains "README.md" "Agent-Native Workflow"
assert_file "migrations/000001_init.up.sql"
assert_file "migrations/000001_init.down.sql"
assert_file "Dockerfile"
assert_file "docker-compose.yml"
assert_file "docker-compose.dev.yml"
assert_file "frontend/tsconfig.json"
assert_file "frontend/vite.config.ts"

if [[ -e "$PROJECT_DIR/pkg" ]]; then
    echo "expected empty pkg/ to be removed" >&2
    exit 1
fi

if [[ -e "$PROJECT_DIR/api/protos/transport" || -e "$PROJECT_DIR/api/protos/hermes" ]]; then
    echo "expected legacy foundation proto directories to be removed" >&2
    exit 1
fi

test_step "run updated project scaffold checks"
"$PROJECT_DIR/scripts/checks/project_scaffold_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/agent_contract_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/practice_controls_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/runtime_performance_contract_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/formal_methods_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/operational_excellence_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/logging_practices_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/river_practices_check.sh" "$PROJECT_DIR"

echo "foundation update-project test passed"
