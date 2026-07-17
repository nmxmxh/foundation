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
        native) [[ "${WITH_NATIVE:-false}" == "true" ]] ;;
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

# Seed ledger: records, for every create-mode file, the hash of the template
# it was seeded from and the hash of the rendered file as written. Update uses
# it to warn when the Foundation template evolves after seeding — without ever
# rewriting project-owned files, and without flagging ordinary user edits.
scaffold_seed_ledger_path() {
    echo "$PROJECT_PATH/.foundation-seeds.tsv"
}

scaffold_seed_ledger_row() {
    local dest_rel="$1"
    local ledger
    ledger="$(scaffold_seed_ledger_path)"
    [[ -f "$ledger" ]] || return 0
    awk -F'\t' -v d="$dest_rel" '$1 == d { print; exit }' "$ledger"
}

scaffold_record_seed() {
    local dest_rel="$1"
    local template_hash="$2"
    local seeded_hash="$3"
    [[ "${DRY_RUN:-false}" == "true" ]] && return 0

    local ledger tmp
    ledger="$(scaffold_seed_ledger_path)"
    tmp="${ledger}.tmp"
    {
        echo "# destination	template_sha256	seeded_sha256"
        {
            if [[ -f "$ledger" ]]; then
                awk -F'\t' -v d="$dest_rel" '/^#/ { next } $1 != d { print }' "$ledger"
            fi
            printf '%s\t%s\t%s\n' "$dest_rel" "$template_hash" "$seeded_hash"
        } | sort
    } >"$tmp"
    mv "$tmp" "$ledger"
}

scaffold_warn_seed_drift() {
    local dest_rel="$1"
    local source_abs="$2"
    local dest_abs="$3"
    local seeded_hash="$4"

    local file_now
    file_now="$(foundation_hash_file "$dest_abs")"
    if [[ "$file_now" == "$seeded_hash" ]]; then
        foundation_log_warn "Seed drift: $dest_rel — Foundation template evolved; local copy is unmodified since seeding. Delete the file and re-run update to reseed it."
    else
        foundation_log_warn "Seed drift: $dest_rel — Foundation template evolved and the local copy is customized. Review: diff \"$dest_abs\" \"$source_abs\""
    fi
}

scaffold_reseed_untouched() {
    local dest_rel="$1"
    local source_abs="$2"
    local dest_abs="$3"
    local template_hash="$4"

    if [[ "${DRY_RUN:-false}" == "true" ]]; then
        foundation_log_info "[DRY RUN] Would safely reseed untouched project-owned file: $dest_rel"
        return 0
    fi
    cp "$source_abs" "$dest_abs"
    foundation_render_file "$dest_abs"
    scaffold_record_seed "$dest_rel" "$template_hash" "$(foundation_hash_file "$dest_abs")"
    foundation_log_info "Safely reseeded untouched project-owned file: $dest_rel"
}

scaffold_report_seed_drift() {
    local manifest="$FOUNDATION_DIR/templates/scaffold.manifest.tsv"
    [[ -f "$manifest" ]] || return 0

    local drift_count=0
    local backfill_count=0
    while IFS=$'\t' read -r source dest profiles feature mode; do
        [[ -z "${source:-}" || "${source:0:1}" == "#" ]] && continue
        [[ "$mode" == "create" ]] || continue
        profile_matches "$profiles" || continue
        feature_matches "$feature" || continue

        local dest_rel source_abs dest_abs
        dest_rel="$(scaffold_expand_path "$dest")"
        source_abs="$FOUNDATION_DIR/$source"
        dest_abs="$PROJECT_PATH/$dest_rel"
        [[ -f "$source_abs" && -f "$dest_abs" ]] || continue

        local template_now row template_rec seeded_rec
        template_now="$(foundation_hash_file "$source_abs")"
        row="$(scaffold_seed_ledger_row "$dest_rel")"
        if [[ -z "$row" ]]; then
            scaffold_record_seed "$dest_rel" "$template_now" "$(foundation_hash_file "$dest_abs")"
            backfill_count=$((backfill_count + 1))
            continue
        fi
        template_rec="$(echo "$row" | cut -f2)"
        [[ "$template_now" == "$template_rec" ]] && continue

        if [[ "${ACKNOWLEDGE_SEED_DRIFT:-false}" == "true" ]]; then
            seeded_rec="$(echo "$row" | cut -f3)"
            scaffold_record_seed "$dest_rel" "$template_now" "$seeded_rec"
            foundation_log_info "Seed drift acknowledged: $dest_rel re-baselined to the current template"
            continue
        fi

        seeded_rec="$(echo "$row" | cut -f3)"
        if [[ "${AUTO_RESEED_UNTOUCHED:-true}" == "true" ]] && [[ "$(foundation_hash_file "$dest_abs")" == "$seeded_rec" ]]; then
            scaffold_reseed_untouched "$dest_rel" "$source_abs" "$dest_abs" "$template_now"
            continue
        fi
        scaffold_warn_seed_drift "$dest_rel" "$source_abs" "$dest_abs" "$seeded_rec"
        drift_count=$((drift_count + 1))
    done < "$manifest"

    if [[ "$backfill_count" -gt 0 ]]; then
        foundation_log_info "Seed ledger: recorded baseline for $backfill_count project-owned file(s)"
    fi
    if [[ "$drift_count" -gt 0 ]]; then
        foundation_log_warn "$drift_count project-owned file(s) drifted from evolved templates; review them, then re-run with --acknowledge-seed-drift to re-baseline"
    fi
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

    if [[ "$mode" == "create" ]]; then
        scaffold_record_seed "${dest#$PROJECT_PATH/}" \
            "$(foundation_hash_file "$source")" \
            "$(foundation_hash_file "$dest")"
    fi
}

