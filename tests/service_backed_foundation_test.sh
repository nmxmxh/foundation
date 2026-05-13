#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.service-backed.yml"
PROJECT_NAME="foundation-service-backed-${RANDOM:-0}"
GO_CACHE_DIR="${GO_CACHE_DIR:-${TMPDIR:-/tmp}/ovasabi-foundation-go-cache}"

SERVICE_BACKED_DB_USER="${SERVICE_BACKED_DB_USER:-postgres}"
SERVICE_BACKED_DB_PASSWORD="${SERVICE_BACKED_DB_PASSWORD:-postgres}"
SERVICE_BACKED_DB_NAME="${SERVICE_BACKED_DB_NAME:-foundation_service_test}"
SERVICE_BACKED_POSTGRES_PORT="${SERVICE_BACKED_POSTGRES_PORT:-55432}"
SERVICE_BACKED_REDIS_PORT="${SERVICE_BACKED_REDIS_PORT:-56379}"

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

export SERVICE_BACKED_DATABASE_URL="postgres://${SERVICE_BACKED_DB_USER}:${SERVICE_BACKED_DB_PASSWORD}@localhost:${SERVICE_BACKED_POSTGRES_PORT}/${SERVICE_BACKED_DB_NAME}?sslmode=disable"
export SERVICE_BACKED_REDIS_URL="redis://localhost:${SERVICE_BACKED_REDIS_PORT}/0"
export SERVICE_BACKED_REDIS_PREFIX="svc-${PROJECT_NAME}"

echo "== service-backed foundation: race tests =="
mkdir -p "$GO_CACHE_DIR"
(
    cd "$FOUNDATION_DIR/server-kit/go"
    GOCACHE="$GO_CACHE_DIR" go test -tags=servicebacked -race -count=1 -timeout 5m ./servicebacked
)

echo "== service-backed foundation: live benchmarks =="
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
)

echo "service-backed foundation test passed"
