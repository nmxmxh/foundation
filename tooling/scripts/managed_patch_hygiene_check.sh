#!/bin/zsh
set -euo pipefail

# managed_patch_hygiene_check.sh
#
# Managed patches deliberately reach into project-owned files. That is their
# purpose: it is the only way to deliver a fix to a project scaffolded two years
# ago. Nothing here argues with that.
#
# What this checks is the other half — the part that has no natural pressure to
# happen. A patch, once written, is never removed. It accumulates, it has to be
# read and reasoned about by everyone who touches the file afterwards, and its
# search string stays live forever. That last part is the real hazard: a patch
# looking for `GO_VERSION=1.25` sits dormant for years and then fires again the
# day someone legitimately pins 1.25.
#
# So every patch must declare when it was introduced, and a patch older than the
# oldest supported Foundation version is reported as retirable — because every
# project that could have needed it has already had it applied.
#
#   @since <version>   required on every patch function
#   retirable          @since strictly older than tooling/min_supported_foundation

target="${1:-.}"
target="$(cd "$target" && pwd)"

patch_script="$target/tooling/scripts/scaffold_managed_patches.sh"
min_file="$target/tooling/min_supported_foundation"
failed=0

# App-safe: tooling/scripts is copied wholesale into every generated project as
# scripts/checks. Patch hygiene is a Foundation-maintenance concern — a project
# does not author managed patches — so outside a Foundation checkout this is a
# skip, not a failure.
if [[ ! -f "$patch_script" || ! -f "$min_file" ]]; then
  echo "[SKIP] not a Foundation checkout; managed-patch hygiene is maintained upstream."
  exit 0
fi

min_supported="$(grep -v '^#' "$min_file" | grep -v '^[[:space:]]*$' | head -n 1 | tr -d '[:space:]')"
if [[ -z "$min_supported" ]]; then
  echo "[FAIL] tooling/min_supported_foundation has no version line"
  exit 1
fi

# version_lt A B -- true when A sorts strictly before B, comparing numerically
# component by component so 0.10.0 is correctly newer than 0.9.0.
version_lt() {
  local a="$1" b="$2"
  [[ "$a" == "$b" ]] && return 1
  local first
  first="$(printf '%s\n%s\n' "$a" "$b" | sort -t. -k1,1n -k2,2n -k3,3n | head -n 1)"
  [[ "$first" == "$a" ]]
}

# ---------------------------------------------------------------------------
# every patch function declares provenance
# ---------------------------------------------------------------------------

missing=0
total=0
retirable=0

# Walk the file so a function's @since is read from the line directly above it,
# which keeps the metadata next to the code instead of in a second file that can
# drift away from it.
prev_since=""
while IFS= read -r line; do
  if [[ "$line" == "# @since "* ]]; then
    prev_since="${line#\# @since }"
    prev_since="${prev_since//[[:space:]]/}"
    continue
  fi
  if [[ "$line" =~ '^(patch|rename|sync|replace|ensure|remove)_[a-z0-9_]+\(\) \{' ]]; then
    local_name="${line%%\(*}"
    total=$((total + 1))
    if [[ -z "$prev_since" ]]; then
      echo "[FAIL] $local_name has no '# @since <version>' line"
      echo "  A patch without provenance can never be retired: nobody can prove"
      echo "  every supported project has already received it."
      missing=$((missing + 1))
      failed=1
    elif version_lt "$prev_since" "$min_supported"; then
      echo "[RETIRABLE] $local_name (@since $prev_since < min supported $min_supported)"
      echo "  Every supported project has passed through this. Delete it, and drop"
      echo "  its call from the invocation list at the end of the script."
      retirable=$((retirable + 1))
    fi
  fi
  prev_since=""
done < "$patch_script"

# ---------------------------------------------------------------------------
# every declared patch is actually invoked
#
# An annotated function that nobody calls is the quietest kind of stale: it
# reads like live behaviour during review but does nothing.
# ---------------------------------------------------------------------------

uninvoked=0
while IFS= read -r fn; do
  # A call is any bare mention that is not the definition itself.
  if ! grep -qE "^[[:space:]]*${fn}( |$|\")" "$patch_script"; then
    echo "[FAIL] $fn is defined but never invoked"
    uninvoked=$((uninvoked + 1))
    failed=1
  fi
done < <(grep -oE '^(patch|rename|sync|replace|ensure|remove)_[a-z0-9_]+\(\) \{' "$patch_script" | sed 's/() {//')

# ---------------------------------------------------------------------------

echo
echo "Managed patch hygiene:"
echo "  patches:            $total"
echo "  missing @since:     $missing"
echo "  retirable:          $retirable"
echo "  defined not called: $uninvoked"
echo "  min supported:      $min_supported"

if (( retirable > 0 )); then
  echo
  echo "$retirable patch(es) are retirable. Removing them is the intended"
  echo "maintenance step, not an optional cleanup — the file only shrinks if"
  echo "someone acts on this."
fi

if (( failed != 0 )); then
  echo
  echo "Managed patch hygiene check failed"
  exit 1
fi

echo
echo "Managed patch hygiene check passed"
