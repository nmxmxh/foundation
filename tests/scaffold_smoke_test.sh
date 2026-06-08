#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-smoke.XXXXXX)"
PROJECT_DIR="$TMP_DIR/smoke_project_v1"
ARTIFACT_DIR="${SCAFFOLD_SMOKE_ARTIFACT_DIR:-$FOUNDATION_DIR/test-results/scaffold-smoke-$(date -u +%Y%m%dT%H%M%SZ)}"
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

test_step "generate frontend prototype runtime in fixture"
(
    cd "$PROJECT_DIR"
    make frontend-prototype-runtime >/dev/null
)
assert_file "frontend/src/generated/prototypeRuntime.ts"
assert_contains "frontend/src/generated/prototypeRuntime.ts" "prototypeDomains"
assert_contains "frontend/src/generated/prototypeRuntime.ts" "createPrototypeTenantStores"

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
    mkdir -p "$ARTIFACT_DIR"
    metrics_file="$ARTIFACT_DIR/frontend-metrics.tsv"
    printf '# metric\tvalue\tunit\n' >"$metrics_file"
    if [[ ! -d "$PROJECT_DIR/frontend/node_modules" ]]; then
        if [[ "${SCAFFOLD_SMOKE_INSTALL:-0}" == "1" || "${CI:-}" == "true" ]]; then
            echo "== scaffold smoke: installing generated frontend dependencies =="
            install_started="$(date +%s)"
            npm --prefix "$PROJECT_DIR/frontend" install --no-audit --no-fund 2>&1 | tee "$ARTIFACT_DIR/frontend-install.log"
            install_finished="$(date +%s)"
            printf 'install_seconds\t%s\tseconds\n' "$((install_finished - install_started))" >>"$metrics_file"
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
    build_started="$(date +%s)"
    (
        cd "$PROJECT_DIR"
        make build-frontend
    ) 2>&1 | tee "$ARTIFACT_DIR/frontend-build.log"
    build_finished="$(date +%s)"
    printf 'build_seconds\t%s\tseconds\n' "$((build_finished - build_started))" >>"$metrics_file"
    if [[ -d "$PROJECT_DIR/frontend/dist" ]]; then
        dist_kib="$(du -sk "$PROJECT_DIR/frontend/dist" | awk '{print $1}')"
        dist_files="$(find "$PROJECT_DIR/frontend/dist" -type f | wc -l | tr -d ' ')"
        printf 'dist_bytes\t%s\tbytes\n' "$((dist_kib * 1024))" >>"$metrics_file"
        printf 'dist_files\t%s\tcount\n' "$dist_files" >>"$metrics_file"
    fi
    echo "== scaffold smoke: testing generated frontend =="
    test_started="$(date +%s)"
    (
        cd "$PROJECT_DIR"
        make test-frontend
    ) 2>&1 | tee "$ARTIFACT_DIR/frontend-test.log"
    test_finished="$(date +%s)"
    printf 'test_seconds\t%s\tseconds\n' "$((test_finished - test_started))" >>"$metrics_file"
    echo "frontend scaffold artifacts: $ARTIFACT_DIR"
else
    echo "Skipping generated frontend build/test; set SCAFFOLD_SMOKE_FRONTEND=1 to enable locally"
fi

echo "foundation scaffold smoke test passed"
