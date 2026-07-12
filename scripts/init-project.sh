#!/bin/bash
# Create a new project from the Foundation manifest-driven scaffold.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
FOUNDATION_VERSION="$(cat "$FOUNDATION_DIR/VERSION" 2>/dev/null || echo "1.0.0")"

source "$FOUNDATION_DIR/scripts/lib/foundation.sh"
source "$FOUNDATION_DIR/scripts/lib/scaffold.sh"

show_help() {
    cat <<'EOF'
Ovasabi Foundation Initializer

Usage: ./init.sh <project-name> [full|backend|frontend|minimal] [options]

Profiles:
  full       Go backend + React frontend + WASM baseline (default)
  backend    Go backend only
  frontend   React frontend only
  minimal    Metadata, docs, agents, and checks only

Options:
  --project-dir <path>  Create the project at an explicit path
  --go-module <path>    Custom Go module path
  --no-docker           Skip Docker scaffold
  --with-docker         Include Docker scaffold
  --no-wasm             Skip WASM scaffold and runtime-sdk
  --with-wasm           Include WASM scaffold and runtime-sdk
  --no-native           Skip native/Tauri scaffold and runtime-native
  --with-native         Include native/Tauri scaffold and runtime-native
  --dry-run             Preview without creating files
  --update              Update an existing project-owned checkout with current vendored foundation modules, docs, tooling, and frontend manifest contract
  --skip-deps           Skip dependency verification
  --why                 Explain the Foundation architecture split
  --help, -h            Show this message

Examples:
  ./init.sh civic_watch
  ./init.sh api backend --go-module github.com/ovasabi/api
  ./init.sh dashboard frontend --project-dir ../dashboard_v1
  ./init.sh civic_watch full --project-dir ../civic_watch_ng_v1 --update
EOF
}

show_why() {
    cat <<'EOF'
Foundation separates shared platform code from generated scaffold and app-owned behavior.

Layer 1: platform modules
  server-kit, runtime-transport, runtime-sdk, and config-contracts are shared, tested contracts.

Layer 2: managed scaffold
  Makefile, Docker, workflows, checks, cmd/worker, cmd/docgen, baseline frontend config, and WASM are synchronized from templates/scaffold.manifest.tsv.

Layer 3: project-owned architecture
  Domain services, handlers, route registration, business workers, and app-specific startup wiring belong to the application.

The rule is simple: Foundation owns structure and contracts; projects own domain behavior.
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

validate_project_name() {
    if [[ ! "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]]; then
        foundation_log_error "Project name must start with a letter or number and contain only letters, numbers, underscores, or hyphens"
        exit 1
    fi
}

dep_label() {
    case "$1" in
        git) echo "Git" ;;
        go) echo "Go" ;;
        node) echo "Node.js" ;;
        npm) echo "npm" ;;
        docker) echo "Docker" ;;
        cargo) echo "Rust/Cargo" ;;
        *) echo "$1" ;;
    esac
}

dep_install_hint() {
    case "$1" in
        git) echo "https://git-scm.com" ;;
        go) echo "brew install go or https://go.dev" ;;
        node) echo "brew install node or https://nodejs.org" ;;
        npm) echo "bundled with Node.js" ;;
        docker) echo "brew install --cask docker or https://docker.com" ;;
        cargo) echo "https://rustup.rs" ;;
        *) echo "" ;;
    esac
}

check_dependencies() {
    local required="git"
    local optional=""

    case "$PROFILE" in
        full)
            required="git go node npm"
            ;;
        backend)
            required="git go"
            ;;
        frontend)
            required="git node npm"
            ;;
    esac

    [[ "$WITH_DOCKER" == "true" ]] && optional="$optional docker"
    [[ "$WITH_WASM" == "true" ]] && optional="$optional cargo"
    [[ "${WITH_NATIVE:-false}" == "true" ]] && optional="$optional cargo"

    foundation_log_info "Checking environment..."

    local missing_required=""
    local dep
    for dep in $required; do
        if command -v "$dep" >/dev/null 2>&1; then
            foundation_log_success "$(dep_label "$dep") found"
        else
            foundation_log_error "$(dep_label "$dep") not found"
            missing_required="$missing_required $dep"
        fi
    done

    for dep in $optional; do
        [[ -z "$dep" ]] && continue
        if command -v "$dep" >/dev/null 2>&1; then
            foundation_log_success "$(dep_label "$dep") found"
        else
            foundation_log_warn "$(dep_label "$dep") not found; generated files will still be created"
        fi
    done

    if [[ -n "$missing_required" ]]; then
        echo ""
        foundation_log_error "Missing required dependencies:"
        for dep in $missing_required; do
            echo "  - $(dep_label "$dep"): $(dep_install_hint "$dep")"
        done
        echo "Run again after installing dependencies, or pass --skip-deps."
        exit 1
    fi
}

