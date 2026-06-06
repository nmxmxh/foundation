#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.service-backed.yml"
PROJECT_NAME="${SERVICE_BACKED_LOAD_PROJECT_NAME:-foundation-service-load-${RANDOM:-0}}"
GO_CACHE_DIR="${GO_CACHE_DIR:-${TMPDIR:-/tmp}/ovasabi-foundation-load-go-cache}"
RESULTS_DIR="${SERVICE_BACKED_LOAD_RESEARCH_RESULTS_DIR:-$FOUNDATION_DIR/benchmark-results}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOG_FILE="$RESULTS_DIR/service_backed_load_research_${TIMESTAMP}.log"
SUMMARY_FILE="$RESULTS_DIR/service_backed_load_research_${TIMESTAMP}.tsv"
POSTGRES_LOG_FILE="$RESULTS_DIR/service_backed_load_research_${TIMESTAMP}.postgres.log"
REDIS_LOG_FILE="$RESULTS_DIR/service_backed_load_research_${TIMESTAMP}.redis.log"

SERVICE_BACKED_DB_USER="${SERVICE_BACKED_DB_USER:-postgres}"
SERVICE_BACKED_DB_PASSWORD="${SERVICE_BACKED_DB_PASSWORD:-postgres}"
SERVICE_BACKED_DB_NAME="${SERVICE_BACKED_DB_NAME:-foundation_service_load}"
SERVICE_BACKED_POSTGRES_PORT="${SERVICE_BACKED_POSTGRES_PORT:-0}"
SERVICE_BACKED_REDIS_PORT="${SERVICE_BACKED_REDIS_PORT:-0}"
SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS="${SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS:-120}"
SERVICE_BACKED_REDIS_MAXMEMORY="${SERVICE_BACKED_REDIS_MAXMEMORY:-1gb}"

service_backed_load_max_step() {
    local raw_steps="${SERVICE_BACKED_LOAD_RESEARCH_STEPS:-1000,10000,50000,100000,250000,500000,1000000}"
    local max_step_cap="${SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP:-1000000}"
    local max_step=0
    local step
    IFS=',' read -r -a step_values <<<"$raw_steps"
    for step in "${step_values[@]}"; do
        step="${step//[[:space:]]/}"
        [[ "$step" =~ ^[0-9]+$ ]] || continue
        if (( step > max_step_cap )); then
            continue
        fi
        if (( step > max_step )); then
            max_step="$step"
        fi
    done
    if (( max_step <= 0 )); then
        max_step="$max_step_cap"
    fi
    echo "$max_step"
}

service_backed_load_default_postgres_tmpfs() {
    local max_step="$1"
    if (( max_step >= 1000000 )); then
        echo "8g"
    elif (( max_step >= 500000 )); then
        echo "6g"
    elif (( max_step >= 250000 )); then
        echo "4g"
    else
        echo "2g"
    fi
}

service_backed_load_default_postgres_max_wal_size() {
    local max_step="$1"
    if (( max_step >= 1000000 )); then
        echo "4GB"
    elif (( max_step >= 500000 )); then
        echo "3GB"
    elif (( max_step >= 250000 )); then
        echo "2GB"
    else
        echo "1GB"
    fi
}

service_backed_load_default_postgres_min_wal_size() {
    local max_step="$1"
    if (( max_step >= 1000000 )); then
        echo "1GB"
    elif (( max_step >= 500000 )); then
        echo "512MB"
    else
        echo "256MB"
    fi
}

