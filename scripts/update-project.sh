#!/bin/bash
# Ovasabi Foundation Project Update Script
# Updates a project to the latest foundation version

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

show_help() {
    echo "Ovasabi Foundation Project Update Script"
    echo ""
    echo "Usage: ./update-project.sh <project-path> [options]"
    echo ""
    echo "Options:"
    echo "  --dry-run         Show what would be updated without making changes"
    echo "  --force           Force update even if versions match"
    echo "  --docs-only       Only update documentation"
    echo "  --tooling-only    Only update tooling configuration"
    echo "  --foundation-only Only update foundation modules (server-kit, runtime-transport, config-contracts)"
    echo "  --help            Show this help message"
    echo ""
    echo "What gets updated:"
    echo "  - Foundation modules (server-kit, runtime-transport, config-contracts)"
    echo "  - Linting configurations (.eslintrc.json, .golangci.yml)"
    echo "  - Compliance check scripts"
    echo "  - Documentation (linked, not copied)"
    echo "  - Makefile foundation targets"
}

# Parse arguments
PROJECT_PATH=""
DRY_RUN=false
FORCE=false
DOCS_ONLY=false
TOOLING_ONLY=false
FOUNDATION_ONLY=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --help)
            show_help
            exit 0
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --docs-only)
            DOCS_ONLY=true
            shift
            ;;
        --tooling-only)
            TOOLING_ONLY=true
            shift
            ;;
        --foundation-only)
            FOUNDATION_ONLY=true
            shift
            ;;
        *)
            PROJECT_PATH="$1"
            shift
            ;;
    esac
done

if [ -z "$PROJECT_PATH" ]; then
    log_error "Project path is required"
    show_help
    exit 1
fi

# Resolve absolute path
PROJECT_PATH="$(cd "$PROJECT_PATH" 2>/dev/null && pwd)"

if [ ! -d "$PROJECT_PATH" ]; then
    log_error "Project directory does not exist: $PROJECT_PATH"
    exit 1
fi

# Check for .foundation file
FOUNDATION_FILE="$PROJECT_PATH/.foundation"
if [ ! -f "$FOUNDATION_FILE" ]; then
    log_error "Not a foundation project (missing .foundation file)"
    exit 1
fi

# Read project metadata
PROFILE=$(grep "PROFILE=" "$FOUNDATION_FILE" | cut -d'=' -f2)
PROJECT_FOUNDATION_VERSION=$(grep "FOUNDATION_VERSION=" "$FOUNDATION_FILE" | cut -d'=' -f2)

# Get current foundation version
FOUNDATION_VERSION="1.0.0"  # Read from foundation version file if exists
if [ -f "$FOUNDATION_DIR/VERSION" ]; then
    FOUNDATION_VERSION=$(cat "$FOUNDATION_DIR/VERSION")
fi

log_info "Project: $PROJECT_PATH"
log_info "Profile: $PROFILE"
log_info "Project Foundation Version: $PROJECT_FOUNDATION_VERSION"
log_info "Current Foundation Version: $FOUNDATION_VERSION"

has_foundation_drift() {
    for path in foundation/server-kit foundation/runtime-transport foundation/config-contracts foundation/ui-minimal docs/foundation; do
        case "$path" in
            foundation/server-kit)
                [ -d "$FOUNDATION_DIR/server-kit" ] && [ -d "$PROJECT_PATH/$path" ] || continue
                diff -qr "$FOUNDATION_DIR/server-kit" "$PROJECT_PATH/$path" >/dev/null 2>&1 || return 0
                ;;
            foundation/runtime-transport)
                [ -d "$FOUNDATION_DIR/runtime-transport" ] && [ -d "$PROJECT_PATH/$path" ] || continue
                diff -qr "$FOUNDATION_DIR/runtime-transport" "$PROJECT_PATH/$path" >/dev/null 2>&1 || return 0
                ;;
            foundation/config-contracts)
                [ -d "$FOUNDATION_DIR/config-contracts" ] && [ -d "$PROJECT_PATH/$path" ] || continue
                diff -qr "$FOUNDATION_DIR/config-contracts" "$PROJECT_PATH/$path" >/dev/null 2>&1 || return 0
                ;;
            foundation/ui-minimal)
                [ -d "$FOUNDATION_DIR/ui-minimal" ] && [ -d "$PROJECT_PATH/$path" ] || continue
                diff -qr "$FOUNDATION_DIR/ui-minimal" "$PROJECT_PATH/$path" >/dev/null 2>&1 || return 0
                ;;
            docs/foundation)
                [ -d "$FOUNDATION_DIR/docs" ] && [ -d "$PROJECT_PATH/$path" ] || continue
                diff -qr "$FOUNDATION_DIR/docs" "$PROJECT_PATH/$path" >/dev/null 2>&1 || return 0
                ;;
        esac
    done
    return 1
}

