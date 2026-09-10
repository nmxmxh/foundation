#!/usr/bin/env bash
# Measures what change-notification costs the record store.
#
# Every Foundation project writes its domain state to one table,
# `governance_state_records`, which makes it the busiest table in the
# deployment and the worst possible place to add per-write work. The obvious
# way to tell other processes that a scope moved — a trigger calling
# pg_notify — puts that work squarely on the commit path, and Postgres
# serializes the commit of any transaction that notified (the fix landed for
# PG19; nothing before it has it).
#
# This measures the size of that on your own hardware, because the number is
# what makes the argument. It runs the real upsert the record store performs,
# under rising concurrency, in three configurations:
#
#   A  no trigger                — the ceiling
#   B  pg_notify per statement   — the "batched" version people reach for
#   C  pg_notify per row         — the naive version
#
# It counts the notifications a listener actually received, so a trigger that
# silently failed to install cannot be mistaken for a cheap one. That is not
# hypothetical: it is how the first run of this benchmark reported that
# notification was free.
#
# Usage: record_store_notify_bench.sh <container> <db> [duration_seconds]
set -euo pipefail

CONTAINER="${1:?usage: record_store_notify_bench.sh <container> <db> [seconds]}"
DB="${2:?usage: record_store_notify_bench.sh <container> <db> [seconds]}"
DUR="${3:-9}"
PSQL=(docker exec -i -e PGOPTIONS=--client-min-messages=warning "$CONTAINER" \
      psql -U postgres -d "$DB" -q -v ON_ERROR_STOP=1)

echo "== record store notification cost: $DB in $CONTAINER, ${DUR}s per run =="
docker exec "$CONTAINER" pgbench --version >/dev/null || { echo "pgbench not in $CONTAINER"; exit 1; }

docker exec -i "$CONTAINER" bash -c 'cat > /tmp/rs_upsert.sql' <<'SQL'
\set rec random(1, 200000)
INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
VALUES ('communication','presence','org-1','rec-' || :rec,
        jsonb_build_object('room_id', 'r' || (:rec % 100), 'seq', :rec, 'heartbeat_at', now()))
ON CONFLICT (domain, collection_name, organization_id, record_id)
DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
WHERE governance_state_records.data IS DISTINCT FROM EXCLUDED.data;
SQL

# Each run starts from the same table. Without this the earlier runs' dead
# tuples accumulate and every configuration after the first is measured against
# a more bloated table than the one before it — which reads as "notification is
# cheap and the baseline is slow".
reset_table() {
  "${PSQL[@]}" >/dev/null <<'SQL'
TRUNCATE governance_state_records;
INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
SELECT 'communication','presence','org-1','rec-' || i,
       jsonb_build_object('room_id','r' || (i % 100), 'seq', i)
FROM generate_series(1,200000) AS i;
VACUUM (ANALYZE) governance_state_records;
SQL
}

# A listener has to exist for the queue work to be real. Started once and left
# for the whole run; it is dropped when the script exits.
start_listener() {
  docker exec -d "$CONTAINER" psql -U postgres -d "$DB" \
    -c "LISTEN rs_bench;" -c "SELECT pg_sleep(3600);" >/dev/null 2>&1
  sleep 1
}

stop_listener() {
  docker exec "$CONTAINER" psql -U postgres -d "$DB" -tAc \
    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity
      WHERE datname = '$DB' AND query LIKE '%pg_sleep(3600)%';" >/dev/null 2>&1 || true
}

reset_triggers() {
  "${PSQL[@]}" >/dev/null <<'SQL'
DROP TRIGGER IF EXISTS rs_bench_row ON governance_state_records;
DROP TRIGGER IF EXISTS rs_bench_stmt_ins ON governance_state_records;
DROP TRIGGER IF EXISTS rs_bench_stmt_upd ON governance_state_records;
SQL
}

install_row_trigger() {
  "${PSQL[@]}" >/dev/null <<'SQL'
CREATE OR REPLACE FUNCTION rs_bench_row() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('rs_bench', NEW.domain || '/' || NEW.collection_name);
    RETURN NULL;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER rs_bench_row AFTER INSERT OR UPDATE ON governance_state_records
    FOR EACH ROW EXECUTE FUNCTION rs_bench_row();
SQL
}

install_stmt_trigger() {
  # Transition tables are not allowed on a multi-event trigger, so one each.
  "${PSQL[@]}" >/dev/null <<'SQL'
CREATE OR REPLACE FUNCTION rs_bench_stmt() RETURNS TRIGGER AS $$
DECLARE scope TEXT;
BEGIN
    FOR scope IN SELECT DISTINCT domain || '/' || collection_name FROM changed_rows
    LOOP PERFORM pg_notify('rs_bench', scope); END LOOP;
    RETURN NULL;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER rs_bench_stmt_ins AFTER INSERT ON governance_state_records
    REFERENCING NEW TABLE AS changed_rows FOR EACH STATEMENT EXECUTE FUNCTION rs_bench_stmt();
CREATE TRIGGER rs_bench_stmt_upd AFTER UPDATE ON governance_state_records
    REFERENCING NEW TABLE AS changed_rows FOR EACH STATEMENT EXECUTE FUNCTION rs_bench_stmt();
SQL
}

run() {
  local label="$1" installer="$2" clients="$3"
  reset_table
  reset_triggers
  [ "$installer" != "none" ] && "$installer"
  local tps
  tps=$(docker exec "$CONTAINER" pgbench -U postgres -d "$DB" -f /tmp/rs_upsert.sql \
        -c "$clients" -j 4 -T "$DUR" -n 2>/dev/null | awk '/^tps/ {print $3; exit}')
  printf "%-28s c=%-3s tps=%.0f\n" "$label" "$clients" "$tps"
}

trap stop_listener EXIT
start_listener
echo "listener attached; resetting the table before every run"
echo

for c in 1 8 32 64; do run "A no trigger"              none                 "$c"; done
echo
for c in 1 8 32 64; do run "B pg_notify per statement" install_stmt_trigger "$c"; done
echo
for c in 1 8 32 64; do run "C pg_notify per row"       install_row_trigger  "$c"; done
reset_triggers
echo
echo "If B and C do not fall well below A, confirm the triggers actually"
echo "installed: an uninstalled trigger benchmarks as free."
