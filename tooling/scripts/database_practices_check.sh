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

  if ! rg -in "CREATE TABLE" "$first_up_path" >/dev/null 2>&1; then
    echo "[FAIL] first migration must establish schema tables"
    failed=1
  else
    echo "[OK] first migration creates schema objects"
  fi

  if rg -in "(organization_id|org_id|workspace_id)" "$first_up_path" >/dev/null 2>&1; then
    if ! rg -in "CREATE( UNIQUE)? INDEX .*?(organization_id|org_id|workspace_id)|PRIMARY KEY .*?(organization_id|org_id|workspace_id)|UNIQUE .*?(organization_id|org_id|workspace_id)" "$first_up_path" >/dev/null 2>&1; then
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

if rg -n "(SELECT|RETURNING) \\*" "$target" --glob '*.go' --glob '*.sql' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] wildcard SELECT */RETURNING * found in Go/SQL hot-path sources; project explicit columns"
  failed=1
else
  echo "[OK] no wildcard SELECT */RETURNING * in Go/SQL hot-path sources"
fi

if rg -n "\b(crypt|gen_salt|pgp_sym_encrypt|pgp_pub_encrypt)\s*\(" "$target" --glob '*.go' --glob '*.sql' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] Database-side cryptographic operations (crypt/gen_salt/pgp_sym_encrypt/pgp_pub_encrypt) found in SQL or Go sources. Perform credential hashing, encryption, and verification in the application layer (Go/Rust/TS) instead of SQL queries."
  failed=1
else
  echo "[OK] no database-side cryptographic operations (crypt/gen_salt/pgp_sym_encrypt/pgp_pub_encrypt) in Go/SQL sources"
fi

if [[ -d "$target/internal/service/persistence" ]] || rg -n "internal/service/persistence" "$target" --glob '*.go' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] persistence helper package found under internal/service; shared database helpers must come from server-kit/go/database"
  failed=1
else
  echo "[OK] no app-local persistence helper package under internal/service"
fi

if rg -n "(pgx|postgres|QueryRow\\(|Query\\(|Exec\\(|Begin\\()" "$target/internal/service" --glob 'repository.go' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  if ! rg -n "server-kit/go/database|database\\.(QueryOne|QuerySQLOne|QueryEach|QuerySQLEach|QueryAll|QuerySQLAll|ExecCommand|ExecRowsAffected|ExecSQLRowsAffected|AtomicLane|SendBatch|CopyFrom)" "$target/internal/service" --glob 'repository.go' --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] repository SQL usage detected without Foundation database helper usage"
    failed=1
  else
    echo "[OK] repository SQL usage is anchored to Foundation database helpers"
  fi
fi

if rg -n "\\.(Exec|Begin)\\(" "$target/internal/service" --glob 'repository.go' --glob 'persist.go' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] raw repository command/transaction calls found; use ExecCommand, ExecRowsAffected, or AtomicLane"
  failed=1
else
  echo "[OK] repository command/transaction paths use Foundation executors"
fi

if rg -n "database\\.(QuerySQL|ExecSQL)" "$target/internal/service" --glob '*.go' --glob '!**/node_modules/**' >/dev/null 2>&1; then
  echo "[FAIL] transitional SQL compatibility helpers found in service repositories; use executor helpers"
  failed=1
else
  echo "[OK] no transitional SQL compatibility helpers in service repositories"
fi


# Ambiguous SQL parameter types: a bare $N inside arithmetic gives Postgres no
# type anchor, and pgx sends no parameter OIDs — deduction then conflicts with
# the parameter's other uses (SQLSTATE 42P08, e.g. "integer versus text") or
# silently lands on text. Every parameter in an arithmetic expression must
# carry an explicit cast ($N::bigint). This caught a production checkout
# outage; keep it strict.
if rg -nP '\$[0-9]+\s*[+\-*/]|[+\-*/]\s*\$[0-9]+(?!::)' "$target/internal" --glob '*.go' --glob '!*_test.go' --glob '!**/node_modules/**' 2>/dev/null | rg -v '\$[0-9]+::' ; then
  echo "[FAIL] SQL parameters used in arithmetic without an explicit ::cast (ambiguous type deduction, 42P08)"
  failed=1
else
  echo "[OK] SQL parameters in arithmetic carry explicit casts"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "database practices check failed"
  exit 1
fi

echo "database practices check passed"
