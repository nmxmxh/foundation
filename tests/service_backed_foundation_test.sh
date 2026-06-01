#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.service-backed.yml"
PROJECT_NAME="foundation-service-backed-${RANDOM:-0}"
GO_CACHE_DIR="${GO_CACHE_DIR:-${TMPDIR:-/tmp}/ovasabi-foundation-go-cache}"
BENCHMARK_RESULTS_DIR="${BENCHMARK_RESULTS_DIR:-$FOUNDATION_DIR/benchmark-results}"
BENCHMARK_TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BENCHMARK_LOG="$BENCHMARK_RESULTS_DIR/service_backed_${BENCHMARK_TIMESTAMP}.log"
BENCHMARK_SUMMARY="$BENCHMARK_RESULTS_DIR/service_backed_${BENCHMARK_TIMESTAMP}.tsv"

SERVICE_BACKED_DB_USER="${SERVICE_BACKED_DB_USER:-postgres}"
SERVICE_BACKED_DB_PASSWORD="${SERVICE_BACKED_DB_PASSWORD:-postgres}"
SERVICE_BACKED_DB_NAME="${SERVICE_BACKED_DB_NAME:-foundation_service_test}"
SERVICE_BACKED_POSTGRES_PORT="${SERVICE_BACKED_POSTGRES_PORT:-0}"
SERVICE_BACKED_REDIS_PORT="${SERVICE_BACKED_REDIS_PORT:-0}"

export SERVICE_BACKED_DB_USER
export SERVICE_BACKED_DB_PASSWORD
export SERVICE_BACKED_DB_NAME
export SERVICE_BACKED_POSTGRES_PORT
export SERVICE_BACKED_REDIS_PORT

cleanup() {
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== service-backed foundation: starting postgres/redis =="
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
export SERVICE_BACKED_REDIS_PREFIX="svc-${PROJECT_NAME}"

echo "== service-backed foundation: race tests =="
mkdir -p "$GO_CACHE_DIR"
mkdir -p "$BENCHMARK_RESULTS_DIR"
(
    cd "$FOUNDATION_DIR/server-kit/go"
    GOCACHE="$GO_CACHE_DIR" go test -tags=servicebacked -race -count=1 -timeout 5m ./servicebacked
)

echo "== service-backed foundation: live benchmarks =="
echo "benchmark log: ${BENCHMARK_LOG#$FOUNDATION_DIR/}"
echo "benchmark summary: ${BENCHMARK_SUMMARY#$FOUNDATION_DIR/}"
set +e
(
    cd "$FOUNDATION_DIR/server-kit/go"
    GOCACHE="$GO_CACHE_DIR" go test \
        -tags=servicebacked \
        -run '^$' \
        -bench=BenchmarkServiceBacked \
        -benchmem \
        -benchtime "${SERVICE_BACKED_BENCHTIME:-1s}" \
        -count "${SERVICE_BACKED_BENCH_COUNT:-1}" \
        -timeout 10m \
        ./servicebacked
) 2>&1 | tee "$BENCHMARK_LOG"
bench_status=${PIPESTATUS[0]}
set -e

{
    echo "# benchmark	ns_per_op	bytes_per_op	allocs_per_op	unit_per_op	source"
    awk '
      /^Benchmark/ {
        bench=$1
        ns=""
        bytes=""
        allocs=""
        unit=""
        for (i=2; i<=NF; i++) {
          if ($(i+1) == "ns/op") ns=$i
          if ($(i+1) == "B/op") bytes=$i
          if ($(i+1) == "allocs/op") allocs=$i
          if ($(i+1) ~ /(keys|rows|records|events)\/op/) unit=$i " " $(i+1)
        }
        if (ns != "") {
          printf "%s\t%s\t%s\t%s\t%s\tservice-backed\n", bench, ns, bytes, allocs, unit
        }
      }
    ' "$BENCHMARK_LOG"
} >"$BENCHMARK_SUMMARY"

if [[ "$bench_status" -ne 0 ]]; then
    echo "service-backed benchmark failed; log retained at ${BENCHMARK_LOG#$FOUNDATION_DIR/}" >&2
    exit "$bench_status"
fi

echo "service-backed foundation test passed"
