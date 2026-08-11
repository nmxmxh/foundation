#!/bin/zsh
set -euo pipefail

# version_consistency_check.sh
#
# Two modes.
#
#   (default)      Foundation-internal consistency. Every version literal in
#                  templates/, tests/, and .github/ must equal tooling/versions.tsv.
#                  A version bump becomes a one-line manifest edit plus this gate
#                  proving the edit reached all nine-odd declaration sites.
#
#   --app <path>   Scaffolded-app validity. The app's own .env is checked against
#                  the manifest's `supported` column, not against Foundation's
#                  current value. Reversible keys must be current; irreversible
#                  keys need only sit inside the supported range, so an app that
#                  pinned Postgres 16 in 2027 still passes in 2035.
#
# The app mode is the one that answers "is this service actually valid", because
# it reads .env — what Docker Compose resolves at run time — rather than
# .env.example, which is only the seeded default.

target="${1:-.}"
mode="foundation"
app_path=""

if [[ "${1:-}" == "--app" ]]; then
  mode="app"
  app_path="${2:?--app requires a project path}"
  target="."
elif [[ "${2:-}" == "--app" ]]; then
  mode="app"
  app_path="${3:?--app requires a project path}"
fi

target="$(cd "$target" && pwd)"
failed=0

# This whole directory is copied into every generated project as scripts/checks,
# so the script has to stay app-safe. A project carries the manifest under
# foundation/ if it vendors one at all; a project that does not is not a
# consistency failure, it simply has nothing here to check.
manifest=""
for candidate in "$target/tooling/versions.tsv" "$target/foundation/tooling/versions.tsv"; do
  if [[ -f "$candidate" ]]; then
    manifest="$candidate"
    break
  fi
done

if [[ -z "$manifest" ]]; then
  if [[ "$mode" == "app" ]]; then
    echo "[SKIP] no version manifest available; cannot range-check this project."
    echo "  Run this from a Foundation checkout, or vendor foundation/tooling/versions.tsv."
    exit 0
  fi
  echo "[SKIP] no tooling/versions.tsv here; nothing to verify."
  exit 0
fi

# ---------------------------------------------------------------------------
# manifest access
# ---------------------------------------------------------------------------

manifest_field() {
  local key="$1" field="$2"
  awk -F'\t' -v k="$key" -v f="$field" '
    /^#/ { next }
    NF && $1 == k { print $f; exit }
  ' "$manifest"
}

manifest_keys() {
  awk -F'\t' '/^#/ { next } NF { print $1 }' "$manifest"
}

# extract_version FILE PERL_REGEX -- prints every capture group 1 match.
#
# The pattern travels in the environment and is compiled with qr// rather than
# being interpolated into the -e text. Interpolating breaks the moment a pattern
# contains the delimiter, which a character class like [a-z/] does.
extract_version() {
  local file="$1" regex="$2"
  [[ -f "$file" ]] || return 0
  VC_REGEX="$regex" perl -ne '
    BEGIN { $re = qr/$ENV{VC_REGEX}/ }
    print "$1\n" if /$re/
  ' "$file"
}

# ---------------------------------------------------------------------------
# declaration sites
#
# One row per place a version literal is written, because the sites spell the
# same version differently: 1.26 in a Dockerfile ARG, 1.26.x in a CI matrix,
# 18 in an env file but postgres:18-alpine in a compose image tag. Matching on
# the value alone would miss a site that stopped declaring anything at all, so
# each row names the file and the exact shape expected in it.
#
# format: key<TAB>relative_path<TAB>perl_regex_with_one_capture_group
# ---------------------------------------------------------------------------

