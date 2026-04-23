#!/bin/bash
# Ovasabi Foundation Documentation Generator
# Generates API documentation from Go code and OpenAPI specs

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Default values
OUTPUT_DIR="docs/api"
FORMAT="markdown"
INCLUDE_PRIVATE=false

show_help() {
    echo "Ovasabi Foundation Documentation Generator"
    echo ""
    echo "Usage: docgen.sh [options]"
    echo ""
    echo "Options:"
    echo "  --output, -o <dir>     Output directory (default: docs/api)"
    echo "  --format, -f <format>  Output format: markdown, html, json (default: markdown)"
    echo "  --include-private      Include private/internal APIs"
    echo "  --openapi <file>       Path to OpenAPI spec file"
    echo "  --help                 Show this help message"
    echo ""
    echo "Examples:"
    echo "  ./scripts/docgen.sh"
    echo "  ./scripts/docgen.sh --output docs/api --format html"
    echo "  ./scripts/docgen.sh --openapi api/openapi.yaml"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --help|-h)
            show_help
            exit 0
            ;;
        --output|-o)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --format|-f)
            FORMAT="$2"
            shift 2
            ;;
        --include-private)
            INCLUDE_PRIVATE=true
            shift
            ;;
        --openapi)
            OPENAPI_FILE="$2"
            shift 2
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Create output directory
mkdir -p "$OUTPUT_DIR"

log_info "Generating documentation..."
log_info "Output: $OUTPUT_DIR"
log_info "Format: $FORMAT"

# Generate API documentation header
generate_header() {
    local project_name=$(basename "$(pwd)" | sed 's/_v[0-9]*$//')
    cat > "$OUTPUT_DIR/README.md" <<EOF
# ${project_name} API Documentation

Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")

## Overview

This documentation describes the REST API endpoints for ${project_name}.

## Base URL

- Development: \`http://localhost:8080/api\`
- Production: \`https://api.${project_name}.com/api\`

## Authentication

Most endpoints require authentication via Bearer token:

\`\`\`
Authorization: Bearer <token>
\`\`\`

## Response Format

All responses follow this structure:

\`\`\`json
{
  "data": { ... },
  "meta": {
    "page": 1,
    "limit": 20,
    "total": 100
  }
}
\`\`\`

## Error Format

\`\`\`json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "Resource not found",
    "details": { ... }
  }
}
\`\`\`

## Endpoints

EOF
}

# Extract endpoints from Go handlers
extract_go_endpoints() {
    log_info "Extracting endpoints from Go handlers..."

    # Find all handler files
    handler_files=$(find . -name "*handler*.go" -o -name "*route*.go" 2>/dev/null | grep -v vendor | grep -v _test.go || true)

    if [ -z "$handler_files" ]; then
        log_warn "No handler files found"
        return
    fi

    # Create endpoints section
    echo "" >> "$OUTPUT_DIR/README.md"
    echo "### Extracted Endpoints" >> "$OUTPUT_DIR/README.md"
    echo "" >> "$OUTPUT_DIR/README.md"

    # Extract HTTP method patterns from Go files
    for file in $handler_files; do
        # Look for HandleFunc patterns
        grep -E '(HandleFunc|Handle)\s*\(\s*"(GET|POST|PUT|PATCH|DELETE)\s+' "$file" 2>/dev/null | while read -r line; do
            method=$(echo "$line" | grep -oE '(GET|POST|PUT|PATCH|DELETE)')
            path=$(echo "$line" | grep -oE '"/[^"]*"' | tr -d '"')
            if [ -n "$method" ] && [ -n "$path" ]; then
                echo "- \`$method $path\`" >> "$OUTPUT_DIR/README.md"
            fi
        done
    done
}

# Generate OpenAPI documentation if spec file exists
generate_openapi_docs() {
    if [ -n "$OPENAPI_FILE" ] && [ -f "$OPENAPI_FILE" ]; then
        log_info "Processing OpenAPI spec: $OPENAPI_FILE"

        # Check for redocly or swagger-cli
        if command -v redocly &> /dev/null; then
            redocly build-docs "$OPENAPI_FILE" --output "$OUTPUT_DIR/api.html"
            log_success "Generated HTML docs with Redocly"
        elif command -v npx &> /dev/null; then
            npx @redocly/cli build-docs "$OPENAPI_FILE" --output "$OUTPUT_DIR/api.html" 2>/dev/null || \
            log_warn "Redocly not available, skipping HTML generation"
        fi

        # Copy spec to output
        cp "$OPENAPI_FILE" "$OUTPUT_DIR/openapi.yaml"
        log_success "Copied OpenAPI spec to $OUTPUT_DIR/openapi.yaml"
    fi
}

# Generate domain model documentation
generate_model_docs() {
    log_info "Generating model documentation..."

    # Find model files
    model_files=$(find . -path "*/domain/*" -name "*.go" 2>/dev/null | grep -v vendor | grep -v _test.go || true)

    if [ -z "$model_files" ]; then
        return
    fi

    cat >> "$OUTPUT_DIR/README.md" <<EOF

## Data Models

EOF

    for file in $model_files; do
        domain=$(dirname "$file" | xargs basename)

        # Extract struct definitions with comments
        structs=$(grep -E "^type\s+[A-Z][a-zA-Z]*\s+struct" "$file" 2>/dev/null | awk '{print $2}' || true)

        if [ -n "$structs" ]; then
            echo "### $domain" >> "$OUTPUT_DIR/README.md"
            echo "" >> "$OUTPUT_DIR/README.md"
            for struct in $structs; do
                echo "- \`$struct\`" >> "$OUTPUT_DIR/README.md"
            done
            echo "" >> "$OUTPUT_DIR/README.md"
        fi
    done
}

# Main execution
generate_header
extract_go_endpoints
generate_openapi_docs
generate_model_docs

# Add footer
cat >> "$OUTPUT_DIR/README.md" <<EOF

---

*Documentation generated by Ovasabi Foundation docgen*
EOF

log_success "Documentation generated at $OUTPUT_DIR"
