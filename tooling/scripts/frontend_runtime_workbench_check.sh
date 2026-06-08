#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

check_file() {
  local label="$1"
  local file="$2"
  if [[ -f "$file" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${file#$target/}"
  fi
}

check_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

docs_dir="$target/docs"
workbench="$target/frontend-kit/ts/src/runtimeWorkbench.ts"
projection_worker_pipeline="$target/frontend-kit/ts/src/projectionWorkerPipeline.ts"
workbench_tests="$target/frontend-kit/ts/src/runtimeWorkbench.test.ts"
indexeddb_tests="$target/frontend-kit/ts/src/indexedDBStorage.test.ts"
profile_tests="$target/frontend-kit/ts/src/runtimeWorkbench.profile.test.ts"
runtime_host_dir="$target/runtime-sdk/ts/browser-host/src"
contracts_dir="$target/runtime-sdk/protocols/system/v1"
profile_script="$target/tooling/scripts/frontend_workbench_profile.sh"
scaffold_smoke="$target/tests/scaffold_smoke_test.sh"
prototype_store_template="$target/templates/frontend/src/stores/prototype.ts"
prototype_generator="$target/tooling/scripts/generate_frontend_prototype_runtime.mjs"

check_file "frontend runtime workbench doc present" "$docs_dir/frontend_runtime_workbench.md"
check_file "runtime SAB/Cap'n Proto doc present" "$docs_dir/runtime_sab_capnp_contracts.md"
check_file "frontend prototype runtime TODO present" "$docs_dir/frontend_prototype_runtime_todo.md"
check_file "frontend workbench source present" "$workbench"
check_file "frontend projection worker pipeline source present" "$projection_worker_pipeline"
check_file "frontend workbench tests present" "$workbench_tests"
check_file "frontend IndexedDB storage tests present" "$indexeddb_tests"
check_file "frontend workbench profile tests present" "$profile_tests"
check_file "frontend workbench profile script present" "$profile_script"
check_file "frontend prototype runtime generator present" "$prototype_generator"
check_file "runtime bridge source present" "$runtime_host_dir/runtimeBridge.ts"
check_file "runtime dispatcher source present" "$runtime_host_dir/runtimeDispatcher.ts"
check_file "runtime module loader source present" "$runtime_host_dir/runtimeModuleLoader.ts"
check_file "runtime worker pool source present" "$runtime_host_dir/runtimeWorkerPool.ts"
check_file "runtime registry reader source present" "$runtime_host_dir/runtimeRegistryReader.ts"

check_contains "workbench has deterministic dummy factory" "$workbench" "createDummyDataFactory"
check_contains "workbench has faker-compatible provider adapter" "$workbench" "createFakerDummyValueProvider"
check_contains "workbench has tenant projection store" "$workbench" "createTenantProjectionStore"
check_contains "workbench supports direct keyed store reads" "$workbench" "get(id: string)"
check_contains "workbench has lazy immutable store snapshots" "$workbench" "createDomainStoreSnapshot"
check_contains "workbench batch apply reports accepted version" "$workbench" "lastAcceptedVersion"
check_contains "workbench has tenant runtime cache" "$workbench" "createPrototypeRuntimeCache"
check_contains "workbench has tenant snapshot persistence" "$workbench" "createTenantSnapshotPersistence"
check_contains "workbench has IndexedDB tenant snapshot persistence" "$workbench" "createIndexedDBTenantSnapshotPersistence"
check_contains "workbench has live projection binding" "$workbench" "createLiveProjectionBinding"
check_contains "workbench live binding batches projection ingestion" "$workbench" "queuedLiveMutations"
check_contains "workbench has Hermes projection adapter" "$workbench" "createHermesProjectionAdapter"
check_contains "workbench Hermes adapter accepts worker normalizer" "$workbench" "normalizer"
check_contains "workbench has abstract runtime adapter" "$workbench" "RuntimeWorkbenchAdapter"
check_contains "workbench manages live loading state" "$workbench" "LiveProjectionStatus"
check_contains "workbench compute is planned before dispatch" "$workbench" "planCompute"
check_contains "workbench omits sensitive dummy fields" "$workbench" "DEFAULT_SENSITIVE_FIELDS"
check_contains "prototype TODO excludes P2P scope" "$docs_dir/frontend_prototype_runtime_todo.md" "No P2P"
check_contains "workbench doc names live projection contract" "$docs_dir/frontend_runtime_workbench.md" "Live Projection Contract"
check_contains "workbench doc names cache contract" "$docs_dir/frontend_runtime_workbench.md" "Cache Contract"
check_contains "workbench doc names persistence contract" "$docs_dir/frontend_runtime_workbench.md" "Persistence Contract"
check_contains "workbench tests cover tenant snapshot reset" "$workbench_tests" "resetSession"
check_contains "workbench tests cover Hermes integration fixture" "$workbench_tests" "Hermes live projection integration fixture"
check_contains "workbench tests cover worker projection normalizer" "$workbench_tests" "worker-backed bounded pipeline"
check_contains "projection worker pipeline has bounded pending queue" "$projection_worker_pipeline" "maxPendingRequests"
check_contains "projection worker pipeline has timeout fallback" "$projection_worker_pipeline" "timeoutMs"
check_contains "projection worker pipeline has local fallback" "$projection_worker_pipeline" "fallbackRuns"
check_contains "projection worker pipeline exposes worker handler installer" "$projection_worker_pipeline" "installProjectionWorkerHandler"
check_contains "workbench profile emits PROFILE metrics" "$profile_tests" "PROFILE"
check_contains "workbench profile covers projection event pipeline" "$profile_tests" "projection_event_pipeline"
check_contains "frontend profile script emits TSV summary" "$profile_script" "frontend_workbench_profile_"
check_contains "scaffold smoke captures frontend artifact logs" "$scaffold_smoke" "frontend-build.log"
check_contains "prototype store template exposes persistence hydrate" "$prototype_store_template" "hydratePersistence"
check_contains "prototype store template accepts projection normalizer" "$prototype_store_template" "projectionNormalizer"
check_contains "prototype generator selectors use direct keyed lookup" "$prototype_generator" "return store.get(selectedId);"

check_file "runtime syscall Cap'n Proto contract present" "$contracts_dir/runtime_syscall.capnp"
check_file "runtime compute Cap'n Proto contract present" "$contracts_dir/runtime_compute.capnp"

if rg -n "new WebSocket|RTCPeerConnection|Atomics\\.wait\\(" "$target/frontend-kit/ts/src" --glob '*.ts' >/tmp/frontend-runtime-workbench-check.txt 2>/dev/null; then
  fail "frontend-kit avoids raw transport/runtime blocking calls" "$(cat /tmp/frontend-runtime-workbench-check.txt)"
else
  ok "frontend-kit avoids raw transport/runtime blocking calls"
fi

if rg -n "getSnapshot\\(\\)\\.byId\\.get" "$workbench" "$prototype_generator" >/tmp/frontend-runtime-workbench-snapshot-read-check.txt 2>/dev/null; then
  fail "frontend hot paths avoid snapshot materialization for keyed reads" "$(cat /tmp/frontend-runtime-workbench-snapshot-read-check.txt)"
else
  ok "frontend hot paths avoid snapshot materialization for keyed reads"
fi

if rg -n "wasm_bindgen|wasm-bindgen" "$target/runtime-sdk/ts/browser-host/src" "$target/frontend-kit/ts/src" --glob '*.ts' >/tmp/frontend-runtime-workbench-bindgen-check.txt 2>/dev/null; then
  fail "runtime browser/frontend public API is not wasm-bindgen-first" "$(cat /tmp/frontend-runtime-workbench-bindgen-check.txt)"
else
  ok "runtime browser/frontend public API is not wasm-bindgen-first"
fi

if rg -n "peer-assisted|peer delegation|active signaling|RTCPeerConnection|WebRTC|webrtc-data-channel|RuntimeMesh|runtime_mesh|activeSignaling|webRtcDataChannel|allowPeerAcceleration" \
    "$runtime_host_dir" \
    "$contracts_dir" \
    --glob '*.ts' --glob '*.capnp' >/tmp/frontend-runtime-workbench-scope-check.txt 2>/dev/null; then
  fail "frontend prototype runtime implementation excludes P2P/mesh/WebRTC" "$(cat /tmp/frontend-runtime-workbench-scope-check.txt)"
else
  ok "frontend prototype runtime implementation excludes P2P/mesh/WebRTC"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "frontend runtime workbench check failed"
  exit 1
fi

echo "frontend runtime workbench check passed"
