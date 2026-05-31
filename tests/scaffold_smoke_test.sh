#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-smoke.XXXXXX)"
PROJECT_DIR="$TMP_DIR/smoke_project_v1"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

"$FOUNDATION_DIR/init.sh" smoke_project --project-dir "$PROJECT_DIR" --skip-deps >/dev/null

(
    cd "$PROJECT_DIR"
    make lifecycle-contracts >/dev/null
)
assert_file "tests/contract/generated_lifecycle_test.go"

if [[ -f "$PROJECT_DIR/wasm/main.go" ]]; then
    echo "== scaffold smoke: compiling generated Go WASM shim =="
    (
        cd "$PROJECT_DIR"
        GOOS=js GOARCH=wasm go build -o "$TMP_DIR/main.wasm" ./wasm
    )
fi

should_run_frontend=0
if [[ "${SCAFFOLD_SMOKE_FRONTEND:-auto}" == "1" || "${CI:-}" == "true" ]]; then
    should_run_frontend=1
elif [[ "${SCAFFOLD_SMOKE_FRONTEND:-auto}" == "auto" && -d "$PROJECT_DIR/frontend/node_modules" ]]; then
    should_run_frontend=1
fi

if [[ "$should_run_frontend" -eq 1 && -f "$PROJECT_DIR/frontend/package.json" ]]; then
    if [[ ! -d "$PROJECT_DIR/frontend/node_modules" ]]; then
        if [[ "${SCAFFOLD_SMOKE_INSTALL:-0}" == "1" || "${CI:-}" == "true" ]]; then
            echo "== scaffold smoke: installing generated frontend dependencies =="
            npm --prefix "$PROJECT_DIR/frontend" install --no-audit --no-fund
        elif [[ "${SCAFFOLD_SMOKE_FRONTEND:-auto}" == "1" ]]; then
            echo "generated frontend dependencies are missing; set SCAFFOLD_SMOKE_INSTALL=1 to install them" >&2
            exit 1
        else
            echo "Skipping generated frontend build/test; set SCAFFOLD_SMOKE_INSTALL=1 to install dependencies"
            echo "foundation scaffold smoke test passed"
            exit 0
        fi
    fi
    echo "== scaffold smoke: building generated frontend =="
    (
        cd "$PROJECT_DIR"
        make build-frontend >/dev/null
    )
    echo "== scaffold smoke: testing generated frontend =="
    (
        cd "$PROJECT_DIR"
        make test-frontend >/dev/null
    )
else
    echo "Skipping generated frontend build/test; set SCAFFOLD_SMOKE_FRONTEND=1 to enable locally"
fi

echo "foundation scaffold smoke test passed"
