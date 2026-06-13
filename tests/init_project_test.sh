#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-init.XXXXXX)"
PROJECT_DIR="$TMP_DIR/generated_project_v1"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

test_step "init generated_project fixture"
"$FOUNDATION_DIR/init.sh" generated_project --project-dir "$PROJECT_DIR" --skip-deps >/dev/null

test_step "validate native project identifier guard"
if PROJECT_IDENTIFIER=flat "$FOUNDATION_DIR/init.sh" invalid_native --project-dir "$TMP_DIR/invalid_native_v1" --with-native --dry-run --skip-deps >"$TMP_DIR/invalid_identifier.log" 2>&1; then
    echo "expected flat native PROJECT_IDENTIFIER override to fail validation" >&2
    exit 1
fi
if ! rg -n "reverse-domain notation" "$TMP_DIR/invalid_identifier.log" >/dev/null 2>&1; then
    echo "expected invalid identifier error to explain reverse-domain notation" >&2
    exit 1
fi

assert_contains ".foundation" "^PROJECT_NAME=generated_project$"
assert_contains ".foundation" "^PROFILE=full$"
assert_contains ".foundation" "^PROJECT_IDENTIFIER=com\\.ovasabi\\.generated\\.project$"
assert_contains ".foundation" "^WITH_DOCKER=true$"
assert_contains ".foundation" "^WITH_WASM=true$"
assert_contains ".foundation" "^WITH_NATIVE=true$"
assert_contains ".foundation" "^BASELINE_GENERATION=manifest-v4$"
assert_file ".cursorrules"
assert_file "AGENTS.md"
assert_file "CLAUDE.md"
assert_file "Makefile"
assert_file "cmd/server/main.go"
assert_file "cmd/worker/main.go"
assert_file "cmd/docgen/main.go"
assert_file "internal/bootstrap/services.go"
assert_file "internal/startup/dependencies.go"
assert_contains "internal/startup/dependencies.go" "hermes.WrapRuntimeStore"
assert_contains ".env.example" "HERMES_MAX_RECORDS_PER_SCOPE"
assert_file "internal/worker/registry.go"
assert_file "foundation/server-kit/go/go.mod"
assert_absent "foundation/server-kit/go/servicebacked"
assert_absent "foundation/server-kit/go/.cache"
assert_absent "foundation/server-kit/go/appbench.test"
assert_absent "foundation/server-kit/go/chain.test"
assert_absent "foundation/server-kit/go/grpcsvc.test"
assert_file "foundation/runtime-transport/go/go.mod"
assert_file "foundation/runtime-transport/protos/foundation/v1/envelope.proto"
assert_file "foundation/runtime-transport/protos/foundation/v1/metadata.proto"
assert_file "foundation/runtime-transport/protos/foundation/v1/projection.proto"
assert_file "foundation/runtime-transport/protos/foundation/v1/types.proto"
assert_file "api/protos/foundation/v1/envelope.proto"
assert_file "api/protos/foundation/v1/metadata.proto"
assert_file "api/protos/foundation/v1/projection.proto"
assert_file "api/protos/foundation/v1/types.proto"
assert_file "api/schemas/foundation/v1/envelope.capnp"
assert_absent "api/protos/common"
assert_absent "api/schemas/common"
assert_absent "api/protos/transport"
assert_absent "api/protos/hermes"
assert_file "foundation/runtime-sdk/go/go.mod"
assert_file "foundation/runtime-native/rust/Cargo.toml"
assert_file "foundation/runtime-native/ts/package.json"
assert_file "native/package.json"
assert_file "native/src-tauri/tauri.conf.json"
assert_file "native/src-tauri/tauri.dev.conf.json"
assert_file "native/src-tauri/tauri.prod.conf.json"
assert_file "native/src-tauri/capabilities/examples.md"
assert_file "native/src-tauri/src/lib.rs"
assert_contains "native/package.json" "tauri dev --config src-tauri/tauri.dev.conf.json"
assert_contains "native/package.json" "tauri build --config src-tauri/tauri.prod.conf.json"
assert_contains "native/src-tauri/tauri.dev.conf.json" "ws://127.0.0.1:5173"
assert_contains "native/src-tauri/tauri.dev.conf.json" "'unsafe-inline'"
assert_contains "native/src-tauri/tauri.prod.conf.json" "connect-src 'self'"
assert_contains "Makefile" "native-bench:"
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
assert_contains "README.md" "Agent-Native Workflow"
assert_file "tests/integration/hermes_test.go"
assert_file "tests/integration/setup_helpers_test.go"
assert_file "migrations/000001_init.up.sql"
assert_file "migrations/000001_init.down.sql"
assert_file "wasm/main.go"
assert_absent "pkg"

test_step "run generated project scaffold checks"
"$PROJECT_DIR/scripts/checks/project_scaffold_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/agent_contract_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/practice_controls_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/runtime_performance_contract_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/formal_methods_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/operational_excellence_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/logging_practices_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/river_practices_check.sh" "$PROJECT_DIR"
test_step "generate lifecycle contracts in fixture"
(
    cd "$PROJECT_DIR"
    make lifecycle-contracts >/dev/null
)
test_step "run generated contract drift check"
"$PROJECT_DIR/scripts/checks/contract_drift_check.sh" "$PROJECT_DIR"
assert_file "tests/contract/generated_lifecycle_test.go"
assert_contains "tests/contract/generated_lifecycle_test.go" "VerifyCommandLifecycle"

echo "foundation init-project test passed"