PROJECT_NAME=""
PROFILE="full"
PROJECT_DIR=""
GO_MODULE=""
WITH_DOCKER="true"
WITH_WASM=""
WITH_NATIVE=""
DRY_RUN="false"
SKIP_DEPS="false"
FORCE="false"
UPDATE_EXISTING="false"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            show_help
            exit 0
            ;;
        --why)
            show_why
            exit 0
            ;;
        --project-dir)
            require_value "$1" "${2:-}"
            PROJECT_DIR="$2"
            shift 2
            ;;
        --go-module)
            require_value "$1" "${2:-}"
            GO_MODULE="$2"
            shift 2
            ;;
        --no-docker)
            WITH_DOCKER="false"
            shift
            ;;
        --with-docker)
            WITH_DOCKER="true"
            shift
            ;;
        --no-wasm)
            WITH_WASM="false"
            shift
            ;;
        --with-wasm)
            WITH_WASM="true"
            shift
            ;;
        --no-native)
            WITH_NATIVE="false"
            shift
            ;;
        --with-native)
            WITH_NATIVE="true"
            shift
            ;;
        --dry-run)
            DRY_RUN="true"
            shift
            ;;
        --update)
            UPDATE_EXISTING="true"
            shift
            ;;
        --skip-deps)
            SKIP_DEPS="true"
            shift
            ;;
        full|backend|frontend|minimal)
            PROFILE="$1"
            shift
            ;;
        -*)
            foundation_log_error "Unknown option: $1"
            show_help
            exit 1
            ;;
        *)
            if [[ -n "$PROJECT_NAME" ]]; then
                foundation_log_error "Only one project name may be provided"
                exit 1
            fi
            PROJECT_NAME="$1"
            shift
            ;;
    esac
done

if [[ -z "$PROJECT_NAME" ]]; then
    foundation_log_error "Project name is required"
    show_help
    exit 1
fi

validate_project_name "$PROJECT_NAME"
validate_profile "$PROFILE"

if [[ -z "$WITH_WASM" ]]; then
    if [[ "$PROFILE" == "full" ]]; then
        WITH_WASM="true"
    else
        WITH_WASM="false"
    fi
fi

if [[ -z "$WITH_NATIVE" ]]; then
    if [[ "$PROFILE" == "full" ]]; then
        WITH_NATIVE="true"
    else
        WITH_NATIVE="false"
    fi
fi

if [[ "$WITH_NATIVE" == "true" && "$WITH_WASM" != "true" ]]; then
    foundation_log_warn "Native scaffold uses runtime-sdk; enabling WITH_WASM=true"
    WITH_WASM="true"
fi

if [[ "$WITH_NATIVE" == "true" ]]; then
    foundation_validate_project_identifier "$(foundation_project_identifier)" || exit 1
fi

if [[ "$PROFILE" == "minimal" ]]; then
    WITH_DOCKER="false"
fi

[[ -z "$GO_MODULE" ]] && GO_MODULE="github.com/ovasabi/$PROJECT_NAME"
[[ -z "$PROJECT_DIR" ]] && PROJECT_DIR="$(dirname "$FOUNDATION_DIR")/${PROJECT_NAME}_v1"

case "$PROJECT_DIR" in
    /*) PROJECT_PATH="$PROJECT_DIR" ;;
    *) PROJECT_PATH="$(pwd)/$PROJECT_DIR" ;;
esac

FOUNDATION_FILE="$PROJECT_PATH/.foundation"

foundation_log_info "Project: $PROJECT_NAME"
foundation_log_info "Profile: $PROFILE"
foundation_log_info "Path: $PROJECT_PATH"
[[ "$PROFILE" == "full" || "$PROFILE" == "backend" ]] && foundation_log_info "Go module: $GO_MODULE"
foundation_log_info "Docker: $WITH_DOCKER"
foundation_log_info "WASM: $WITH_WASM"
foundation_log_info "Native: $WITH_NATIVE"
[[ "$WITH_NATIVE" == "true" ]] && foundation_log_info "Native identifier: $(foundation_project_identifier)"
[[ "$UPDATE_EXISTING" == "true" ]] && foundation_log_info "Mode: update existing project"

if [[ "$DRY_RUN" == "true" ]]; then
    foundation_log_info "[DRY RUN] Would create project from manifest-driven scaffold"
    exit 0
fi

if [[ -e "$PROJECT_PATH" && "$UPDATE_EXISTING" != "true" ]]; then
    foundation_log_error "Directory already exists: $PROJECT_PATH"
    exit 1
fi

if [[ "$UPDATE_EXISTING" == "true" && ! -d "$PROJECT_PATH" ]]; then
    foundation_log_error "Cannot update missing project directory: $PROJECT_PATH"
    exit 1
fi

if [[ "$SKIP_DEPS" != "true" ]]; then
    check_dependencies
fi

if [[ "$UPDATE_EXISTING" == "true" ]]; then
    foundation_log_info "Updating managed foundation surfaces..."
    scaffold_sync_foundation_modules
    scaffold_sync_docs
    scaffold_sync_tooling
    scaffold_sync_frontend_manifest_contract
    foundation_log_success "$PROJECT_NAME foundation surfaces updated at $PROJECT_PATH"
    exit 0
fi

mkdir -p "$PROJECT_PATH"

if command -v git >/dev/null 2>&1; then
    git -C "$PROJECT_PATH" init -q
fi

foundation_write_metadata
scaffold_sync_foundation_modules
scaffold_sync_docs
scaffold_sync_tooling
foundation_log_info "Creating managed scaffold..."
scaffold_apply_manifest
foundation_log_success "Managed scaffold created"
if [[ -x "$FOUNDATION_DIR/tooling/scripts/scaffold_managed_patches.sh" ]]; then
    foundation_log_info "Applying managed scaffold patches..."
    "$FOUNDATION_DIR/tooling/scripts/scaffold_managed_patches.sh" "$PROJECT_PATH"
    foundation_log_success "Managed scaffold patches applied"
fi
printf '%s\t%s\n' "20260712-managed-compat-v1" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$PROJECT_PATH/.foundation-migrations.tsv"

foundation_log_success "$PROJECT_NAME created at $PROJECT_PATH"
echo ""
echo "Next steps:"
echo "  cd $PROJECT_PATH"
echo "  make lint-foundation"
[[ "$PROFILE" == "full" || "$PROFILE" == "backend" ]] && echo "  go test ./cmd/worker ./cmd/docgen ./internal/worker"
