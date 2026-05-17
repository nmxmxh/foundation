#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_te_check.out"

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/coverage/**' \
    "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_has_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    "$pattern" "$@" >/dev/null 2>&1; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    failed=1
  fi
}

if rg --files "$target" | rg -q '(\.test|\.spec)\.(ts|tsx|js|jsx)$'; then
  check_no_match "TE no focused TypeScript tests" "(\b(describe|it|test)\.only\s*\(|\.only\s*\()" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx'

  check_no_match "TE no skipped TypeScript tests" "\b(describe|it|test)\.skip\s*\(" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx'

  check_no_match "TE TypeScript tests avoid long fixed waits" "setTimeout\s*\([^,\n]+,\s*([2-9][0-9]{3,}|[1-9][0-9]{4,})\s*\)" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx' \
    --glob '!**/e2e/**' --glob '!**/load/**'
else
  echo "[OK] no TypeScript test files present"
fi

if rg --files "$target" | rg -q '_test\.go$'; then
  check_no_match "TE Go tests avoid extreme fixed sleeps outside load tests" "time\.Sleep\s*\(\s*([5-9][0-9]{2}|[1-9][0-9]{3,})\s*\*\s*time\.Millisecond|time\.Sleep\s*\(\s*([2-9]|[1-9][0-9]+)\s*\*\s*time\.(Second|Minute)|time\.Sleep\s*\(\s*time\.(Second|Minute)\s*\)" "$target" \
    --glob '*_test.go' --glob '!**/tests/load/**' --glob '!**/load/**'

  if [[ -d "$target/api/protos" || -d "$target/server-kit/go/contracttest" || -d "$target/tests/contract" ]]; then
    check_has_match "TE lifecycle/contract tests present for event contracts" "(VerifyProducer|VerifyConsumer|lifecycle|:requested|:success|:failed)" "$target" \
      --glob '*_test.go' --glob '!**/node_modules/**'
  fi
else
  echo "[OK] no Go test files present"
fi

if [[ -f "$target/templates/frontend/package.json" ]]; then
  check_has_match "TE frontend scaffold includes Testing Library React" '"@testing-library/react"' "$target/templates/frontend/package.json"
  check_has_match "TE frontend scaffold includes user-event" '"@testing-library/user-event"' "$target/templates/frontend/package.json"
  check_has_match "TE frontend scaffold includes jsdom" '"jsdom"' "$target/templates/frontend/package.json"
fi

if [[ -f "$target/templates/Makefile" ]]; then
  check_has_match "TE scaffold lint runs testing practices check" "check-testing-practices|testing_practices_check\.sh" "$target/templates/Makefile"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "testing practices check failed"
  exit 1
fi

echo "testing practices check passed"
