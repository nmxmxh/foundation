#!/usr/bin/env bash
# Regression guard for tooling/scripts/sql_contract_alignment_check.go.
# Each case builds a small project and asserts pass or fail.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/tooling/scripts/sql_contract_alignment_check.go"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

make_project() {
  local dir="$work/$1"
  mkdir -p "$dir/migrations" "$dir/internal/service/widget"
  cat >"$dir/migrations/000001_init.up.sql" <<'SQL'
CREATE TABLE widgets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL,
  name TEXT NOT NULL,
  label TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL
  printf '%s\n' "$dir"
}

write_repo() {
  cat >"$1/internal/service/widget/repository.go" <<GO
package widget

import "context"

type row interface{ Scan(dest ...any) error }
type db interface {
	QueryRow(ctx context.Context, sql string, args ...any) row
	Exec(ctx context.Context, sql string, args ...any) (any, error)
}

type Repo struct{ db db }

func (r *Repo) Run(ctx context.Context, orgID, name, label string) error {
	var w struct{ ID, Name, Label string }
$2
	return nil
}
GO
}

expect() {
  local want="$1" name="$2" dir="$3"
  if go run "$checker" "$dir" >"$work/out.txt" 2>&1; then got=pass; else got=fail; fi
  if [[ "$got" != "$want" ]]; then
    echo "[FAIL] $name: expected $want, got $got"
    cat "$work/out.txt"
    exit 1
  fi
  echo "[OK] $name ($want)"
}

d="$(make_project happy)"
write_repo "$d" '	_ = r.db.QueryRow(ctx, `INSERT INTO widgets (organization_id, name, label) VALUES ($1, $2, $3) RETURNING id, name, label`, orgID, name, label).Scan(&w.ID, &w.Name, &w.Label)'
expect pass "happy path" "$d"

d="$(make_project scan_swap)"
write_repo "$d" '	_ = r.db.QueryRow(ctx, `SELECT id, name, label FROM widgets WHERE id = $1`, orgID).Scan(&w.ID, &w.Label, &w.Name)'
expect fail "swapped Scan fields" "$d"

d="$(make_project arg_swap)"
write_repo "$d" '	_, _ = r.db.Exec(ctx, `INSERT INTO widgets (organization_id, name, label) VALUES ($1, $2, $3)`, orgID, label, name)'
expect fail "swapped INSERT args" "$d"

d="$(make_project missing_notnull)"
write_repo "$d" '	_, _ = r.db.Exec(ctx, `INSERT INTO widgets (organization_id, name) VALUES ($1, $2)`, orgID, name)'
expect fail "missing NOT NULL column" "$d"

d="$(make_project bad_column)"
write_repo "$d" '	_ = r.db.QueryRow(ctx, `SELECT id, title, label FROM widgets WHERE id = $1`, orgID).Scan(&w.ID, &w.Name, &w.Label)'
expect fail "unknown column" "$d"

d="$(make_project unscoped)"
write_repo "$d" '	_, _ = r.db.Exec(ctx, `DELETE FROM widgets`)'
expect fail "unscoped DELETE" "$d"

d="$(make_project waiver)"
write_repo "$d" '	// cp:allow-sql-shape fixture proves the waiver path
	_, _ = r.db.Exec(ctx, `DELETE FROM widgets`)'
expect pass "waiver comment" "$d"

echo "sql contract alignment test passed"