if [ "$PROJECT_FOUNDATION_VERSION" = "$FOUNDATION_VERSION" ] && [ "$FORCE" = "false" ] && ! has_foundation_drift; then
    log_success "Project is already up to date!"
    exit 0
fi

# Update functions
update_foundation_modules() {
    log_info "Updating foundation modules..."

    if [ "$DRY_RUN" = "true" ]; then
        log_info "[DRY RUN] Would update foundation/server-kit"
        log_info "[DRY RUN] Would update foundation/runtime-transport"
        log_info "[DRY RUN] Would update foundation/config-contracts"
        log_info "[DRY RUN] Would update foundation/ui-minimal (if frontend profile)"
        log_info "[DRY RUN] Would update foundation/runtime-sdk (if WASM)"
        log_info "[DRY RUN] Would update api/protos/"
        log_info "[DRY RUN] Would update docs/foundation/"
        return
    fi

    # Create foundation directory if needed
    mkdir -p "$PROJECT_PATH/foundation"

    # Update server-kit (required for backend profiles)
    if [ "$PROFILE" = "full" ] || [ "$PROFILE" = "backend" ]; then
        if [ -d "$FOUNDATION_DIR/server-kit" ]; then
            rm -rf "$PROJECT_PATH/foundation/server-kit"
            cp -r "$FOUNDATION_DIR/server-kit" "$PROJECT_PATH/foundation/"
            log_success "Updated foundation/server-kit"
        else
            log_error "server-kit not found in foundation"
        fi
    fi

    # Update runtime-transport (required - has protos and transport)
    if [ -d "$FOUNDATION_DIR/runtime-transport" ]; then
        rm -rf "$PROJECT_PATH/foundation/runtime-transport"
        cp -r "$FOUNDATION_DIR/runtime-transport" "$PROJECT_PATH/foundation/"
        log_success "Updated foundation/runtime-transport"
    else
        log_error "runtime-transport not found in foundation"
    fi

    # Update config-contracts
    if [ -d "$FOUNDATION_DIR/config-contracts" ]; then
        rm -rf "$PROJECT_PATH/foundation/config-contracts"
        cp -r "$FOUNDATION_DIR/config-contracts" "$PROJECT_PATH/foundation/"
        log_success "Updated foundation/config-contracts"
    else
        log_warn "config-contracts not found in foundation"
    fi

    # Update ui-minimal for frontend profiles
    if [ "$PROFILE" = "full" ] || [ "$PROFILE" = "frontend" ]; then
        if [ -d "$FOUNDATION_DIR/ui-minimal" ]; then
            rm -rf "$PROJECT_PATH/foundation/ui-minimal"
            cp -r "$FOUNDATION_DIR/ui-minimal" "$PROJECT_PATH/foundation/"
            log_success "Updated foundation/ui-minimal"
        else
            log_warn "ui-minimal not found in foundation"
        fi
    fi

    # Update runtime-sdk if WASM directory exists in project
    if [ -d "$PROJECT_PATH/wasm" ] || [ -d "$PROJECT_PATH/foundation/runtime-sdk" ]; then
        if [ -d "$FOUNDATION_DIR/runtime-sdk" ]; then
            rm -rf "$PROJECT_PATH/foundation/runtime-sdk"
            cp -r "$FOUNDATION_DIR/runtime-sdk" "$PROJECT_PATH/foundation/"
            log_success "Updated foundation/runtime-sdk"
        fi
    fi

    # Update protocol definitions
    log_info "Updating protocol definitions..."
    if [ -d "$FOUNDATION_DIR/runtime-transport/protos" ]; then
        mkdir -p "$PROJECT_PATH/api/protos"
        rm -rf "$PROJECT_PATH/api/protos/transport"
        cp -r "$FOUNDATION_DIR/runtime-transport/protos/"* "$PROJECT_PATH/api/protos/" 2>/dev/null || true
        log_success "Updated api/protos/"
    fi

    # Update foundation documentation
    log_info "Updating foundation documentation..."
    if [ -d "$FOUNDATION_DIR/docs" ]; then
        rm -rf "$PROJECT_PATH/docs/foundation"
        mkdir -p "$PROJECT_PATH/docs/foundation"
        cp -R "$FOUNDATION_DIR/docs/." "$PROJECT_PATH/docs/foundation/" 2>/dev/null || true
        log_success "Updated docs/foundation/"
    fi

    log_success "Foundation modules updated"
}

