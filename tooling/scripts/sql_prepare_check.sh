#!/bin/zsh
# sql_prepare_check.sh — validate every inline repository SQL statement against
# a live schema by PREPARE-ing it, catching type-deduction failures (SQLSTATE
# 42P08 "inconsistent types deduced") that unit tests can never see because
# they only occur when Postgres resolves parameter types.
#
# pgx sends parameters without type OIDs, so `PREPARE stmt AS <sql>` performs
# the exact same deduction the driver triggers at runtime. A query that
# prepares cleanly here cannot fail on parameter typing in production.
#
# Usage: sql_prepare_check.sh [project-root]
#   Requires DATABASE_URL pointing at a database with the project schema
#   applied (the migrate-test target's database works). Skips gracefully when
#   DATABASE_URL is unset so it can sit in lint pipelines without a DB.
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "[SKIP] DATABASE_URL not set; sql prepare check requires a schema-loaded database"
  exit 0
fi
if ! command -v psql >/dev/null 2>&1; then
  echo "[SKIP] psql not available"
  exit 0
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

python3 - "$target" "$workdir/sweep.sql" <<'PYEOF'
import re, glob, sys, os

target, out = sys.argv[1], sys.argv[2]
files = sorted(set(glob.glob(os.path.join(target, "internal/service/*/*.go"))))
consts = {}
for f in files:
    src = open(f).read()
    for m in re.finditer(r'(\w+ReturnColumns)\s*=\s*`([^`]+)`', src):
        consts[m.group(1)] = m.group(2)

count = 0
with open(out, "w") as fh:
    fh.write("\\set ON_ERROR_STOP off\n")
    for f in files:
        if f.endswith("_test.go"):
            continue
        src = open(f).read()
        for m in re.finditer(r'`([^`]+)`(\s*\+\s*(\w+))?(\s*\+\s*`([^`]+)`)?', src, re.S):
            sql = m.group(1)
            if m.group(3):
                sql += consts.get(m.group(3), "")
            if m.group(5):
                sql += m.group(5)
            if not re.search(r'\b(SELECT|INSERT|UPDATE|DELETE)\b', sql):
                continue
            if '$1' not in sql:
                continue
            line = src[:m.start()].count('\n') + 1
            rel = os.path.relpath(f, target)
            fh.write(f"\\echo === {rel}:{line}\n")
            fh.write(f"BEGIN;\nPREPARE sweep_q{count} AS {sql};\nROLLBACK;\n")
            count += 1
print(f"[INFO] extracted {count} parameterized SQL statements")
PYEOF

output="$(psql "$DATABASE_URL" -f "$workdir/sweep.sql" 2>&1)"
errors="$(echo "$output" | grep -B1 'ERROR' || true)"
if [[ -n "$errors" ]]; then
  echo "$errors"
  echo "[FAIL] repository SQL failed type deduction against the live schema"
  exit 1
fi

echo "[OK] all repository SQL statements prepare cleanly ($(echo "$output" | grep -c '^PREPARE') prepared)"