SERVICE_BACKED_LOAD_RESEARCH_MAX_EFFECTIVE_STEP="$(service_backed_load_max_step)"
SERVICE_BACKED_POSTGRES_TMPFS_SIZE="${SERVICE_BACKED_POSTGRES_TMPFS_SIZE:-$(service_backed_load_default_postgres_tmpfs "$SERVICE_BACKED_LOAD_RESEARCH_MAX_EFFECTIVE_STEP")}"
SERVICE_BACKED_POSTGRES_MAX_WAL_SIZE="${SERVICE_BACKED_POSTGRES_MAX_WAL_SIZE:-$(service_backed_load_default_postgres_max_wal_size "$SERVICE_BACKED_LOAD_RESEARCH_MAX_EFFECTIVE_STEP")}"
SERVICE_BACKED_POSTGRES_MIN_WAL_SIZE="${SERVICE_BACKED_POSTGRES_MIN_WAL_SIZE:-$(service_backed_load_default_postgres_min_wal_size "$SERVICE_BACKED_LOAD_RESEARCH_MAX_EFFECTIVE_STEP")}"
SERVICE_BACKED_POSTGRES_CHECKPOINT_TIMEOUT="${SERVICE_BACKED_POSTGRES_CHECKPOINT_TIMEOUT:-15min}"
SERVICE_BACKED_POSTGRES_AUTOVACUUM_MAX_WORKERS="${SERVICE_BACKED_POSTGRES_AUTOVACUUM_MAX_WORKERS:-5}"
SERVICE_BACKED_POSTGRES_AUTOVACUUM_WORK_MEM="${SERVICE_BACKED_POSTGRES_AUTOVACUUM_WORK_MEM:-128MB}"
SERVICE_BACKED_POSTGRES_AUTOVACUUM_NAPTIME="${SERVICE_BACKED_POSTGRES_AUTOVACUUM_NAPTIME:-10s}"
SERVICE_BACKED_POSTGRES_AUTOVACUUM_INSERT_SCALE_FACTOR="${SERVICE_BACKED_POSTGRES_AUTOVACUUM_INSERT_SCALE_FACTOR:-0.05}"

export SERVICE_BACKED_DB_USER
export SERVICE_BACKED_DB_PASSWORD
export SERVICE_BACKED_DB_NAME
export SERVICE_BACKED_POSTGRES_PORT
export SERVICE_BACKED_REDIS_PORT
export SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS
export SERVICE_BACKED_POSTGRES_TMPFS_SIZE
export SERVICE_BACKED_POSTGRES_MAX_WAL_SIZE
export SERVICE_BACKED_POSTGRES_MIN_WAL_SIZE
export SERVICE_BACKED_POSTGRES_CHECKPOINT_TIMEOUT
export SERVICE_BACKED_POSTGRES_AUTOVACUUM_MAX_WORKERS
export SERVICE_BACKED_POSTGRES_AUTOVACUUM_WORK_MEM
export SERVICE_BACKED_POSTGRES_AUTOVACUUM_NAPTIME
export SERVICE_BACKED_POSTGRES_AUTOVACUUM_INSERT_SCALE_FACTOR
export SERVICE_BACKED_REDIS_MAXMEMORY
export SERVICE_BACKED_LOAD_RESEARCH_STEPS="${SERVICE_BACKED_LOAD_RESEARCH_STEPS:-1000,10000,50000,100000,250000,500000,1000000}"
export SERVICE_BACKED_LOAD_RESEARCH_SAMPLES="${SERVICE_BACKED_LOAD_RESEARCH_SAMPLES:-5}"
export SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP="${SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP:-1000000}"
export SERVICE_BACKED_LOAD_RESEARCH_TIMEOUT="${SERVICE_BACKED_LOAD_RESEARCH_TIMEOUT:-90m}"
export SERVICE_BACKED_LOAD_RESEARCH_DB_ACQUIRE_TIMEOUT="${SERVICE_BACKED_LOAD_RESEARCH_DB_ACQUIRE_TIMEOUT:-2s}"
export SERVICE_BACKED_LOAD_RESEARCH_DB_QUERY_TIMEOUT="${SERVICE_BACKED_LOAD_RESEARCH_DB_QUERY_TIMEOUT:-10s}"
export SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_BATCH="${SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_BATCH:-512}"
export SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_TAILER_BATCH="${SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_TAILER_BATCH:-256}"
export SERVICE_BACKED_LOAD_RESEARCH_OUTPUT="$SUMMARY_FILE"
export RUN_SERVICE_BACKED_LOAD_RESEARCH=1

source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

capture_service_logs() {
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs service-postgres >"$POSTGRES_LOG_FILE" 2>/dev/null || true
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs service-redis >"$REDIS_LOG_FILE" 2>/dev/null || true
}

test_step "service-backed load research: starting postgres/redis"
docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" up -d service-postgres service-redis

postgres_ready=0
for _ in $(seq 1 90); do
    if docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" exec -T service-postgres \
        pg_isready -U "$SERVICE_BACKED_DB_USER" -d "$SERVICE_BACKED_DB_NAME" >/dev/null 2>&1; then
        postgres_ready=1
        break
    fi
    sleep 1
done
if [[ "$postgres_ready" -ne 1 ]]; then
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs service-postgres
    echo "postgres service did not become ready" >&2
    exit 1
fi

redis_ready=0
for _ in $(seq 1 90); do
    if docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" exec -T service-redis \
        redis-cli ping >/dev/null 2>&1; then
        redis_ready=1
        break
    fi
    sleep 1
