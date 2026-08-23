#!/usr/bin/env bash
# PostgreSQL read-pressure diagnostics for Foundation deployments.
#
# Collects the evidence set required by the database optimization handoff:
#   1. pg_stat_user_tables read-pressure leaders (seq_tup_read / idx_tup_fetch)
#   2. river_job state distribution and per-queue backlog
#   3. dead-tuple/bloat indicators for the leaders
#
# All connection details come from the environment. No host, credential, or
# container name is hardcoded. Run from an operator workstation with SSH
# access to the database host.
#
# Usage:
#   DB_SSH_TARGET=ops@db-host \
#   DB_CONTAINER=app-postgres-1 \
#   DB_NAME=pronto \
#   ./scripts/db_diagnostics.sh
#
# Optional: DB_SSH_PORT (default 22), DB_USER (default postgres),
# LIMIT (default 10). Requires ssh and a psql-capable container on the target.

set -euo pipefail

DB_SSH_TARGET="${DB_SSH_TARGET:?set DB_SSH_TARGET, e.g. ops@db-host}"
DB_CONTAINER="${DB_CONTAINER:?set DB_CONTAINER, the postgres docker container name}"
DB_NAME="${DB_NAME:?set DB_NAME, the database name}"
DB_USER="${DB_USER:-postgres}"
DB_SSH_PORT="${DB_SSH_PORT:-22}"
LIMIT="${LIMIT:-10}"

ssh_cmd() {
  ssh -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new -p "$DB_SSH_PORT" "$DB_SSH_TARGET" "$@"
}

psql_query() {
  local sql="$1"
  ssh_cmd "docker exec ${DB_CONTAINER} psql -U ${DB_USER} -d ${DB_NAME} -c \"${sql}\""
}

echo "== pg_stat_user_tables: top ${LIMIT} by sequential-read volume =="
psql_query "SELECT relname, seq_scan, seq_tup_read, idx_scan, idx_tup_fetch, n_live_tup, n_dead_tup FROM pg_stat_user_tables ORDER BY seq_tup_read DESC LIMIT ${LIMIT};"

echo
echo "== river_job: state distribution =="
psql_query "SELECT state, count(*) AS jobs FROM river_job GROUP BY state ORDER BY jobs DESC;"

echo
echo "== river_job: available backlog per queue =="
psql_query "SELECT queue, count(*) AS available FROM river_job WHERE state = 'available' GROUP BY queue ORDER BY available DESC LIMIT ${LIMIT};"

echo
echo "== river_job: oldest available job age =="
psql_query "SELECT coalesce(max(now() - created_at), interval '0') AS oldest_available FROM river_job WHERE state = 'available';"

echo
echo "NOTE: run VACUUM ANALYZE or check autovacuum output if n_dead_tup is high;"
echo "River's maintainer (job cleaner + reindexer) requires an elected leader to prune."
