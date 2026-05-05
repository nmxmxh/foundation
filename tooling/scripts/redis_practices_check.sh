#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

if rg -n "redis" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
  if ! rg -n "(key_prefix|KeyPrefix|REDIS_PREFIX|RedisPrefix|keyPrefix)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without key prefix configuration"
    failed=1
  else
    echo "[OK] redis key prefix configuration present"
  fi

  if ! rg -n "(ttl|TTL|expire|Expire)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without TTL policy"
    failed=1
  else
    echo "[OK] redis TTL policy present"
  fi

  if ! rg -n "(REDIS_POOL_SIZE|RedisPoolSize|pool_size|PoolSize|ConnectWithOptions)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without pool sizing baseline"
    failed=1
  else
    echo "[OK] redis pool sizing baseline present"
  fi

  if ! rg -n "(REDIS_SHARD_URLS|RedisShardURLs|shard_urls|ShardURLs)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
    echo "[FAIL] redis usage detected without shard-url extension point"
    failed=1
  else
    echo "[OK] redis shard-url extension point present"
  fi

  if ! rg -n "(allkeys-lfu|pipeline|Pipelined|Pipeline|io-threads)" "$target" --glob '!**/node_modules/**' >/dev/null 2>&1; then
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
