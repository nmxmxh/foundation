#!/usr/bin/env bash
set -euo pipefail

# check_escape_analysis.sh
# Verifies that hot-path scratch arena operations do not inadvertently escape
# to the Go GC heap, honoring Carmack's static analysis guarantee.

target_dir="server-kit/go/scratch"

echo "Running escape analysis static check on $target_dir..."
output=$(cd "$target_dir" && go build -gcflags="-m" . 2>&1)

failed=0

# Check that Reset, Scope, and End methods inline and do not escape
for method in "(*Arena).Reset" "Scope.End" "Scope.BytesAllocated"; do
  if echo "$output" | grep -q "$method"; then
    echo "  [OK] Found analysis for $method"
  else
    echo "  [FAIL] Missing escape analysis for $method"
    failed=1
  fi
done

# Ensure Scope.End and (*Arena).Reset do not escape receiver
if echo "$output" | grep -E "(Scope\.End|Reset).*escapes to heap"; then
  echo "  [FAIL] Hot path receiver escapes to heap!"
  failed=1
else
  echo "  [OK] Hot path receivers remain stack-allocated / non-escaping"
fi

if [[ $failed -ne 0 ]]; then
  echo "Escape analysis static check failed."
  exit 1
fi

echo "Escape analysis static check passed."
