#!/bin/bash

set -euo pipefail

profile_matches() {
    local profiles="$1"
    [[ "$profiles" == "all" ]] && return 0
    case ",$profiles," in
        *,"$PROFILE",*) return 0 ;;
        *) return 1 ;;
    esac
}

feature_matches() {
    local feature="$1"
    case "$feature" in
        always) return 0 ;;
        docker) [[ "${WITH_DOCKER:-false}" == "true" ]] ;;
        wasm) [[ "${WITH_WASM:-false}" == "true" ]] ;;
        *) return 1 ;;
    esac
}

scaffold_expand_path() {
    local value="$1"
    local frontend_root="frontend"
    [[ "$PROFILE" == "frontend" ]] && frontend_root="."
    value="${value//'{{FRONTEND_ROOT}}'/$frontend_root}"
    echo "$value"
}

scaffold_should_overwrite() {
    local mode="$1"
    local dest="$2"

    case "$mode" in
        overwrite) return 0 ;;
        force)
            [[ "${FORCE:-false}" == "true" || ! -e "$dest" ]]
            return
            ;;
        create)
            [[ ! -e "$dest" ]]
            return
            ;;
        *)
            [[ ! -e "$dest" ]]
            return
            ;;
    esac
}

scaffold_copy_file() {
    local source="$1"
    local dest="$2"
    local mode="$3"

    [[ -f "$source" ]] || return 0

    if [[ "$dest" == "$PROJECT_PATH"/migrations/000001_init.*.sql && ! -e "$dest" ]]; then
        if [[ -d "$PROJECT_PATH/migrations" ]] && [[ -n "$(find "$PROJECT_PATH/migrations" -maxdepth 1 -type f -name '*.sql' ! -name '000001_init.up.sql' ! -name '000001_init.down.sql' -print -quit 2>/dev/null)" ]]; then
            foundation_log_info "Skipping seed migration $(basename "$dest"); project already owns migrations"
            return 0
        fi
    fi

    if [[ "$dest" == "$PROJECT_PATH"/internal/startup/dependencies.go && ! -e "$dest" ]]; then
        if [[ -d "$PROJECT_PATH/internal/startup" ]] && [[ -n "$(find "$PROJECT_PATH/internal/startup" -maxdepth 1 -type f -name '*.go' ! -name 'dependencies.go' ! -name 'logger.go' -print -quit 2>/dev/null)" ]]; then
            foundation_log_info "Skipping startup dependencies baseline; project already owns startup wiring"
            return 0
        fi
    fi

    scaffold_should_overwrite "$mode" "$dest" || return 0

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would copy $source -> $dest"
        return 0
    fi

    mkdir -p "$(dirname "$dest")"
    cp "$source" "$dest"
    foundation_render_file "$dest"
    [[ "$(basename "$dest")" == "start.sh" ]] && chmod +x "$dest" 2>/dev/null || true
}

scaffold_sync_frontend_manifest_contract() {
    [[ "$PROFILE" == "full" || "$PROFILE" == "frontend" ]] || return 0

    local frontend_root="$PROJECT_PATH/frontend"
    [[ "$PROFILE" == "frontend" ]] && frontend_root="$PROJECT_PATH"

    local target_manifest="$frontend_root/package.json"
    local template_manifest="$FOUNDATION_DIR/templates/frontend/package.json"
    local sync_script="$FOUNDATION_DIR/tooling/scripts/frontend_manifest_sync.mjs"

    [[ -f "$target_manifest" && -f "$template_manifest" && -f "$sync_script" ]] || return 0

    if ! command -v node >/dev/null 2>&1; then
        foundation_log_warn "Node.js not found; skipping frontend manifest contract sync"
        return 0
    fi

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would synchronize frontend manifest contract"
        return 0
    fi

    node "$sync_script" "$template_manifest" "$target_manifest"
}

scaffold_apply_manifest() {
    local manifest="$FOUNDATION_DIR/templates/scaffold.manifest.tsv"
    [[ -f "$manifest" ]] || {
        foundation_log_error "Missing scaffold manifest: $manifest"
        return 1
    }

    while IFS=$'\t' read -r source dest profiles feature mode; do
        [[ -z "${source:-}" || "${source:0:1}" == "#" ]] && continue
        profile_matches "$profiles" || continue
        feature_matches "$feature" || continue

        local expanded_dest
        expanded_dest="$(scaffold_expand_path "$dest")"
        scaffold_copy_file "$FOUNDATION_DIR/$source" "$PROJECT_PATH/$expanded_dest" "$mode"
    done < "$manifest"

    scaffold_sync_frontend_manifest_contract
    scaffold_remove_empty_pkg_dir
}