SITES=(
"GO_VERSION	templates/docker/Dockerfile	ARG GO_VERSION=([0-9.]+)"
"GO_VERSION	templates/.env.example	^GO_VERSION=([0-9.]+)"
"GO_VERSION	templates/github/workflows/ci.yml	GO_VERSION: \"([0-9.]+)\""
"GO_VERSION	templates/github/workflows/security.yml	go-version: \"([0-9.]+)\""
"GO_VERSION	.github/workflows/core-ci.yml	GO_VERSION: \"([0-9.]+)\\.x\""

# Every module's go directive is a declaration site too. runtime-transport sat
# on 1.24.1 while the rest of the repo was on 1.26.0 and nothing noticed, which
# is precisely the drift this row now prevents. The capture takes major.minor,
# since the directive carries a patch component the toolchain pins do not.
"GO_VERSION	server-kit/go/go.mod	^go ([0-9]+\\.[0-9]+)"
"GO_VERSION	runtime-sdk/go/go.mod	^go ([0-9]+\\.[0-9]+)"
"GO_VERSION	runtime-transport/go/go.mod	^go ([0-9]+\\.[0-9]+)"
"GO_VERSION	config-contracts/go/go.mod	^go ([0-9]+\\.[0-9]+)"
"GO_VERSION	cmd/ovasabi/go.mod	^go ([0-9]+\\.[0-9]+)"

"NODE_VERSION	templates/github/workflows/ci.yml	node-version: \"([0-9]+)\""
"NODE_VERSION	.github/workflows/core-ci.yml	NODE_VERSION: \"([0-9]+)\""

"ALPINE_VERSION	templates/docker/Dockerfile	ARG ALPINE_VERSION=([0-9.]+)"
"ALPINE_VERSION	templates/.env.example	^ALPINE_VERSION=([0-9.]+)"

"MIGRATE_VERSION	templates/docker/Dockerfile.migrate	ARG MIGRATE_VERSION=(v[0-9.]+)"
"MIGRATE_VERSION	templates/.env.example	^MIGRATE_VERSION=(v[0-9.]+)"

"POSTGRES_VERSION	templates/docker/Dockerfile.postgres	ARG POSTGRES_VERSION=([0-9]+)"
"POSTGRES_VERSION	templates/.env.example	^POSTGRES_VERSION=([0-9]+)"
"POSTGRES_VERSION	templates/docker/docker-compose.yml	POSTGRES_VERSION:-([0-9]+)"
"POSTGRES_VERSION	templates/docker/docker-compose.test.yml	postgres:([0-9]+)-alpine"
"POSTGRES_VERSION	templates/github/workflows/ci.yml	POSTGRES_VERSION: \"([0-9]+)\""
"POSTGRES_VERSION	tests/docker-compose.service-backed.yml	postgres:([0-9]+)-alpine"

"POSTGRES_BASE_IMAGE	templates/docker/Dockerfile.postgres	ARG POSTGRES_BASE_IMAGE=(\\S+)"
"POSTGRES_BASE_IMAGE	templates/.env.example	^POSTGRES_BASE_IMAGE=(\\S+)"
"POSTGRES_BASE_IMAGE	templates/docker/docker-compose.yml	POSTGRES_BASE_IMAGE:-([^}]+)"

"REDIS_BASE_IMAGE	templates/docker/Dockerfile.redis	ARG REDIS_BASE_IMAGE=(\\S+)"
"REDIS_BASE_IMAGE	templates/.env.example	^REDIS_BASE_IMAGE=(\\S+)"
"REDIS_BASE_IMAGE	templates/docker/docker-compose.yml	REDIS_BASE_IMAGE:-([^}]+)"

"REDIS_VERSION	templates/docker/Dockerfile.redis	ARG REDIS_VERSION=([0-9]+-alpine)"
"REDIS_VERSION	templates/.env.example	^REDIS_VERSION=([0-9]+-alpine)"
"REDIS_VERSION	templates/docker/docker-compose.yml	REDIS_VERSION:-([0-9]+-alpine)"
"REDIS_VERSION	templates/docker/docker-compose.test.yml	redis:([0-9]+-alpine)"
"REDIS_VERSION	templates/github/workflows/ci.yml	image: redis:([0-9]+-alpine)"
"REDIS_VERSION	tests/docker-compose.service-backed.yml	redis:([0-9]+-alpine)"

# The managed-patch script is a declaration site too, and an easily forgotten
# one: it carries whole compose fragments as string literals, so a template that
# gains an arg leaves the patch injecting the old shape into every project that
# has not been updated yet. That is exactly how POSTGRES_BASE_IMAGE went missing
# from the injected block while the template already had it.
"POSTGRES_VERSION	tooling/scripts/scaffold_managed_patches.sh	POSTGRES_VERSION:-([0-9]+)"
"REDIS_VERSION	tooling/scripts/scaffold_managed_patches.sh	REDIS_VERSION:-([0-9]+-alpine)"
"POSTGRES_BASE_IMAGE	tooling/scripts/scaffold_managed_patches.sh	POSTGRES_BASE_IMAGE:-([a-z/]+)"
"REDIS_BASE_IMAGE	tooling/scripts/scaffold_managed_patches.sh	REDIS_BASE_IMAGE:-([a-z/]+)"
)

# ---------------------------------------------------------------------------
# range support
# ---------------------------------------------------------------------------

# major_of reduces a version to the engine major integer used for range checks.
#
# It must cope with every shape a distribution writes:
#   18            stock postgres        -> 18
#   8-alpine      redis image tag       -> 8
#   16-3.4        postgis (pg16 + gis)  -> 16
#   17.2          patch-qualified       -> 17
# and with a full image reference, where the tag follows the last colon:
#   postgis/postgis:16-3.4              -> 16
#
# TimescaleDB inverts the order (2.17.2-pg16), so the engine major is taken from
# an explicit -pgNN suffix when one is present rather than from the leading
# component, which there is the Timescale version and not the engine.
major_of() {
  printf '%s\n' "${1##*:}" | perl -ne '
    if (/-pg(\d+)/) { print $1; exit }
    print $1 if /^(\d+)/
  '
}

