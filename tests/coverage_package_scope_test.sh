#!/usr/bin/env bash
set -euo pipefail
core_root="$(cd "$(dirname "$0")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'chmod -R u+w "$fixture"; rm -rf "$fixture"' EXIT
mkdir -p "$fixture/entry" "$fixture/helper"
cat > "$fixture/go.mod" <<'MOD'
module coveragefixture

go 1.26.0
MOD
cat > "$fixture/helper/helper.go" <<'GO'
package helper
func Value() string { return "covered across packages" }
GO
cat > "$fixture/entry/entry.go" <<'GO'
package entry
import "coveragefixture/helper"
func Value() string { return helper.Value() }
GO
cat > "$fixture/entry/entry_test.go" <<'GO'
package entry
import "testing"
func TestValue(t *testing.T) {
 if got:=Value();got!="covered across packages" {t.Fatalf("value=%q",got)}
}
GO
(cd "$fixture" && GOWORK=off make -f "$core_root/templates/Makefile" test-coverage GO_CACHE_DIR="$(go env GOCACHE)" GO_MOD_CACHE_DIR="$(go env GOMODCACHE)")
echo 'Cross-package coverage regression passed'
cat >> "$fixture/helper/helper.go" <<'GO'
func IntegrationOnly() string { return "integration path" }
GO
cat > "$fixture/entry/integration_test.go" <<'GO'
//go:build integration

package entry
import (
 "os"
 "testing"
 "coveragefixture/helper"
)
func TestIntegrationCoverage(t *testing.T) {
 if os.Getenv("TEST_INFRA_REQUIRED")!="true" {t.Fatal("integration prerequisites are not enforced")}
 if helper.IntegrationOnly()!="integration path" {t.Fatal("missing integration coverage")}
}
GO
if (cd "$fixture" && GOWORK=off make -f "$core_root/templates/Makefile" test-coverage GO_CACHE_DIR="$(go env GOCACHE)" GO_MOD_CACHE_DIR="$(go env GOMODCACHE)" >/dev/null 2>&1); then
  echo 'Unit-only coverage unexpectedly passed the unchanged gate' >&2
  exit 1
fi
(cd "$fixture" && CI=true GOWORK=off make -f "$core_root/templates/Makefile" test-coverage MAKE=true GO_COVERAGE_INTEGRATION=true GO_CACHE_DIR="$(go env GOCACHE)" GO_MOD_CACHE_DIR="$(go env GOMODCACHE)")
echo 'Integration coverage regression passed'
