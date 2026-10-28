#!/bin/bash
set -euo pipefail

# Installs Git pre-commit hooks to run fast race detection gates.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOK_FILE="${ROOT}/.git/hooks/pre-commit"

mkdir -p "${ROOT}/.git/hooks"

cat > "${HOOK_FILE}" << 'EOF'
#!/bin/bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
echo "Running pre-commit race detection gate..."
"${ROOT}/scripts/check-go-race.sh"
EOF

chmod +x "${HOOK_FILE}"
echo "Git pre-commit hook installed successfully at ${HOOK_FILE}."