done
if [[ "$redis_ready" -ne 1 ]]; then
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs service-redis
    echo "redis service did not become ready" >&2
    exit 1
fi

SERVICE_BACKED_POSTGRES_HOST_PORT="$(docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" port service-postgres 5432)"
SERVICE_BACKED_REDIS_HOST_PORT="$(docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" port service-redis 6379)"
SERVICE_BACKED_POSTGRES_HOST_PORT="${SERVICE_BACKED_POSTGRES_HOST_PORT##*:}"
SERVICE_BACKED_REDIS_HOST_PORT="${SERVICE_BACKED_REDIS_HOST_PORT##*:}"
if [[ -z "$SERVICE_BACKED_POSTGRES_HOST_PORT" || -z "$SERVICE_BACKED_REDIS_HOST_PORT" ]]; then
    echo "failed to resolve service-backed host ports" >&2
    exit 1
fi

export SERVICE_BACKED_DATABASE_URL="postgres://${SERVICE_BACKED_DB_USER}:${SERVICE_BACKED_DB_PASSWORD}@localhost:${SERVICE_BACKED_POSTGRES_HOST_PORT}/${SERVICE_BACKED_DB_NAME}?sslmode=disable"
export SERVICE_BACKED_REDIS_URL="redis://localhost:${SERVICE_BACKED_REDIS_HOST_PORT}/0"
export SERVICE_BACKED_REDIS_PREFIX="svc-load-${PROJECT_NAME}"

mkdir -p "$GO_CACHE_DIR"
mkdir -p "$RESULTS_DIR"

test_step "service-backed load research: staged run"
echo "load log: ${LOG_FILE#$FOUNDATION_DIR/}"
echo "load summary: ${SUMMARY_FILE#$FOUNDATION_DIR/}"
echo "postgres log: ${POSTGRES_LOG_FILE#$FOUNDATION_DIR/}"
echo "redis log: ${REDIS_LOG_FILE#$FOUNDATION_DIR/}"
echo "steps: $SERVICE_BACKED_LOAD_RESEARCH_STEPS"
echo "lanes: ${SERVICE_BACKED_LOAD_RESEARCH_LANES:-default}"
echo "max step: $SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP"
echo "max workers: ${SERVICE_BACKED_LOAD_RESEARCH_MAX_WORKERS:-auto}"
echo "ws register batch: ${SERVICE_BACKED_LOAD_RESEARCH_WS_REGISTER_BATCH:-256}"
echo "postgres max connections: $SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS"
echo "postgres tmpfs: $SERVICE_BACKED_POSTGRES_TMPFS_SIZE"
echo "postgres max wal size: $SERVICE_BACKED_POSTGRES_MAX_WAL_SIZE"
echo "postgres min wal size: $SERVICE_BACKED_POSTGRES_MIN_WAL_SIZE"
echo "postgres checkpoint timeout: $SERVICE_BACKED_POSTGRES_CHECKPOINT_TIMEOUT"
echo "postgres autovacuum workers: $SERVICE_BACKED_POSTGRES_AUTOVACUUM_MAX_WORKERS"
echo "postgres autovacuum work mem: $SERVICE_BACKED_POSTGRES_AUTOVACUUM_WORK_MEM"
echo "redis maxmemory: $SERVICE_BACKED_REDIS_MAXMEMORY"
echo "db acquire timeout: $SERVICE_BACKED_LOAD_RESEARCH_DB_ACQUIRE_TIMEOUT"
echo "db query timeout: $SERVICE_BACKED_LOAD_RESEARCH_DB_QUERY_TIMEOUT"
echo "pipeline batch: $SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_BATCH"
echo "pipeline tailer batch: $SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_TAILER_BATCH"

set +e
(
    cd "$FOUNDATION_DIR/server-kit/go"
    GOCACHE="$GO_CACHE_DIR" go test \
        -tags=servicebacked \
        -run '^TestServiceBackedLoadResearchRamps$' \
        -count=1 \
        -timeout "${SERVICE_BACKED_LOAD_RESEARCH_GO_TIMEOUT:-2h}" \
        ./servicebacked
) 2>&1 | tee "$LOG_FILE"
status=${PIPESTATUS[0]}
set -e

if [[ "$status" -ne 0 ]]; then
    capture_service_logs
    echo "service-backed load research failed; log retained at ${LOG_FILE#$FOUNDATION_DIR/}" >&2
    exit "$status"
fi

capture_service_logs
test_step "service-backed load research complete"
