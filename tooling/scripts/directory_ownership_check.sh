#!/bin/zsh
set -euo pipefail

target="${1:-.}"
manifest="$target/tooling/foundation_ownership.tsv"
failed=0

fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

ok() {
  echo "[OK] $1"
}

if [[ ! -f "$manifest" ]]; then
  fail "foundation ownership manifest exists" "missing: ${manifest#$target/}"
else
  ok "foundation ownership manifest exists"
fi

if [[ -f "$manifest" ]]; then
  while IFS=$'\t' read -r prefix classification propagation notes; do
    [[ -n "${prefix:-}" ]] || continue
    [[ "$prefix" == \#* ]] && continue
    if [[ ! -e "$target/$prefix" ]]; then
      fail "owned path exists" "$prefix ($classification/$propagation)"
    fi
  done <"$manifest"
fi

if find "$target/tooling/scripts" -maxdepth 1 -type f -iname '*service*backed*' | grep -q .; then
  fail "service-backed scripts stay out of scaffolded tooling" "tooling/scripts contains service-backed files"
else
  ok "service-backed scripts stay out of scaffolded tooling"
fi

if grep -Fq "server-kit/go/servicebacked" "$target/templates/scaffold.manifest.tsv"; then
  fail "service-backed package is not scaffolded" "templates/scaffold.manifest.tsv references server-kit/go/servicebacked"
else
  ok "service-backed package is not scaffolded"
fi

if find "$target/templates/backend/internal/service" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -q .; then
  fail "backend template does not predefine domain service directories" "templates/backend/internal/service must stay empty/.gitkeep"
else
  ok "backend template does not predefine domain service directories"
fi

if [[ -d "$target/templates/backend/internal/service" ]] && [[ ! -f "$target/templates/backend/internal/service/.gitkeep" ]]; then
  fail "backend service template keeps only .gitkeep" "missing templates/backend/internal/service/.gitkeep"
else
  ok "backend service template keeps only .gitkeep"
fi

if find "$target/templates/backend/internal" -mindepth 1 -maxdepth 1 -type d \( -name persistence -o -name shared -o -name adapters -o -name servicekit -o -name testutil \) | grep -q .; then
  fail "backend template avoids primitive infrastructure folders" "primitive folders must come from server-kit"
else
  ok "backend template avoids primitive infrastructure folders"
fi

if [[ -d "$target/server-kit/go" ]]; then
  if find "$target/server-kit/go" -mindepth 1 -maxdepth 1 -type d \( -name persistence -o -name shared -o -name utils -o -name common \) | grep -q .; then
    fail "server-kit modules use explicit domain names" "avoid generic module names like persistence/shared/utils/common"
  else
    ok "server-kit modules use explicit domain names"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "directory ownership check failed"
  exit 1
fi

echo "directory ownership check passed"
