#!/bin/bash
# Synchronize an existing project with the canonical Foundation baseline.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
FOUNDATION_VERSION="$(cat "$FOUNDATION_DIR/VERSION" 2>/dev/null || echo "1.0.0")"

source "$FOUNDATION_DIR/scripts/lib/foundation.sh"
source "$FOUNDATION_DIR/scripts/lib/scaffold.sh"

show_help() {
    cat <<'EOF'
Ovasabi Foundation Project Update Script

Usage: ./update-project.sh <project-path> [options]

Options:
  --dry-run           Show what would be updated without writing files
  --force             Overwrite force-managed scaffold files
  --docs-only         Only update docs/foundation
  --tooling-only      Only update linting/tooling/check scripts
  --foundation-only   Only update vendored foundation modules
  --profile <name>    Override inferred profile: full, backend, frontend, minimal
  --go-module <path>  Override inferred Go module path
  --with-docker       Enable Docker scaffold and metadata
  --no-docker         Disable Docker scaffold and metadata
  --with-wasm         Enable WASM scaffold and runtime-sdk metadata
  --no-wasm           Disable WASM scaffold and runtime-sdk metadata
  --with-native       Enable native/Tauri scaffold and runtime-native metadata
  --no-native         Disable native/Tauri scaffold and runtime-native metadata
  --help, -h          Show this help message
EOF
}

require_value() {
    local option="$1"
    local value="${2:-}"
    if [[ -z "$value" || "${value:0:1}" == "-" ]]; then
        foundation_log_error "$option requires a value"
        exit 1
    fi
}

validate_profile() {
    case "$1" in
        full|backend|frontend|minimal) return 0 ;;
        *)
            foundation_log_error "Invalid profile: $1"
            exit 1
            ;;
    esac
}

PROJECT_PATH=""
DRY_RUN="false"
FORCE="false"
DOCS_ONLY="false"
TOOLING_ONLY="false"
FOUNDATION_ONLY="false"
PROFILE_OVERRIDE=""
GO_MODULE_OVERRIDE=""
WITH_DOCKER_OVERRIDE=""
WITH_WASM_OVERRIDE=""
WITH_NATIVE_OVERRIDE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            show_help
            exit 0
            ;;
        --dry-run)
            DRY_RUN="true"
            shift
            ;;
        --force)
            FORCE="true"
            shift
            ;;
        --docs-only)
            DOCS_ONLY="true"
            shift
            ;;
        --tooling-only)
            TOOLING_ONLY="true"
            shift
            ;;
        --foundation-only)
            FOUNDATION_ONLY="true"
            shift
            ;;
        --profile)
            require_value "$1" "${2:-}"
            PROFILE_OVERRIDE="$2"
            validate_profile "$PROFILE_OVERRIDE"
            shift 2
            ;;
        --go-module)
            require_value "$1" "${2:-}"
            GO_MODULE_OVERRIDE="$2"
            shift 2
            ;;
        --with-docker)
            WITH_DOCKER_OVERRIDE="true"
            shift
            ;;
        --no-docker)
            WITH_DOCKER_OVERRIDE="false"
            shift
            ;;
        --with-wasm)
            WITH_WASM_OVERRIDE="true"
            shift
            ;;
        --no-wasm)
            WITH_WASM_OVERRIDE="false"
            shift
            ;;
        --with-native)
            WITH_NATIVE_OVERRIDE="true"
            shift
            ;;
        --no-native)
            WITH_NATIVE_OVERRIDE="false"
            shift
            ;;
        -*)
            foundation_log_error "Unknown option: $1"
            show_help
            exit 1
            ;;
        *)
            if [[ -n "$PROJECT_PATH" ]]; then
                foundation_log_error "Only one project path may be provided"
                exit 1
            fi
            PROJECT_PATH="$1"
            shift
            ;;
    esac
done

if [[ -z "$PROJECT_PATH" ]]; then
    foundation_log_error "Project path is required"
    show_help
    exit 1
fi

