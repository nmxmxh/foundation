#!/bin/bash
# Update all scaffolded projects listed in scaffolded-projects.tsv

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PARENT_DIR="$(dirname "$FOUNDATION_DIR")"
TSV_FILE="$PARENT_DIR/scaffolded-projects.tsv"

if [[ ! -f "$TSV_FILE" ]]; then
    echo "Error: TSV file not found at $TSV_FILE" >&2
    exit 1
fi

while IFS=$'\t' read -r slug path || [[ -n "$slug" ]]; do
    # Skip comments and empty lines
    if [[ "$slug" =~ ^# || -z "$slug" ]]; then
        continue
    fi
    project_path="$PARENT_DIR/$path"
    echo "========================================="
    echo "Updating project: $slug ($project_path)"
    echo "========================================="
    "$SCRIPT_DIR/update-project.sh" "$project_path"
done < "$TSV_FILE"

echo "All projects updated successfully!"
