#!/bin/bash
set -euo pipefail

foundation_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
project_dir="${1:?Pass the Pronto project directory}"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/foundation-pronto-binding.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT

# The overlay leaves application files and vendored modules unchanged.
python3 - "$foundation_dir" "$project_dir" "$test_dir" <<'PY'
import json, pathlib, sys
core, app, scratch = map(pathlib.Path, sys.argv[1:])
work = 'go 1.26.0\nuse ' + json.dumps(str(app)) + '\n'
for module in ['config-contracts/go', 'runtime-sdk/go', 'runtime-transport/go', 'server-kit/go']:
    work += 'replace github.com/nmxmxh/ovasabi_foundation/' + module + ' => ' + json.dumps(str(core / module)) + '\n'
(scratch / 'go.work').write_text(work)
(scratch / 'overlay.json').write_text(json.dumps({'Replace': {
    str(app / 'internal/fusionbridge/foundation_binding_reference_test.go'): str(core / 'tests/fixtures/pronto_binding_test.go.txt')
}}))
PY

export GOWORK="$test_dir/go.work"
export GOCACHE="${GO_CACHE_DIR:-/tmp/foundation-substrate-go-cache}"
export PRONTO_FUSION_LIB="${PRONTO_FUSION_LIB:-$project_dir/rust/target/release/libpronto_fusion.dylib}"
cd "$project_dir"
go test -overlay "$test_dir/overlay.json" -count=1 -timeout=90s \
  -run 'TestFoundationNativeResourceBinding|TestFFILibraryAnswersThroughTheCABI' ./internal/fusionbridge
go test -overlay "$test_dir/overlay.json" -run '^$' -timeout=90s \
  -bench BenchmarkFoundationNativeBinding -benchmem -benchtime=300ms -count=5 ./internal/fusionbridge