update_tooling() {
    log_info "Updating tooling configuration..."

    if [ "$DRY_RUN" = "true" ]; then
        log_info "[DRY RUN] Would update .golangci.yml"
        log_info "[DRY RUN] Would update .eslintrc.json"
        return
    fi

    # Update Go linting config
    if [ -f "$FOUNDATION_DIR/tooling/golangci/.golangci.yml" ]; then
        cp "$FOUNDATION_DIR/tooling/golangci/.golangci.yml" "$PROJECT_PATH/.golangci.yml" 2>/dev/null || true
        log_success "Updated .golangci.yml"
    fi

    # Update ESLint config for frontend projects
    if [ "$PROFILE" = "full" ] || [ "$PROFILE" = "frontend" ]; then
        FRONTEND_DIR="$PROJECT_PATH/frontend"
        if [ "$PROFILE" = "frontend" ]; then
            FRONTEND_DIR="$PROJECT_PATH"
        fi

        if [ -f "$FOUNDATION_DIR/tooling/eslint/base.config.js" ] && [ -d "$FRONTEND_DIR" ]; then
            # Don't overwrite, just report
            log_info "ESLint config available at: $FOUNDATION_DIR/tooling/eslint/"
        fi
    fi
}

update_scripts() {
    log_info "Updating compliance scripts..."

    if [ "$DRY_RUN" = "true" ]; then
        log_info "[DRY RUN] Would update compliance check scripts"
        return
    fi

    # Create scripts directory if needed
    mkdir -p "$PROJECT_PATH/scripts/checks"

    # Copy compliance check scripts
    for script in coding_practices_check.sh database_practices_check.sh redis_practices_check.sh contract_drift_check.sh migration_structure_check.sh; do
        if [ -f "$FOUNDATION_DIR/tooling/scripts/$script" ]; then
            cp "$FOUNDATION_DIR/tooling/scripts/$script" "$PROJECT_PATH/scripts/checks/"
            chmod +x "$PROJECT_PATH/scripts/checks/$script"
        fi
    done

    log_success "Updated compliance scripts"
}

update_docs_link() {
    log_info "Updating documentation link..."

    if [ "$DRY_RUN" = "true" ]; then
        log_info "[DRY RUN] Would update docs/foundation link"
        return
    fi

    # Create symlink to foundation docs (if not exists)
    DOCS_LINK="$PROJECT_PATH/docs/foundation"
    if [ ! -L "$DOCS_LINK" ]; then
        mkdir -p "$PROJECT_PATH/docs"
        ln -sf "$FOUNDATION_DIR/docs" "$DOCS_LINK"
        log_success "Created link to foundation docs"
    fi
}

update_foundation_file() {
    log_info "Updating .foundation file..."

    if [ "$DRY_RUN" = "true" ]; then
        log_info "[DRY RUN] Would update .foundation version to $FOUNDATION_VERSION"
        return
    fi

    # Update version in .foundation file
    sed -i.bak "s/FOUNDATION_VERSION=.*/FOUNDATION_VERSION=$FOUNDATION_VERSION/" "$FOUNDATION_FILE"
    rm -f "${FOUNDATION_FILE}.bak"

    # Add update timestamp
    if ! grep -q "LAST_UPDATED=" "$FOUNDATION_FILE"; then
        echo "LAST_UPDATED=$(date -u +"%Y-%m-%dT%H:%M:%SZ")" >> "$FOUNDATION_FILE"
    else
        sed -i.bak "s/LAST_UPDATED=.*/LAST_UPDATED=$(date -u +"%Y-%m-%dT%H:%M:%SZ")/" "$FOUNDATION_FILE"
        rm -f "${FOUNDATION_FILE}.bak"
    fi

    log_success "Updated .foundation file"
}

# Perform updates based on options
if [ "$DOCS_ONLY" = "true" ]; then
    update_docs_link
elif [ "$TOOLING_ONLY" = "true" ]; then
    update_tooling
elif [ "$FOUNDATION_ONLY" = "true" ]; then
    update_foundation_modules
else
    # Full update - foundation modules first, then tooling/scripts/docs
    update_foundation_modules
    update_tooling
    update_scripts
    update_docs_link
    update_foundation_file
fi

echo ""
log_success "Project updated to foundation version $FOUNDATION_VERSION"

if [ "$DRY_RUN" = "true" ]; then
    log_warn "This was a dry run. No changes were made."
fi

echo ""
echo "Changelog:"
echo "  - See $FOUNDATION_DIR/CHANGELOG.md for foundation updates"
echo ""
echo "Next steps:"
echo "  - Review any breaking changes in the changelog"
echo "  - Run 'make test' to verify everything works"
echo "  - Run 'make lint' to check for new linting rules"
