#!/bin/zsh
set -euo pipefail

target="${1:-.}"
migrations_dir="$target/migrations"

if [[ ! -d "$migrations_dir" ]]; then
  echo "[OK] no migrations directory present"
  exit 0
fi

actual=("${(@f)$(cd "$migrations_dir" && ls -1 *.sql | sort)}")
expected=(
  "0001_schema.down.sql"
  "0001_schema.up.sql"
  "0002_seed_system_data.down.sql"
  "0002_seed_system_data.up.sql"
  "0003_seed_demo_data.down.sql"
  "0003_seed_demo_data.up.sql"
)

if [[ "${actual[*]}" != "${expected[*]}" ]]; then
  echo "[FAIL] migration structure must remain exactly 0001/0002/0003 during active v1"
  printf '%s\n' "${actual[@]}"
  exit 1
fi

echo "migration structure check passed"
