#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
if [[ -d "$ROOT_DIR/server-kit/go" ]]; then
    SERVER_KIT_GO="$ROOT_DIR/server-kit/go"
elif [[ -d "$ROOT_DIR/foundation/server-kit/go" ]]; then
    SERVER_KIT_GO="$ROOT_DIR/foundation/server-kit/go"
else
    echo "server-kit/go module not found under $ROOT_DIR or $ROOT_DIR/foundation" >&2
    exit 1
fi

RESULTS_DIR="${LOAD_RESEARCH_RESULTS_DIR:-$ROOT_DIR/benchmark-results}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOG_FILE="$RESULTS_DIR/load_research_${TIMESTAMP}.log"
SUMMARY_FILE="$RESULTS_DIR/load_research_${TIMESTAMP}.tsv"
GO_CACHE_DIR="${GO_CACHE_DIR:-${TMPDIR:-/tmp}/ovasabi-load-research-go-cache}"

export RUN_LOAD_RESEARCH=1
export LOAD_RESEARCH_STEPS="${LOAD_RESEARCH_STEPS:-1000,10000,50000,100000,250000,500000,1000000}"
export LOAD_RESEARCH_SAMPLES="${LOAD_RESEARCH_SAMPLES:-9}"
export LOAD_RESEARCH_MAX_STEP="${LOAD_RESEARCH_MAX_STEP:-1000000}"
export LOAD_RESEARCH_OUTPUT="$SUMMARY_FILE"

mkdir -p "$RESULTS_DIR"
mkdir -p "$GO_CACHE_DIR"

echo "== foundation load research =="
echo "steps: $LOAD_RESEARCH_STEPS"
echo "max step: $LOAD_RESEARCH_MAX_STEP"
echo "samples: $LOAD_RESEARCH_SAMPLES"
echo "server-kit: ${SERVER_KIT_GO#$ROOT_DIR/}"
echo "log: ${LOG_FILE#$ROOT_DIR/}"
echo "summary: ${SUMMARY_FILE#$ROOT_DIR/}"

(
    cd "$SERVER_KIT_GO"
    GOCACHE="$GO_CACHE_DIR" go test \
        -run '^TestLoadResearchRamps$' \
        -count=1 \
        -timeout "${LOAD_RESEARCH_TIMEOUT:-45m}" \
        -v \
        ./appbench
) 2>&1 | tee "$LOG_FILE"

echo "load research complete"