in_range() {
  local value="$1" range="$2"
  local lo="${range%%..*}" hi="${range##*..}"
  local v vlo vhi
  v="$(major_of "$value")"
  vlo="$(major_of "$lo")"
  vhi="$(major_of "$hi")"
  [[ -n "$v" && -n "$vlo" && -n "$vhi" ]] || return 1
  (( v >= vlo && v <= vhi ))
}

# ---------------------------------------------------------------------------
# foundation mode
# ---------------------------------------------------------------------------

check_foundation() {
  local checked=0
  local -A seen_keys

  local row key rel regex expected file found any
  for row in "${SITES[@]}"; do
    [[ -z "$row" ]] && continue
    key="${row%%	*}"
    rel="${${row#*	}%%	*}"
    regex="${row##*	}"

    expected="$(manifest_field "$key" 2)"
    if [[ -z "$expected" ]]; then
      echo "[FAIL] site references key not in versions.tsv: $key ($rel)"
      failed=1
      continue
    fi
    seen_keys[$key]=1

    file="$target/$rel"
    if [[ ! -f "$file" ]]; then
      echo "[FAIL] $key: declaration site is missing"
      echo "  missing file: $rel"
      failed=1
      continue
    fi

    any=0
    while IFS= read -r found; do
      [[ -z "$found" ]] && continue
      any=1
      checked=$((checked + 1))
      if [[ "$found" != "$expected" ]]; then
        echo "[FAIL] $key drift in $rel"
        echo "  found:    $found"
        echo "  manifest: $expected"
        failed=1
      fi
    done < <(extract_version "$file" "$regex")

    if (( any == 0 )); then
      echo "[FAIL] $key: no declaration found in $rel"
      echo "  expected a match for: $regex"
      echo "  a site that stopped declaring its version is drift waiting to happen"
      failed=1
    fi
  done

  # Every manifest key must be exercised by at least one site, so a key cannot
  # be added to the manifest and then quietly never enforced.
  local k
  for k in $(manifest_keys); do
    if [[ -z "${seen_keys[$k]:-}" ]]; then
      echo "[FAIL] $k is in versions.tsv but no declaration site checks it"
      failed=1
    fi
  done

  if (( failed == 0 )); then
    echo "[OK] $checked version declarations across ${#SITES[@]} sites agree with tooling/versions.tsv"
  fi
}

# ---------------------------------------------------------------------------
# app mode
# ---------------------------------------------------------------------------

check_app() {
  local app="$1"
  local env_file="$app/.env"

  if [[ ! -f "$env_file" ]]; then
    echo "[WARN] $app/.env not found; nothing to validate."
    echo "  Compose resolves versions from .env, so an app without one is running pure defaults."
    return 0
  fi

  local key value class supported app_value
  for key in $(manifest_keys); do
    value="$(manifest_field "$key" 2)"
    class="$(manifest_field "$key" 3)"
    supported="$(manifest_field "$key" 4)"

    app_value="$(perl -ne "print \$1 if /^${key}=(.+?)\\s*\$/" "$env_file" || true)"
    if [[ -z "$app_value" ]]; then
      # Absent means the app inherits the compose default, which is Foundation's
      # current value. That is valid, not drift.
      continue
    fi

    if [[ "$supported" == "any" ]]; then
      # Distribution choice. Foundation seeds a default and has no opinion.
      [[ "$app_value" != "$value" ]] && echo "[OK] $key=$app_value (project-selected distribution)"
      continue
    fi

    if [[ "$supported" == "current" ]]; then
      if [[ "$app_value" != "$value" ]]; then
        # Reversible: reverting costs nothing, so this is a warning.
        echo "[WARN] $key=$app_value differs from Foundation's $value"
        echo "  class: $class — safe to move; run the Foundation update to align."
      fi
      continue
    fi

    if in_range "$app_value" "$supported"; then
      echo "[OK] $key=$app_value is inside the supported range $supported"
      continue
    fi

    # Irreversible and out of range: this one fails.
    echo "[FAIL] $key=$app_value is outside the supported range $supported"
    echo "  class: $class — upgrading rewrites on-disk state and cannot be reverted"
    echo "  by changing this value back. Plan a data migration; do not let an"
    echo "  update move it for you."
    failed=1
  done

  if (( failed == 0 )); then
    echo "[OK] app service versions are within their supported ranges"
  fi
}

# ---------------------------------------------------------------------------

if [[ "$mode" == "app" ]]; then
  check_app "$app_path"
else
  check_foundation
fi

if (( failed != 0 )); then
  echo
  echo "Version consistency check failed."
  echo "  Foundation mode: edit tooling/versions.tsv, then update every site it names."
  echo "  App mode: an irreversible service version is outside the supported range."
  exit 1
fi

echo "Version consistency check passed"
