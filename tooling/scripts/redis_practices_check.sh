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

# Bounded ranked windows (docs/redis_practices.md). Sorted-set members carry no
# TTL, so a file that grows a sorted set must also trim it in the same file.
typeset -a app_go_roots ranked_files
for root in "$target/internal" "$target/cmd"; do
  [[ -d "$root" ]] && app_go_roots+=("$root")
done
if [[ "${#app_go_roots[@]}" -gt 0 ]]; then
  ranked_files=("${(@f)$(rg -l '\.(ZAdd|ZAddMany|ZIncrBy)\(' "${app_go_roots[@]}" \
    --glob '*.go' --glob '!*_test.go' --glob '!**/foundation/**' --glob '!**/server-kit/**' 2>/dev/null || true)}")
  ranked_files=(${ranked_files:#})
fi
ranked_failed=0
for file in "${ranked_files[@]}"; do
  if ! rg -q '(ZRemRangeByRank|TrimRankedWindow|BumpRanked)\(' "$file"; then
    echo "[FAIL] $file adds to a sorted set without trimming it; use rediskit.BumpRanked or rediskit.TrimRankedWindow"
    ranked_failed=1
  fi
done
if [[ "$ranked_failed" -ne 0 ]]; then
  failed=1
elif [[ "${#ranked_files[@]}" -gt 0 ]]; then
  echo "[OK] every sorted-set writer trims (${#ranked_files[@]} files)"
fi

# Redis budget manifest: the summed key-family budget must fit 80% of
# maxmemory, and every default a row is bound to must equal its max_entries.
budget="$target/config/redis_budget.tsv"
redis_conf="$target/config/redis.conf"
if [[ -f "$budget" ]]; then
  maxmemory_bytes=""
  if [[ -f "$redis_conf" ]]; then
    maxmemory_bytes=$(awk '$1 == "maxmemory" {
      v = tolower($2); n = v + 0
      if (v ~ /gb?$/) n *= 1024 * 1024 * 1024
      else if (v ~ /mb?$/) n *= 1024 * 1024
      else if (v ~ /kb?$/) n *= 1024
      printf "%.0f", n
    }' "$redis_conf")
  fi
  if [[ -z "$maxmemory_bytes" || "$maxmemory_bytes" == "0" ]]; then
    echo "[FAIL] $budget exists but config/redis.conf sets no maxmemory to budget against"
    failed=1
  else
    verdict=$(awk -F'\t' -v cap="$maxmemory_bytes" '
      /^[[:space:]]*#/ || NF < 3 { next }
      { total += $2 * $3 * (($5 + 0) > 0 ? $5 : 1) }
      END {
        limit = cap * 0.8
        printf "%s %.0f %.0f", (total <= limit ? "ok" : "over"), total / 1048576, limit / 1048576
      }' "$budget")
    read -r state total_mb limit_mb <<<"$verdict"
    if [[ "$state" != "ok" ]]; then
      echo "[FAIL] redis budget ${total_mb}MB exceeds 80% of maxmemory (${limit_mb}MB); shrink a window or raise maxmemory"
      failed=1
    else
      echo "[OK] redis budget ${total_mb}MB within 80% of maxmemory (${limit_mb}MB)"
    fi
  fi

  typeset -a default_files go_roots
  for f in "$target"/docker-compose*.yml "$target/.env.example"; do
    [[ -f "$f" ]] && default_files+=("$f")
  done
  go_roots=("${app_go_roots[@]}")
  while IFS=$'\t' read -r family entries _bytes bound _rest; do
    [[ -z "$family" || "$family" == \#* || -z "$bound" || "$bound" == "-" ]] && continue
    for ref in ${(s:,:)bound}; do
      kind="${ref%%:*}"
      name="${ref#*:}"
      typeset -a found=()
      case "$kind" in
        env)
          if [[ "${#default_files[@]}" -gt 0 ]]; then
            found+=("${(@f)$(rg -o --no-filename "\\\$\\{${name}:-([0-9_]+)\\}" -r '$1' "${default_files[@]}" 2>/dev/null || true)}")
            found+=("${(@f)$(rg -o --no-filename "^${name}=([0-9_]+)" -r '$1' "${default_files[@]}" 2>/dev/null || true)}")
          fi
          if [[ "${#go_roots[@]}" -gt 0 ]]; then
            found+=("${(@f)$(rg -o --no-filename "\"${name}\",\\s*([0-9_]+)\\)" -r '$1' "${go_roots[@]}" --glob '*.go' --glob '!*_test.go' 2>/dev/null || true)}")
          fi
          ;;
        const)
          if [[ "${#go_roots[@]}" -gt 0 ]]; then
            found+=("${(@f)$(rg -o --no-filename "\\b${name}\\s*=\\s*([0-9_]+)\\b" -r '$1' "${go_roots[@]}" --glob '*.go' --glob '!*_test.go' 2>/dev/null || true)}")
          fi
          ;;
        *)
          echo "[FAIL] $budget row $family has unknown bound kind '$kind' (use env:VAR or const:NAME)"
          failed=1
          continue
          ;;
      esac
      found=(${found:#})
      if [[ "${#found[@]}" -eq 0 ]]; then
        echo "[FAIL] $budget row $family is bound to $ref but no default for it was found"
        failed=1
        continue
      fi
      for value in "${found[@]}"; do
        if [[ "${value//_/}" != "$entries" ]]; then
          echo "[FAIL] $ref defaults to ${value//_/} but $budget budgets $family for $entries entries"
          failed=1
        fi
      done
    done
  done <"$budget"
elif [[ "${#ranked_files[@]}" -gt 0 ]]; then
  echo "[FAIL] sorted sets are written but config/redis_budget.tsv is missing; budget each key family against maxmemory"
  failed=1
fi

if [[ "$failed" -ne 0 ]]; then
  echo "redis practices check failed"
  exit 1
fi

echo "redis practices check passed"
