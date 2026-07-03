#!/bin/bash

set -euo pipefail

foundation_script_dir() {
    cd "$(dirname "${BASH_SOURCE[1]}")" && pwd
}

foundation_escape() {
    printf '%q' "$1"
}

foundation_project_identifier() {
    if [[ -n "${PROJECT_IDENTIFIER:-}" ]]; then
        echo "$PROJECT_IDENTIFIER"
        return
    fi
    local raw="${PROJECT_NAME:-foundation-app}"
    local normalized
    normalized="$(echo "$raw" | tr '[:upper:]_' '[:lower:].' | sed -E 's/[^a-z0-9.-]+/-/g; s/^[.-]+//; s/[.-]+$//')"
    if [[ -z "$normalized" ]]; then
        normalized="foundation-app"
    fi
    if [[ ! "$normalized" =~ ^[a-z] ]]; then
        normalized="app-$normalized"
    fi
    echo "com.ovasabi.$normalized"
}

foundation_validate_project_identifier() {
    local identifier="$1"
    if ! printf '%s\n' "$identifier" | grep -Eq '^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*){2,}$'; then
        foundation_log_error "Native app identifier must use reverse-domain notation such as com.ovasabi.trotters"
        foundation_log_error "Actual identifier: ${identifier:-<empty>}"
        return 1
    fi
}

foundation_sed_in_place() {
    local expression="$1"
    local file="$2"
    local body search replace
    body="${expression#s|}"
    search="${body%%|*}"
    body="${body#*|}"
    replace="${body%|g}"
    PATCH_SEARCH="$search" PATCH_REPLACE="$replace" perl -0pi -e 's/\Q$ENV{PATCH_SEARCH}\E/$ENV{PATCH_REPLACE}/g' "$file"
}

foundation_render_file() {
    local file="$1"
    [[ -f "$file" ]] || return 0

    foundation_sed_in_place "s|{{PROJECT_NAME}}|${PROJECT_NAME}|g" "$file"
    foundation_sed_in_place "s|{{PROJECT_IDENTIFIER}}|$(foundation_project_identifier)|g" "$file"
    foundation_sed_in_place "s|{{MODULE_PATH}}|${GO_MODULE}|g" "$file"
    foundation_sed_in_place "s|{{FOUNDATION_VERSION}}|${FOUNDATION_VERSION}|g" "$file"
    foundation_sed_in_place "s|{{TIMESTAMP}}|$(date -u +"%Y-%m-%dT%H:%M:%SZ")|g" "$file"
}

foundation_render_tree() {
    local root="$1"
    [[ -d "$root" ]] || return 0

    while IFS= read -r -d '' file; do
        foundation_render_file "$file"
    done < <(
        find "$root" -type f \
            \( -name "*.md" -o -name "*.json" -o -name "*.yml" -o -name "*.yaml" -o \
               -name "*.ts" -o -name "*.tsx" -o -name "*.js" -o -name "*.go" -o \
               -name "*.proto" -o -name "*.sql" -o -name "*.html" -o -name "*.template" \) -print0
    )
}

foundation_hash_file() {
    local file="$1"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    else
        echo "missing sha256 tool" >&2
        return 2
    fi
}

foundation_log_info() { echo -e "\033[0;34m[INFO]\033[0m $1"; }
foundation_log_success() { echo -e "\033[0;32m[SUCCESS]\033[0m $1"; }
foundation_log_warn() { echo -e "\033[1;33m[WARN]\033[0m $1"; }
foundation_log_error() { echo -e "\033[0;31m[ERROR]\033[0m $1"; }

foundation_read_metadata_value() {
    local file="$1"
    local key="$2"
    [[ -f "$file" ]] || return 1
    grep -m1 "^${key}=" "$file" | cut -d'=' -f2-
}

foundation_infer_profile() {
    if [[ -f "$PROJECT_PATH/.foundation" ]] && grep -q '^PROFILE=' "$PROJECT_PATH/.foundation"; then
        foundation_read_metadata_value "$PROJECT_PATH/.foundation" PROFILE
        return
    fi

    if [[ -f "$PROJECT_PATH/go.mod" && -f "$PROJECT_PATH/frontend/package.json" ]]; then
        echo "full"
    elif [[ -f "$PROJECT_PATH/go.mod" ]]; then
        echo "backend"
    elif [[ -f "$PROJECT_PATH/package.json" || -f "$PROJECT_PATH/frontend/package.json" ]]; then
        echo "frontend"
    else
        echo "minimal"
    fi
}

foundation_infer_go_module() {
    if [[ -f "$PROJECT_PATH/.foundation" ]] && grep -q '^GO_MODULE=' "$PROJECT_PATH/.foundation"; then
        foundation_read_metadata_value "$PROJECT_PATH/.foundation" GO_MODULE
        return
    fi

    if [[ -f "$PROJECT_PATH/go.mod" ]]; then
        grep -m1 '^module ' "$PROJECT_PATH/go.mod" | awk '{print $2}'
        return
    fi

    echo "github.com/ovasabi/$PROJECT_NAME"
}

foundation_infer_flag() {
    local key="$1"
    local fallback="$2"

    if [[ -f "$PROJECT_PATH/.foundation" ]] && grep -q "^${key}=" "$PROJECT_PATH/.foundation"; then
        foundation_read_metadata_value "$PROJECT_PATH/.foundation" "$key"
        return
    fi

    echo "$fallback"
}

foundation_write_metadata() {
    local created_at
    created_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    if [[ -f "$FOUNDATION_FILE" ]] && grep -q '^CREATED_AT=' "$FOUNDATION_FILE"; then
        created_at="$(foundation_read_metadata_value "$FOUNDATION_FILE" CREATED_AT)"
    fi

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would refresh $FOUNDATION_FILE"
        return
    fi

    cat > "$FOUNDATION_FILE" <<EOF
FOUNDATION_VERSION=$FOUNDATION_VERSION
FOUNDATION_PATH=$(foundation_escape "$FOUNDATION_DIR")
CREATED_AT=$created_at
LAST_UPDATED=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PROFILE=$PROFILE
PROJECT_NAME=$PROJECT_NAME
GO_MODULE=$GO_MODULE
PROJECT_IDENTIFIER=$(foundation_project_identifier)
WITH_DOCKER=$WITH_DOCKER
WITH_WASM=$WITH_WASM
WITH_NATIVE=${WITH_NATIVE:-false}
BASELINE_GENERATION=manifest-v4
EOF
}
