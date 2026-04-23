#!/bin/zsh
set -euo pipefail

target="${1:-.}"
migrations_dir="$target/migrations"

if [[ ! -d "$migrations_dir" ]]; then
  echo "[OK] no migrations directory present"
  exit 0
fi

required=(
  "0001_schema.up.sql"
  "0001_schema.down.sql"
  "0002_seed_system_data.up.sql"
  "0002_seed_system_data.down.sql"
  "0003_seed_demo_data.up.sql"
  "0003_seed_demo_data.down.sql"
)

failed=0
for file in "${required[@]}"; do
  if [[ ! -f "$migrations_dir/$file" ]]; then
    echo "[FAIL] missing migration $file"
    failed=1
  fi
done

schema_file="$migrations_dir/0001_schema.up.sql"
if [[ -f "$schema_file" ]] && rg -n "org_id" "$schema_file" >/dev/null 2>&1; then
  if ! rg -n "create\\s+index.*(org_id|workspace_id)" "$schema_file" >/dev/null 2>&1; then
    echo "[FAIL] schema mentions org_id but has no matching org/workspace index"
    failed=1
  else
    echo "[OK] schema includes org/workspace indexing"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "database practices check failed"
  exit 1
fi

echo "database practices check passed"
