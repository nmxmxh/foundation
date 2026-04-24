#!/bin/bash
# Backwards-compatible entrypoint. Implementation lives in scripts/init-project.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/scripts/init-project.sh" "$@"
