#!/bin/zsh
# projection_audience_check.sh — the projection read path authorizes by scope.
#
# A subscriber entitled to a scope receives every record in that scope for its
# tenant, on the snapshot and on the live delta stream. That is correct when the
# organization IS the customer. When one organization holds many end users, the
# tenant is not an audience, it is everybody, and the read path broadcasts every
# user's rows to every signed-in device.
#
# Nothing downstream re-checks that decision. A per-record filter in a UI is not
# a control, because the rows reach the device before any client code runs.
#
# This check reports scopes that need an audience policy and wiring that cannot
# work. See docs/projection_freshness_contract.md and docs/security_practices.md.
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0
warned=0

ok()   { echo "[OK] $1"; }
warn() { echo "[WARN] $1"; shift; local line; for line in "$@"; do [[ -n "$line" ]] && echo "  $line"; done; warned=1; }
fail() { echo "[FAIL] $1"; shift; local line; for line in "$@"; do [[ -n "$line" ]] && echo "  $line"; done; failed=1; }

# Search project-authored Go only. The vendored foundation copy carries the
# mechanism and its own tests, and matching those would make every project look
# configured.
search_roots=()
for candidate in cmd internal pkg app services; do
  [[ -d "$target/$candidate" ]] && search_roots+=("$target/$candidate")
done
if [[ ${#search_roots[@]} -eq 0 ]]; then
  ok "projection audience: no project Go sources to inspect"
  exit 0
fi

matches() {
  # grep -r over the project roots for a fixed string, files listed once.
  grep -rlF -- "$1" "${search_roots[@]}" --include='*.go' 2>/dev/null || true
}

gateway_users="$(matches 'projectiongw.')"
if [[ -z "$gateway_users" ]]; then
  ok "projection audience: project does not use projectiongw"
  exit 0
fi

policy_sites="$(matches 'WithAudience(')"
per_record_sites="$(matches 'AudiencePerRecord')"
resolver_sites="$(matches 'Audience:')"
strict_sites="$(matches 'Strict:')"

# 1. Per-record scopes with no subject resolver cannot serve anyone. Every read
#    of those scopes answers 403, which is a wiring error, not a posture.
if [[ -n "$per_record_sites" && -z "$resolver_sites" ]]; then
  fail "projection audience: AudiencePerRecord declared with no HandlerConfig.Audience resolver" \
    "declared in: $(echo "$per_record_sites" | tr '\n' ' ')" \
    "every read of a per-record scope will answer 403 until a resolver is wired" \
    "set HandlerConfig.Audience to derive the caller's ids from verified claims"
fi

# 2. Using the gateway without declaring any policy leaves every scope
#    tenant-wide. That is a deliberate posture for a B2B deployment and a
#    disclosure for a consumer one, so it is reported, never assumed.
if [[ -z "$policy_sites" ]]; then
  warn "projection audience: no scope declares an audience policy" \
    "projectiongw used in: $(echo "$gateway_users" | tr '\n' ' ')" \
    "every subscriber receives every record of the scopes it binds, for its whole organization" \
    "if one organization holds many end users, this is a cross-user disclosure" \
    "declare WithAudience(AudienceConfig{Policies: ...}) for scopes owned by particular parties" \
    "see docs/projection_freshness_contract.md (Audience) and docs/security_practices.md"
else
  ok "projection audience: an audience policy is declared"
  # 3. Strict turns a forgotten scope into a refusal instead of a broadcast.
  if [[ -z "$strict_sites" ]]; then
    warn "projection audience: AudienceConfig.Strict is not set" \
      "a scope nobody declared still falls back to tenant-wide delivery" \
      "set Strict once every projected scope has a declared policy"
  else
    ok "projection audience: Strict refuses undeclared scopes"
  fi
  # 4. A per-record scope whose deletes carry no audience cannot converge a
  #    deletion live. The gateway withholds those tombstones and counts them.
  if [[ -n "$per_record_sites" ]]; then
    if [[ -z "$(matches 'DeleteRecordWithFields')$(matches 'AddDeleteRecordSource')" ]]; then
      warn "projection audience: per-record scopes delete without audience fields" \
        "an identity-only tombstone names no audience, so the gateway withholds it" \
        "deletions then converge only on the reader's next snapshot" \
        "use ProjectedRuntimeStore.DeleteRecordWithFields or MirrorSweeper.AddDeleteRecordSource" \
        "watch Gateway.AudienceDrops(): it should stay flat, not track delete traffic"
    else
      ok "projection audience: deletes carry audience fields"
    fi
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "projection audience check failed"
  exit 1
fi
if [[ "$warned" -ne 0 ]]; then
  echo "projection audience check passed with warnings"
fi
exit 0
