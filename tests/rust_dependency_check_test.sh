#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/rust-audit-gate.XXXXXX")"
work="$(cd "$work" && pwd)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/project space/rust" "$work/empty"
printf '[workspace]\n' > "$work/project space/rust/Cargo.toml"
printf 'version = 4\n' > "$work/project space/rust/Cargo.lock"
printf '#!/bin/sh\nexit 0\n' > "$work/bin/cargo-audit"
cat > "$work/bin/cargo" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >> "$AUDIT_GATE_CALLS"
exit "$AUDIT_GATE_EXIT"
STUB
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH" AUDIT_GATE_CALLS="$work/calls"

for scanner_exit in 0 1 7 124; do
  actual=0
  AUDIT_GATE_EXIT="$scanner_exit" bash "$root/tooling/scripts/rust_dependency_check.sh" "$work/project space" > "$work/log" 2>&1 || actual=$?
  [[ "$actual" == "$scanner_exit" ]] || { cat "$work/log"; exit 1; }
done
[[ "$(wc -l < "$work/calls" | tr -d ' ')" == 4 ]]
grep -Fq "audit --file $work/project space/rust/Cargo.lock" "$work/calls"
rm "$work/project space/rust/Cargo.lock"
actual=0
AUDIT_GATE_EXIT=0 bash "$root/tooling/scripts/rust_dependency_check.sh" "$work/project space" > "$work/log" 2>&1 || actual=$?
[[ "$actual" == 1 ]]
grep -Fq 'missing Cargo.lock' "$work/log"
AUDIT_GATE_EXIT=99 bash "$root/tooling/scripts/rust_dependency_check.sh" "$work/empty" > "$work/log"
grep -Fq '0 lockfiles' "$work/log"

mkdir -p "$work/project space/foundation/runtime-sdk/rust/crates/ovrt-wasm-host"
printf 'old lock\n' > "$work/project space/foundation/runtime-sdk/rust/Cargo.lock"
printf 'old manifest\n' > "$work/project space/foundation/runtime-sdk/rust/crates/ovrt-wasm-host/Cargo.toml"
bash "$root/tooling/scripts/scaffold_managed_patches.sh" "$work/project space" --only rust_security > "$work/patch-log"
cmp "$root/runtime-sdk/rust/Cargo.lock" "$work/project space/foundation/runtime-sdk/rust/Cargo.lock"
cmp "$root/runtime-sdk/rust/crates/ovrt-wasm-host/Cargo.toml" "$work/project space/foundation/runtime-sdk/rust/crates/ovrt-wasm-host/Cargo.toml"
cp "$work/project space/.foundation-patches.tsv" "$work/ledger"
bash "$root/tooling/scripts/scaffold_managed_patches.sh" "$work/project space" --only rust_security > "$work/patch-log"
cmp "$work/ledger" "$work/project space/.foundation-patches.tsv"
echo 'Rust audit failure propagation, missing-lock rejection, and security metadata convergence passed'
