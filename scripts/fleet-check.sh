#!/bin/bash
# Fleet-wide integration testing matrix orchestrator.
# Verifies scaffold integrity and compilation readiness across all scaffolded projects.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PARENT_DIR="$(dirname "$FOUNDATION_DIR")"
# The project list lives next to the foundation checkout by default. Override it
# with --projects or FLEET_PROJECTS_TSV, for example in CI.
TSV_FILE="${FLEET_PROJECTS_TSV:-$PARENT_DIR/scaffolded-projects.tsv}"
REPORT_DIR=""
MAX_CONCURRENCY=4

# Detect available CPU cores conservatively
if command -v sysctl >/dev/null 2>&1; then
    NUM_CPU="$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
elif command -v nproc >/dev/null 2>&1; then
    NUM_CPU="$(nproc 2>/dev/null || echo 4)"
else
    NUM_CPU=4
fi

if [[ "$NUM_CPU" -lt "$MAX_CONCURRENCY" && "$NUM_CPU" -gt 0 ]]; then
    MAX_CONCURRENCY="$NUM_CPU"
fi

while [[ $# -gt 0 ]]; do
    case "$1" in
        --report-dir)
            [[ -n "${2:-}" ]] || { echo "--report-dir requires a path" >&2; exit 2; }
            REPORT_DIR="$2"
            shift 2
            ;;
        --projects)
            [[ -n "${2:-}" ]] || { echo "--projects requires a path" >&2; exit 2; }
            TSV_FILE="$2"
            shift 2
            ;;
        --concurrency)
            [[ -n "${2:-}" ]] || { echo "--concurrency requires a number" >&2; exit 2; }
            MAX_CONCURRENCY="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
done

[[ -f "$TSV_FILE" ]] || {
    echo "Error: project list not found at $TSV_FILE" >&2
    echo "Pass --projects <file.tsv> or set FLEET_PROJECTS_TSV (format: slug<TAB>path relative to $PARENT_DIR)." >&2
    exit 1
}
[[ "$MAX_CONCURRENCY" =~ ^[1-9][0-9]*$ ]] || { echo "--concurrency must be a positive integer" >&2; exit 2; }

# json_escape quotes a value for the JSON report (backslash, quote, control characters).
json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\t'/\\t}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    printf '%s' "$value"
}
if [[ -z "$REPORT_DIR" ]]; then
    REPORT_DIR="$FOUNDATION_DIR/test-results/fleet-check-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$REPORT_DIR"

matrix_tsv="$REPORT_DIR/fleet_matrix.tsv"
printf 'slug\tpath\tscaffold_status\tcompile_status\telapsed_seconds\texit_code\n' >"$matrix_tsv"

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/fleet_check_XXXXXX")"
trap 'rm -rf "$WORK_DIR"' EXIT

check_single_project() {
    local slug="$1"
    local path="$2"
    local project_path="$PARENT_DIR/$path"
    local out_file="$WORK_DIR/${slug}.res"
    local log_file="$REPORT_DIR/${slug}.log"

    local start_time
    start_time="$(date +%s)"

    local scaffold_status="passed"
    local compile_status="passed"
    local exit_code=0

    # 1. Scaffold Contract Check
    if ! "$FOUNDATION_DIR/tooling/scripts/project_scaffold_check.sh" "$project_path" >"$log_file" 2>&1; then
        scaffold_status="failed"
        exit_code=1
    fi

    # 2. Fast Compilation Sanity Check (if Go backend exists)
    if [[ "$exit_code" -eq 0 && -f "$project_path/go.mod" ]] && command -v go >/dev/null 2>&1; then
        if ! (cd "$project_path" && go vet ./cmd/... ./internal/... >>"$log_file" 2>&1); then
            compile_status="failed"
            exit_code=2
        fi
    fi

    local end_time
    end_time="$(date +%s)"
    local elapsed=$((end_time - start_time))

    printf '%s\t%s\t%s\t%s\t%d\t%d\n' "$slug" "$project_path" "$scaffold_status" "$compile_status" "$elapsed" "$exit_code" >"$out_file"
}

