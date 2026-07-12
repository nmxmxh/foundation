#!/bin/bash
# Update every scaffolded project and always emit a bounded fleet summary.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PARENT_DIR="$(dirname "$FOUNDATION_DIR")"
TSV_FILE="$PARENT_DIR/scaffolded-projects.tsv"
REPORT_DIR=""
FORWARD_ARGS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --report-dir)
            [[ -n "${2:-}" ]] || { echo "--report-dir requires a path" >&2; exit 2; }
            REPORT_DIR="$2"
            shift 2
            ;;
        *) FORWARD_ARGS+=("$1"); shift ;;
    esac
done

[[ -f "$TSV_FILE" ]] || { echo "Error: TSV file not found at $TSV_FILE" >&2; exit 1; }
if [[ -z "$REPORT_DIR" ]]; then
    REPORT_DIR="$FOUNDATION_DIR/test-results/fleet-update-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$REPORT_DIR"

fleet_tsv="$REPORT_DIR/fleet.tsv"
printf 'slug\tpath\tstatus\texit_code\n' >"$fleet_tsv"
failures=0
total=0

while IFS=$'\t' read -r slug path || [[ -n "$slug" ]]; do
    [[ "$slug" =~ ^# || -z "$slug" ]] && continue
    total=$((total + 1))
    project_path="$PARENT_DIR/$path"
    echo "========================================="
    echo "Updating project: $slug ($project_path)"
    echo "========================================="
    if "$SCRIPT_DIR/update-project.sh" "$project_path" "${FORWARD_ARGS[@]}" --report-dir "$REPORT_DIR"; then
        status="success"
        code=0
    else
        code=$?
        status="failed"
        failures=$((failures + 1))
        echo "Project update failed: $slug (exit $code); continuing fleet update" >&2
    fi
    printf '%s\t%s\t%s\t%s\n' "$slug" "$project_path" "$status" "$code" >>"$fleet_tsv"
done <"$TSV_FILE"

successes=$((total - failures))
{
    printf '{"total":%d,"succeeded":%d,"failed":%d,"projects":[' "$total" "$successes" "$failures"
    first=true
    while IFS=$'\t' read -r slug path status code; do
        [[ "$slug" == "slug" ]] && continue
        [[ "$first" == "true" ]] || printf ','
        first=false
        printf '{"slug":"%s","path":"%s","status":"%s","exit_code":%d}' "$slug" "$path" "$status" "$code"
    done <"$fleet_tsv"
    printf ']}\n'
} >"$REPORT_DIR/fleet.json"

{
    echo '# Foundation fleet update'
    echo
    echo "- Total: $total"
    echo "- Succeeded: $successes"
    echo "- Failed: $failures"
    echo
    echo '| Project | Status | Exit |'
    echo '| --- | --- | ---: |'
    while IFS=$'\t' read -r slug _ status code; do
        [[ "$slug" == "slug" ]] && continue
        echo "| $slug | $status | $code |"
    done <"$fleet_tsv"
} >"$REPORT_DIR/fleet.md"

echo "Fleet update: $successes/$total succeeded; report: $REPORT_DIR/fleet.md"
[[ "$failures" -eq 0 ]]
