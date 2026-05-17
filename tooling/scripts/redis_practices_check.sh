#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
rg_file_args=(
  --glob '*.go'
  --glob '*.sql'
  --glob '*.yml'
  --glob '*.yaml'
  --glob '*.conf'
  --glob '*.env'
  --glob '*.env.example'
  --glob '*.md'
  --glob 'Makefile'
  --glob '!**/.git/**'
  --glob '!**/.cache/**'
  --glob '!**/node_modules/**'
  --glob '!**/dist/**'
  --glob '!**/build/**'
  --glob '!**/target/**'
  --glob '!**/coverage/**'
)

typeset -a scan_roots
for root in \
  "$target/.env.example" \
  "$target/Makefile" \
  "$target/docker-compose.yml" \
  "$target/docker-compose.dev.yml" \
  "$target/docker-compose.test.yml" \
  "$target/config" \
  "$target/scripts/checks" \
  "$target/server-kit/go" \
  "$target/foundation/server-kit/go" \
  "$target/internal" \
  "$target/cmd" \
  "$target/tests" \
  "$target/migrations" \
  "$target/templates"; do
  [[ -e "$root" ]] && scan_roots+=("$root")
done

if [[ "${#scan_roots[@]}" -eq 0 ]]; then
  echo "[OK] no redis usage detected"
elif rg -n "redis" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
  if ! rg -n "(key_prefix|KeyPrefix|REDIS_PREFIX|RedisPrefix|keyPrefix)" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without key prefix configuration"
    failed=1
  else
    echo "[OK] redis key prefix configuration present"
  fi

  if ! rg -n "(ttl|TTL|expire|Expire)" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without TTL policy"
    failed=1
  else
    echo "[OK] redis TTL policy present"
  fi

  if ! rg -n "(REDIS_POOL_SIZE|RedisPoolSize|pool_size|PoolSize|ConnectWithOptions)" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without pool sizing baseline"
    failed=1
  else
    echo "[OK] redis pool sizing baseline present"
  fi

  if ! rg -n "(REDIS_SHARD_URLS|RedisShardURLs|shard_urls|ShardURLs)" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without shard-url extension point"
    failed=1
  else
    echo "[OK] redis shard-url extension point present"
  fi

  if ! rg -n "(allkeys-lfu|pipeline|Pipelined|Pipeline|io-threads)" "${scan_roots[@]}" "${rg_file_args[@]}" >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without adaptive eviction/pipeline baseline"
    failed=1
  else
    echo "[OK] redis adaptive eviction/pipeline baseline present"
  fi
else
  echo "[OK] no redis usage detected"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "redis practices check failed"
  exit 1
fi

echo "redis practices check passed"