scaffold_sync_foundation_modules() {
    foundation_log_info "Syncing vendored foundation modules..."

    if [[ -L "$PROJECT_PATH/foundation" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would replace symlinked foundation directory with a real vendored copy"
        else
            rm "$PROJECT_PATH/foundation"
        fi
    fi

    if [[ "$PROFILE" == "full" || "$PROFILE" == "backend" || "$PROFILE" == "frontend" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would ensure foundation module directory"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            cp "$FOUNDATION_DIR/.gitignore" "$PROJECT_PATH/foundation/.gitignore"
        fi
    fi

    if [[ "$PROFILE" == "full" || "$PROFILE" == "backend" ]]; then
        for dir in server-kit runtime-transport config-contracts tooling; do
            if [[ "${DRY_RUN:-false}" == "true" ]]; then
                foundation_log_info "[DRY RUN] Would refresh foundation/$dir"
            else
                rm -rf "$PROJECT_PATH/foundation/$dir"
                cp -R "$FOUNDATION_DIR/$dir" "$PROJECT_PATH/foundation/"
            fi
        done
        if [[ "${DRY_RUN:-false}" != "true" ]]; then
            mkdir -p "$PROJECT_PATH/api/protos"
            cp -R "$FOUNDATION_DIR/runtime-transport/protos/." "$PROJECT_PATH/api/protos/" 2>/dev/null || true
        fi
    fi

    if [[ "$PROFILE" == "full" || "$PROFILE" == "frontend" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would refresh foundation/ui-minimal and foundation/frontend-kit"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            rm -rf "$PROJECT_PATH/foundation/ui-minimal"
            rm -rf "$PROJECT_PATH/foundation/frontend-kit"
            cp -R "$FOUNDATION_DIR/ui-minimal" "$PROJECT_PATH/foundation/"
            cp -R "$FOUNDATION_DIR/frontend-kit" "$PROJECT_PATH/foundation/"
        fi
    fi

    if [[ "$WITH_WASM" == "true" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would refresh foundation/runtime-sdk"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            rm -rf "$PROJECT_PATH/foundation/runtime-sdk"
            cp -R "$FOUNDATION_DIR/runtime-sdk" "$PROJECT_PATH/foundation/"
        fi
    fi

    foundation_log_success "Foundation modules synchronized"
}

scaffold_sync_docs() {
    foundation_log_info "Syncing foundation documentation..."
    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would refresh docs/foundation"
        return
    fi

    rm -rf "$PROJECT_PATH/docs/foundation"
    mkdir -p "$PROJECT_PATH/docs/foundation"
    cp -R "$FOUNDATION_DIR/docs/." "$PROJECT_PATH/docs/foundation/"
    foundation_log_success "Documentation synchronized"
}

scaffold_sync_tooling() {
    foundation_log_info "Syncing tooling and checks..."
    scaffold_copy_file "$FOUNDATION_DIR/tooling/golangci/.golangci.yml" "$PROJECT_PATH/.golangci.yml" "overwrite"
    scaffold_copy_file "$FOUNDATION_DIR/tooling/rust/clippy.toml" "$PROJECT_PATH/clippy.toml" "overwrite"
    scaffold_copy_file "$FOUNDATION_DIR/tooling/rust/rustfmt.toml" "$PROJECT_PATH/rustfmt.toml" "overwrite"

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would refresh scripts/checks"
    else
        mkdir -p "$PROJECT_PATH/scripts/checks"
        cp -R "$FOUNDATION_DIR/tooling/scripts/." "$PROJECT_PATH/scripts/checks/"
        chmod +x "$PROJECT_PATH"/scripts/checks/*.sh 2>/dev/null || true
    fi

    foundation_log_success "Tooling synchronized"
}

scaffold_remove_empty_pkg_dir() {
    local pkg_dir="$PROJECT_PATH/pkg"
    [[ -d "$pkg_dir" ]] || return 0

    if [[ -n "$(find "$pkg_dir" -mindepth 1 -print -quit 2>/dev/null)" ]]; then
        foundation_log_warn "Leaving non-empty pkg/ in place; move shared code into foundation modules or document ownership"
        return 0
    fi

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would remove empty pkg/"
        return 0
    fi

    rmdir "$pkg_dir"
}