# Collect active projects
projects=()
while IFS=$'\t' read -r slug path || [[ -n "$slug" ]]; do
    [[ "$slug" =~ ^# || -z "$slug" ]] && continue
    projects+=("$slug|$path")
done <"$TSV_FILE"

total="${#projects[@]}"
if [[ "$total" -eq 0 ]]; then
    # bash 3.2 (macOS /bin/bash) treats "${projects[@]}" on an empty array as unset under set -u.
    echo "Error: no projects listed in $TSV_FILE" >&2
    exit 1
fi
echo "========================================="
echo "Starting Fleet Matrix Check: $total projects (concurrency: $MAX_CONCURRENCY)"
echo "========================================="

running=0
for entry in "${projects[@]}"; do
    slug="${entry%%|*}"
    path="${entry#*|}"

    echo "[QUEUED] Project: $slug ($path)"
    check_single_project "$slug" "$path" &
    running=$((running + 1))

    if [[ "$running" -ge "$MAX_CONCURRENCY" ]]; then
        wait -n 2>/dev/null || wait
        running=$((running - 1))
    fi
done

wait

# Aggregate results in consistent order
failures=0
for entry in "${projects[@]}"; do
    slug="${entry%%|*}"
    res_file="$WORK_DIR/${slug}.res"
    if [[ -f "$res_file" ]]; then
        cat "$res_file" >>"$matrix_tsv"
        IFS=$'\t' read -r _ _ sc_st cp_st el_s code <"$res_file"
        if [[ "$code" -ne 0 ]]; then
            failures=$((failures + 1))
            echo "[FAIL] $slug (Scaffold: $sc_st, Compile: $cp_st, Code: $code, Time: ${el_s}s)"
        else
            echo "[PASS] $slug (Scaffold: $sc_st, Compile: $cp_st, Time: ${el_s}s)"
        fi
    else
        failures=$((failures + 1))
        printf '%s\tunknown\tfailed\tfailed\t0\t99\n' "$slug" >>"$matrix_tsv"
        echo "[FAIL] $slug (execution crashed or produced no result)"
    fi
done

successes=$((total - failures))

# Emit fleet_matrix.json
{
    printf '{"total":%d,"succeeded":%d,"failed":%d,"projects":[' "$total" "$successes" "$failures"
    first=true
    while IFS=$'\t' read -r slug path sc_st cp_st el_s code; do
        [[ "$slug" == "slug" ]] && continue
        [[ "$first" == "true" ]] || printf ','
        first=false
        printf '{"slug":"%s","path":"%s","scaffold_status":"%s","compile_status":"%s","elapsed_seconds":%d,"exit_code":%d}' \
            "$(json_escape "$slug")" "$(json_escape "$path")" "$(json_escape "$sc_st")" "$(json_escape "$cp_st")" "$el_s" "$code"
    done <"$matrix_tsv"
    printf ']}\n'
} >"$REPORT_DIR/fleet_matrix.json"

# Emit fleet_matrix.md
{
    echo '# Foundation Fleet Integration Matrix Report'
    echo
    echo "- Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "- Total Projects: $total"
    echo "- Succeeded: $successes"
    echo "- Failed: $failures"
    echo
    echo '| Project | Scaffold Check | Compile Sanity | Elapsed | Exit Code |'
    echo '| :--- | :--- | :--- | ---: | ---: |'
    while IFS=$'\t' read -r slug _ sc_st cp_st el_s code; do
        [[ "$slug" == "slug" ]] && continue
        echo "| \`$slug\` | $sc_st | $cp_st | ${el_s}s | $code |"
    done <"$matrix_tsv"
} >"$REPORT_DIR/fleet_matrix.md"

echo "========================================="
echo "Fleet Matrix Check Completed: $successes/$total passed; report: $REPORT_DIR/fleet_matrix.md"
echo "========================================="

[[ "$failures" -eq 0 ]]
