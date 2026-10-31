#!/bin/bash
set -euo pipefail

# Installs a Git pre-commit hook that runs the fast race detection gate.
#
# - The hooks directory comes from git (core.hooksPath and worktrees, where
#   .git is a file, both resolve correctly).
# - An existing hook that this script did not write is kept as a timestamped
#   backup and still runs first, so local hooks are never silently replaced.
# - Running the script again is safe.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOKS_DIR="$(git -C "${ROOT}" rev-parse --path-format=absolute --git-path hooks)"
HOOK_FILE="${HOOKS_DIR}/pre-commit"
MARKER="# foundation-race-gate"

mkdir -p "${HOOKS_DIR}"

chained=""
# A hook that already runs check-go-race.sh is ours, from before the marker existed.
if [[ -f "${HOOK_FILE}" ]] && ! grep -qF "${MARKER}" "${HOOK_FILE}" && ! grep -qF "check-go-race.sh" "${HOOK_FILE}"; then
  backup="${HOOK_FILE}.local-$(date -u +%Y%m%dT%H%M%SZ)"
  mv "${HOOK_FILE}" "${backup}"
  chained="${backup}"
  echo "Kept existing pre-commit hook as ${backup}; it runs before the race gate."
elif [[ -f "${HOOK_FILE}" ]]; then
  chained="$(sed -n 's/^# chained: //p' "${HOOK_FILE}")"
fi

{
  echo '#!/bin/bash'
  echo 'set -euo pipefail'
  echo "${MARKER}"
  echo "# chained: ${chained}"
  if [[ -n "${chained}" ]]; then
    printf '"%s" "$@"\n' "${chained}"
  fi
  echo 'ROOT="$(git rev-parse --show-toplevel)"'
  echo 'echo "Running pre-commit race detection gate..."'
  echo '"${ROOT}/scripts/check-go-race.sh"'
} >"${HOOK_FILE}"

chmod +x "${HOOK_FILE}"
echo "Git pre-commit hook installed at ${HOOK_FILE}."
