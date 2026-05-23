#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-init.XXXXXX)"
PROJECT_DIR="$TMP_DIR/generated_project_v1"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

assert_file() {
    local path="$1"
    if [[ ! -e "$PROJECT_DIR/$path" ]]; then
        echo "missing expected file: $path" >&2
        exit 1
    fi
}

assert_absent() {
    local path="$1"
    if [[ -e "$PROJECT_DIR/$path" ]]; then
        echo "unexpected file or directory: $path" >&2
        exit 1
    fi
}

assert_contains() {
    local path="$1"
    local pattern="$2"
    if ! rg -n "$pattern" "$PROJECT_DIR/$path" >/dev/null 2>&1; then
        echo "expected $path to contain: $pattern" >&2
        exit 1
    fi
}

"$FOUNDATION_DIR/init.sh" generated_project --project-dir "$PROJECT_DIR" --skip-deps >/dev/null

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
assert_file "internal/worker/registry.go"
assert_file "foundation/server-kit/go/go.mod"
assert_absent "foundation/server-kit/go/servicebacked"
assert_absent "foundation/server-kit/go/.cache"
assert_absent "foundation/server-kit/go/appbench.test"
assert_absent "foundation/server-kit/go/chain.test"
assert_absent "foundation/server-kit/go/grpcsvc.test"
assert_file "foundation/runtime-transport/go/go.mod"
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
assert_file "migrations/000001_init.up.sql"
assert_file "migrations/000001_init.down.sql"
assert_file "wasm/main.go"
assert_absent "pkg"

"$PROJECT_DIR/scripts/checks/project_scaffold_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/river_practices_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/contract_drift_check.sh" "$PROJECT_DIR"
(
    cd "$PROJECT_DIR"
    make lifecycle-contracts >/dev/null
)
assert_file "tests/contract/generated_lifecycle_test.go"
assert_contains "tests/contract/generated_lifecycle_test.go" "VerifyCommandLifecycle"

echo "foundation init-project test passed"
