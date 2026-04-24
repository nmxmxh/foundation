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

assert_contains ".foundation" "^PROJECT_NAME=generated_project$"
assert_contains ".foundation" "^PROFILE=full$"
assert_contains ".foundation" "^WITH_DOCKER=true$"
assert_contains ".foundation" "^WITH_WASM=true$"
assert_contains ".foundation" "^BASELINE_GENERATION=manifest-v3$"
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
assert_file "foundation/runtime-transport/go/go.mod"
assert_file "foundation/runtime-sdk/go/go.mod"
assert_file "docs/foundation/foundation_architecture_contract.md"
assert_file "migrations/000001_init.up.sql"
assert_file "migrations/000001_init.down.sql"
assert_file "wasm/main.go"
assert_absent "pkg"

"$PROJECT_DIR/scripts/checks/project_scaffold_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/river_practices_check.sh" "$PROJECT_DIR"

echo "foundation init-project test passed"