PROJECT_PATH="$(cd "$PROJECT_PATH" 2>/dev/null && pwd)" || {
    foundation_log_error "Project directory does not exist: $PROJECT_PATH"
    exit 1
}

FOUNDATION_FILE="$PROJECT_PATH/.foundation"

if [[ -f "$FOUNDATION_FILE" ]] && grep -q '^PROJECT_NAME=' "$FOUNDATION_FILE"; then
    PROJECT_NAME="$(foundation_read_metadata_value "$FOUNDATION_FILE" PROJECT_NAME)"
else
    PROJECT_NAME="$(basename "$PROJECT_PATH" | sed 's/_v[0-9]*$//')"
fi

PROFILE="${PROFILE_OVERRIDE:-$(foundation_infer_profile)}"
validate_profile "$PROFILE"

GO_MODULE="${GO_MODULE_OVERRIDE:-$(foundation_infer_go_module)}"

docker_default="false"
if [[ -f "$PROJECT_PATH/Dockerfile" || -f "$PROJECT_PATH/docker-compose.yml" || "$PROFILE" == "full" || "$PROFILE" == "backend" ]]; then
    docker_default="true"
fi
WITH_DOCKER="${WITH_DOCKER_OVERRIDE:-$(foundation_infer_flag WITH_DOCKER "$docker_default")}"

wasm_default="false"
if [[ "$PROFILE" == "full" || -d "$PROJECT_PATH/wasm" || -d "$PROJECT_PATH/foundation/runtime-sdk" ]]; then
    wasm_default="true"
fi
WITH_WASM="${WITH_WASM_OVERRIDE:-$(foundation_infer_flag WITH_WASM "$wasm_default")}"

if [[ "$PROFILE" == "full" && "$WITH_WASM" != "true" ]]; then
    foundation_log_warn "Full profile standardizes WITH_WASM=true; updating metadata and scaffold"
    WITH_WASM="true"
fi

native_default="false"
if [[ "$PROFILE" == "full" || -d "$PROJECT_PATH/native" || -d "$PROJECT_PATH/foundation/runtime-native" ]]; then
    native_default="true"
fi
WITH_NATIVE="${WITH_NATIVE_OVERRIDE:-$(foundation_infer_flag WITH_NATIVE "$native_default")}"

if [[ "$PROFILE" == "full" && "$WITH_NATIVE" != "true" ]]; then
    foundation_log_warn "Full profile standardizes WITH_NATIVE=true; updating metadata and scaffold"
    WITH_NATIVE="true"
fi

if [[ "$WITH_NATIVE" == "true" && "$WITH_WASM" != "true" ]]; then
    foundation_log_warn "Native scaffold uses runtime-sdk; updating WITH_WASM=true"
    WITH_WASM="true"
fi

if [[ "$WITH_NATIVE" == "true" ]]; then
    foundation_validate_project_identifier "$(foundation_project_identifier)" || exit 1
fi

foundation_log_info "Project: $PROJECT_PATH"
foundation_log_info "Profile: $PROFILE"
foundation_log_info "Go module: $GO_MODULE"
foundation_log_info "Foundation version: $FOUNDATION_VERSION"
foundation_log_info "Native scaffold: $WITH_NATIVE"
[[ "$WITH_NATIVE" == "true" ]] && foundation_log_info "Native identifier: $(foundation_project_identifier)"

foundation_write_metadata

if [[ "$DOCS_ONLY" == "true" ]]; then
    scaffold_sync_docs
elif [[ "$TOOLING_ONLY" == "true" ]]; then
    scaffold_sync_tooling
elif [[ "$FOUNDATION_ONLY" == "true" ]]; then
    scaffold_sync_foundation_modules
else
    scaffold_sync_foundation_modules
    scaffold_sync_docs
    scaffold_sync_tooling
    foundation_log_info "Syncing managed scaffold..."
    scaffold_apply_manifest
    foundation_log_success "Managed scaffold synchronized"
fi

foundation_log_success "Project synchronized to foundation v$FOUNDATION_VERSION"