scaffold_copy_tree() {
    local source="$1"
    local dest_parent="$2"
    local name
    name="$(basename "$source")"
    local dest="$dest_parent/$name"

    mkdir -p "$dest_parent"
    if command -v rsync >/dev/null 2>&1; then
        mkdir -p "$dest"
        rsync -a \
            --delete \
            --exclude '.cache/' \
            --exclude '.gocache/' \
            --exclude 'node_modules/' \
            --exclude 'dist/' \
            --exclude 'build/' \
            --exclude 'target/' \
            --exclude 'test-results/' \
            --exclude '*.test' \
            --exclude '*.prof' \
            --exclude '*.cover' \
            --exclude '*.out' \
            --exclude '.DS_Store' \
            "$source/" "$dest/"
        return
    fi

    local tmp
    tmp="$(mktemp -d "$dest_parent/.${name}.tmp.XXXXXX")"
    cp -R "$source/." "$tmp/"
    rm -rf "$dest"
    mv "$tmp" "$dest"
    find "$dest" \( \
        -name '.cache' -o \
        -name '.gocache' -o \
        -name 'node_modules' -o \
        -name 'dist' -o \
        -name 'build' -o \
        -name 'target' -o \
        -name 'test-results' \
    \) -prune -exec rm -rf {} + 2>/dev/null || true
    find "$dest" \( \
        -name '*.test' -o \
        -name '*.prof' -o \
        -name '*.cover' -o \
        -name '*.out' -o \
        -name '.DS_Store' \
    \) -delete 2>/dev/null || true
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

    local old_hash=""
    if [[ -f "$target_manifest" ]]; then
        old_hash="$(foundation_hash_file "$target_manifest")"
    fi

    WITH_NATIVE="${WITH_NATIVE:-false}" node "$sync_script" "$template_manifest" "$target_manifest"

    local new_hash=""
    if [[ -f "$target_manifest" ]]; then
        new_hash="$(foundation_hash_file "$target_manifest")"
    fi

    if [[ "$old_hash" != "$new_hash" ]]; then
        foundation_log_info "Frontend manifest contract updated; running npm install to synchronize lockfile..."
        if command -v npm >/dev/null 2>&1; then
            (cd "$frontend_root" && npm install --package-lock-only)
            foundation_log_success "Frontend lockfile synchronized"
        else
            foundation_log_warn "npm not found; lockfile may be desynchronized from manifest"
        fi
    fi
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

    if [[ "$PROFILE" == "full" || "$PROFILE" == "backend" || -f "$PROJECT_PATH/backend/go.mod" ]]; then
        for dir in server-kit runtime-transport config-contracts tooling; do
            if [[ "${DRY_RUN:-false}" == "true" ]]; then
                foundation_log_info "[DRY RUN] Would refresh foundation/$dir"
            else
                scaffold_copy_tree "$FOUNDATION_DIR/$dir" "$PROJECT_PATH/foundation"
            fi
        done
        if [[ "${DRY_RUN:-false}" != "true" ]]; then
            mkdir -p "$PROJECT_PATH/api/protos"
            rm -rf "$PROJECT_PATH/api/protos/transport" "$PROJECT_PATH/api/protos/hermes" "$PROJECT_PATH/api/protos/common"
            cp -R "$FOUNDATION_DIR/runtime-transport/protos/." "$PROJECT_PATH/api/protos/" 2>/dev/null || true
            if [[ -d "$FOUNDATION_DIR/runtime-transport/schemas" ]]; then
                mkdir -p "$PROJECT_PATH/api/schemas"
                rm -rf "$PROJECT_PATH/api/schemas/common"
                cp -R "$FOUNDATION_DIR/runtime-transport/schemas/." "$PROJECT_PATH/api/schemas/" 2>/dev/null || true
            fi
        fi
    fi

    if [[ "$PROFILE" == "full" || "$PROFILE" == "frontend" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would refresh foundation/ui-minimal and foundation/frontend-kit"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            scaffold_copy_tree "$FOUNDATION_DIR/ui-minimal" "$PROJECT_PATH/foundation"
            scaffold_copy_tree "$FOUNDATION_DIR/frontend-kit" "$PROJECT_PATH/foundation"
        fi
    fi

    if [[ "$WITH_WASM" == "true" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would refresh foundation/runtime-sdk"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            scaffold_copy_tree "$FOUNDATION_DIR/runtime-sdk" "$PROJECT_PATH/foundation"
        fi
    fi

    if [[ "${WITH_NATIVE:-false}" == "true" ]]; then
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would refresh foundation/runtime-native"
        else
            mkdir -p "$PROJECT_PATH/foundation"
            scaffold_copy_tree "$FOUNDATION_DIR/runtime-native" "$PROJECT_PATH/foundation"
        fi
    fi

    scaffold_prune_foundation_core_only_assets
    foundation_log_success "Foundation modules synchronized"
}

scaffold_prune_foundation_core_only_assets() {
    local core_only_paths=(
        "$PROJECT_PATH/foundation/server-kit/go/servicebacked"
    )

    local path
    for path in "${core_only_paths[@]}"; do
        if [[ "${DRY_RUN:-false}" == "true" ]]; then
            foundation_log_info "[DRY RUN] Would exclude core-only asset ${path#$PROJECT_PATH/}"
            continue
        fi
        [[ -e "$path" ]] && rm -rf "$path"
    done
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
        cp "$FOUNDATION_DIR/scripts/check-rust.sh" "$PROJECT_PATH/scripts/checks/check-rust.sh"
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
