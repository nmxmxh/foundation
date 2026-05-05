#!/bin/zsh
set -euo pipefail

target="${1:-.}"
migrations_dir="$target/migrations"
failed=0

if [[ ! -d "$migrations_dir" ]]; then
  echo "[OK] no migrations directory present"
else
  first_up="$(cd "$migrations_dir" && ls -1 *.up.sql 2>/dev/null | sort | head -n 1)"

  if [[ -z "$first_up" ]]; then
    echo "[FAIL] no up migrations found"
    exit 1
  fi

  first_up_path="$migrations_dir/$first_up"

  if ! rg -n "CREATE TABLE" "$first_up_path" >/dev/null 2>&1; then
    echo "[FAIL] first migration must establish schema tables"
    failed=1
  else
    echo "[OK] first migration creates schema objects"
  fi

  if rg -n "(organization_id|org_id|workspace_id)" "$first_up_path" >/dev/null 2>&1; then
    if ! rg -n "CREATE( UNIQUE)? INDEX .*?(organization_id|org_id|workspace_id)|PRIMARY KEY .*?(organization_id|org_id|workspace_id)" "$first_up_path" >/dev/null 2>&1; then
      echo "[FAIL] tenant boundary columns found without supporting index guidance in first migration"
      failed=1
    else
      echo "[OK] tenant boundary columns are indexed"
    fi
  fi

  if rg -n "DROP SCHEMA public CASCADE|TRUNCATE .* CASCADE" "$migrations_dir" --glob '*.up.sql' >/dev/null 2>&1; then
    echo "[FAIL] destructive schema-wide operations found in up migrations"
    failed=1
  else
    echo "[OK] no schema-wide destructive operations in up migrations"
  fi

  if ! rg -n "(created_at|updated_at)" "$first_up_path" >/dev/null 2>&1; then
    echo "[FAIL] first migration should establish audit timestamps on base tables"
    failed=1
  else
    echo "[OK] audit timestamps present in base schema"
  fi
fi

if rg -n "database|postgres|pgx|DATABASE_URL|DB_MAX_CONNS" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
  if ! rg -n "(DB_MAX_CONNS|DB_QUERY_TIMEOUT|query_timeout_ms|QueryTimeout|DefaultPoolOptionsFor)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] database usage detected without explicit pool/query budgets"
    failed=1
  else
    echo "[OK] database pool/query budgets present"
  fi
  if ! rg -n "(statement_timeout|autovacuum_vacuum_scale_factor|idle_in_transaction_session_timeout)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] database config should include timeout/autovacuum guardrails"
    failed=1
  else
    echo "[OK] database timeout/autovacuum guardrails present"
  fi
  if ! rg -n "(POSTGRES_VERSION=18|postgres:\\$\\{POSTGRES_VERSION:-18\\}|PostgreSQL 18|io_method|pg_aios|pg_stat_io)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] database baseline should expose PostgreSQL 18 async I/O/observability guidance"
    failed=1
  else
    echo "[OK] PostgreSQL 18 async I/O/observability baseline present"
  fi
fi

if rg -n "SELECT \\*" "$target" --glob '*.go' --glob '*.sql' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] SELECT * found in Go/SQL hot-path sources; project explicit columns"
  failed=1
else
  echo "[OK] no SELECT * in Go/SQL hot-path sources"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "database practices check failed"
  exit 1
fi

echo "database practices check passed"
