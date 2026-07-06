#!/bin/zsh
set -euo pipefail

target="${1:-.}"
service_root="$target/internal/service"
[[ -d "$service_root" ]] || service_root="$target/backend/internal/service"

if [[ ! -d "$service_root" || ! -d "$target/api/protos" ]]; then
  echo "[OK] no service/proto domain surface present"
  exit 0
fi

failed=0
checked=0
warnings=0

# Domains that legitimately own no relational table (object-storage or event-only
# persistence). Their proto fields live on other domains' tables (e.g. *_url columns),
# so a proto<->migration overlap check would always be a false positive. Space-separated.
table_less_domains="media"

normalize_tokens() {
  tr '[:upper:]' '[:lower:]' |
    sed -E 's/([a-z0-9])([A-Z])/\1_\2/g; s/[^a-z0-9_]+/\n/g' |
    sed '/^$/d'
}

field_tokens_for_proto_dir() {
  local proto_dir="$1"
  rg -o '^[[:space:]]*(repeated[[:space:]]+)?([A-Za-z0-9_.<>]+)[[:space:]]+([a-z][A-Za-z0-9_]*)[[:space:]]*=' "$proto_dir" --glob '*.proto' 2>/dev/null |
    rg -v '(^|:|[[:space:]])option[[:space:]]' |
    sed -E 's/.*[[:space:]]([a-z][A-Za-z0-9_]*)[[:space:]]*=.*/\1/' |
    normalize_tokens |
    sort -u |
    rg -v '^(id|metadata|created_at|updated_at|deleted_at|status|state|type|name|description|notes|page|limit|offset)$' || true
}

go_tokens_for_service_dir() {
  local dir="$1"
  rg -o '[A-Za-z][A-Za-z0-9_]*(ID|Id|At|Time|Date|Ref|Code|Type|State|Status|Amount|Minor|Price|Name|Title|Body|Note|Latitude|Longitude|Address|City)?' "$dir" --glob '*.go' 2>/dev/null |
    normalize_tokens |
    sort -u || true
}

# Table-block aware column extraction. A line-scoped grep for the domain word misses
# almost every column (SQL rarely repeats the table name per line), so it only ever saw
# CHECK-enum values and constraint names -> spurious "no overlap" drift warnings. Instead
# we locate CREATE TABLE blocks whose name belongs to the domain and emit each column's
# leading identifier. Empty output => the domain owns no relational table.
migration_column_tokens_for_domain() {
  local domain="$1"
  [[ -d "$target/migrations" ]] || return 0
  setopt local_options null_glob
  local files=("$target"/migrations/*.up.sql)
  (( ${#files} )) || return 0
  awk -v d="$domain" '
    FNR==1 { capture=0 }
    /CREATE[ \t]+TABLE/ {
      t=$0
      sub(/.*CREATE[ \t]+TABLE([ \t]+IF[ \t]+NOT[ \t]+EXISTS)?[ \t]+/, "", t)
      sub(/[ \t("].*/, "", t)
      gsub(/[`"]/, "", t)
      sub(/^[a-z0-9_]+\./, "", t)   # strip schema qualifier
      capture = (tolower(t) ~ ("(^|_)" d "s?($|_)"))
      next
    }
    capture && /^[ \t]*[a-z][a-z0-9_]*[ \t]/ {
      c = tolower($1)
      if (c !~ /^(constraint|primary|unique|foreign|check|index|like)$/) print c
    }
    capture && /\);/ { capture=0 }
  ' "${files[@]}" 2>/dev/null | sort -u || true
}

timestamp_proto_violations() {
  local proto_dir="$1"
  rg -n '^[[:space:]]*(string|int32|int64|uint64|double|float)[[:space:]]+(created_at|updated_at|deleted_at|expires_at|expired_at|occurred_at|reviewed_at|verified_at|scheduled_for|delivered_at|acknowledged_at|published_at|refreshed_at|last_seen_at|first_seen_at|[a-z][A-Za-z0-9_]*(_at|_date))[[:space:]]*=' "$proto_dir" --glob '*.proto' 2>/dev/null || true
}

timestamp_sql_violations() {
  local domain="$1"
  [[ -d "$target/migrations" ]] || return 0
  rg -n "^[[:space:]]+(created_at|updated_at|deleted_at|expires_at|expired_at|occurred_at|reviewed_at|verified_at|scheduled_for|delivered_at|acknowledged_at|published_at|refreshed_at|last_seen_at|first_seen_at|[a-z][a-z0-9_]*_at)[[:space:]]+(TEXT|VARCHAR|CHAR|INT|BIGINT)" "$target/migrations" --glob '*.up.sql' 2>/dev/null |
    rg "($domain|${domain}s|_)" || true
}

while IFS= read -r service_dir; do
  domain="$(basename "$service_dir")"
  case "$domain" in
    .*|_*|common|transport) continue ;;
  esac

  proto_dir="$target/api/protos/$domain"
  [[ -d "$proto_dir" ]] || continue
  checked=$((checked + 1))

  proto_tokens="$(field_tokens_for_proto_dir "$proto_dir")"
  go_tokens="$(go_tokens_for_service_dir "$service_dir")"
  migration_tokens="$(migration_column_tokens_for_domain "$domain")"

  if [[ -n "$proto_tokens" ]]; then
    if [[ -z "$migration_tokens" ]]; then
      # No CREATE TABLE block belongs to this domain -> no relational persistence to drift
      # against. Announce the known object-store/event-only domains; stay silent for the
      # rest rather than inventing drift we cannot actually measure.
      if [[ " $table_less_domains " == *" $domain "* ]]; then
        echo "[INFO] $domain has no owned table (object-store/event-only); migration overlap skipped"
      fi
    else
      overlap="$(comm -12 <(print -r -- "$proto_tokens") <(print -r -- "$migration_tokens") | head -5)"
      if [[ -z "$overlap" ]]; then
        echo "[WARN] $domain proto fields do not overlap owned-table columns"
        echo "  check api/protos/$domain against migrations for table/column naming drift"
        warnings=$((warnings + 1))
      fi
    fi
  fi

  if [[ -n "$proto_tokens" && -n "$go_tokens" ]]; then
    overlap="$(comm -12 <(print -r -- "$proto_tokens") <(print -r -- "$go_tokens") | head -5)"
    if [[ -z "$overlap" ]]; then
      echo "[FAIL] $domain proto fields do not overlap service Go vocabulary"
      echo "  check api/protos/$domain against internal/service/$domain model/request names"
      failed=1
    fi
  fi

  proto_time_bad="$(timestamp_proto_violations "$proto_dir")"
  if [[ -n "$proto_time_bad" ]]; then
    echo "[WARN] $domain proto timestamp-like fields may need google.protobuf.Timestamp"
    echo "$proto_time_bad" | sed 's/^/  /' | head -10
    warnings=$((warnings + 1))
  fi

  sql_time_bad="$(timestamp_sql_violations "$domain")"
  if [[ -n "$sql_time_bad" ]]; then
    echo "[WARN] $domain migration timestamp-like columns may need timestamp types"
    echo "$sql_time_bad" | sed 's/^/  /' | head -10
    warnings=$((warnings + 1))
  fi
done < <(find "$service_root" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort)

if [[ "$failed" -ne 0 ]]; then
  echo "domain contract consistency check failed"
  exit 1
fi

echo "[OK] domain contract consistency checked $checked service domains"
if [[ "$warnings" -gt 0 ]]; then
  echo "[OK] domain contract consistency emitted $warnings schema/proto review warnings"
fi
echo "domain contract consistency check passed"
